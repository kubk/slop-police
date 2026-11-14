package lint

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"runtime/metrics"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/bundled"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/collections"
	"github.com/microsoft/typescript-go/internal/compiler"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/ls"
	"github.com/microsoft/typescript-go/internal/lsp/lsproto"
	"github.com/microsoft/typescript-go/internal/project"
	"github.com/microsoft/typescript-go/internal/tspath"
	"github.com/microsoft/typescript-go/internal/vfs/osvfs"
)

// Workers is the number of goroutines used for parallel work.
var Workers = runtime.GOMAXPROCS(0)

// FilesPerChecker is the number of analyzed files that justify one more
// checker for a project. Every checker rebuilds its own type caches, so a
// project only gets as many as its size pays for.
var FilesPerChecker = 200

// chunkSize is how many neighbouring files (which tend to share imports, and
// so type-checking work) are assigned to the same checker.
const chunkSize = 64

type Project struct {
	ConfigPath string
	Program    *compiler.Program

	project  *project.Project
	snapshot *project.Snapshot
	files    []*File

	// The checkers are set from the first batch that the project is in
	// until they are released (see forEachBatch), with the contexts to make
	// language service calls on them and the functions that return them to
	// the pool.
	checkers   []*checker.Checker
	checkerCtx []context.Context
	releases   []func()
}

type File struct {
	Index   int
	Source  *ast.SourceFile
	Project *Project
	IsTest  bool
	IsEntry bool // matches the entry option
	RelPath string

	projectIndex int // position among the project's analyzed files
	lineOnce     sync.Once
	lineStarts   []core.TextPos
	public       map[int]bool // positions of public declarations (see IsPublic)
}

type Context struct {
	Projects []*Project // in config order, duplicates included
	Files    []*File    // analyzed files, deduplicated by first project
	// Unanalyzed are the projects' other source files (ignored ones and
	// declaration files), minus libraries. Nothing in them is reported, but
	// rules can count their uses of analyzed code. Their indexes follow those
	// of Files.
	Unanalyzed []*File
	Timing     *Timing
	// HasEntries is set when the config lists entry points: files that
	// something outside the analyzed code loads.
	HasEntries bool

	filesByPath map[tspath.Path]*File
	// requests is the context of the run's checker requests (see
	// takeCheckers).
	requests context.Context
	// missingImports are relative imports of files that don't exist, which
	// no-unresolved-import reports (see checkImports).
	missingImports []Finding

	moduleObjectsOnce     sync.Once
	moduleObjectExports   map[tspath.Path]bool // see HasModuleObjectValue
	moduleObjectReexports map[tspath.Path]bool // see IsReexportedByModuleObjectValue
}

// newProjectSession creates a project session for root, which becomes its
// current directory.
func newProjectSession(root string) *project.Session {
	return project.NewSession(&project.SessionInit{
		BackgroundCtx: context.Background(),
		FS:            bundled.WrapFS(osvfs.FS()),
		Options: &project.SessionOptions{
			CurrentDirectory:   tspath.GetNormalizedAbsolutePath(root, ""),
			DefaultLibraryPath: bundled.LibPath(),
			PositionEncoding:   lsproto.PositionEncodingKindUTF8,
			// A released checker is disposed at once: checkers are released
			// to free memory (see forEachBatch), so their state has to be
			// collectable before the next batch's is built.
			CheckerPoolOptions: project.CheckerPoolOptions{MaxCheckers: Workers + 1, IdleTimeout: time.Nanosecond},
		},
	})
}

// newContext opens the configured projects the way the TypeScript API server
// does (one project session, all projects opened in one request) and
// collects the analyzed files: TypeScript sources among the projects' root
// files, each owned by the first project that lists it, minus ignored ones.
//
// The run's checker requests are made with requests, which the caller cancels
// when the run is over: the checker pools keep a request's checker
// association until its context is done, and until then the context keeps
// the pools, and so the programs, reachable.
func newContext(requests context.Context, session *project.Session, config *Config, timing *Timing) (*Context, error) {
	root := session.GetCurrentDirectory()
	ctx := &Context{Timing: timing, HasEntries: len(config.Entry) > 0, filesByPath: map[tspath.Path]*File{}, requests: requests}
	request := &project.APISnapshotRequest{OpenProjects: &collections.Set[string]{}}
	configPaths := make([]string, len(config.Projects))
	for i, projectPath := range config.Projects {
		configPaths[i] = tspath.GetNormalizedAbsolutePath(projectPath, root)
		if _, err := os.Stat(configPaths[i]); err != nil {
			return nil, configErrorf("TypeScript project not found: %s", projectPath)
		}
		request.OpenProjects.Add(configPaths[i])
	}
	snapshot, err := session.APIUpdate(context.Background(), project.FileChangeSummary{}, request)
	if err != nil {
		return nil, fmt.Errorf("failed to open projects: %w", err)
	}
	timing.Mark("load programs")

	byPath := map[tspath.Path]*Project{}
	for _, configPath := range configPaths {
		path := tspath.ToPath(configPath, root, session.FS().UseCaseSensitiveFileNames())
		p := byPath[path]
		if p == nil {
			proj := snapshot.ProjectCollection.GetProjectByPath(path)
			if proj == nil || proj.CommandLine == nil || proj.GetProgram() == nil {
				return nil, configErrorf("Unable to open TypeScript project: %s", relativePath(root, configPath))
			}
			p = &Project{ConfigPath: configPath, Program: proj.GetProgram(), project: proj, snapshot: snapshot}
			byPath[path] = p
		}
		ctx.Projects = append(ctx.Projects, p)
	}

	gitignored := newGitignore(root)
	for _, p := range ctx.Projects {
		for _, fileName := range p.project.CommandLine.FileNames() {
			if !isTypeScriptSource(fileName) {
				continue
			}
			sf := p.Program.GetSourceFile(fileName)
			if sf == nil || ctx.filesByPath[sf.Path()] != nil {
				continue
			}
			rel := relativePath(root, sf.FileName())
			if MatchesAny(config.Ignore, rel) || gitignored.ignored(sf.FileName()) {
				continue
			}
			file := &File{Index: len(ctx.Files), Source: sf, Project: p, IsTest: MatchesAny(config.TestFiles, rel), IsEntry: MatchesAny(config.Entry, rel), RelPath: rel, projectIndex: len(p.files)}
			p.files = append(p.files, file)
			ctx.filesByPath[sf.Path()] = file
			ctx.Files = append(ctx.Files, file)
		}
	}
	unanalyzed := map[tspath.Path]bool{}
	for _, p := range ctx.Projects {
		for _, sf := range p.Program.GetSourceFiles() {
			if ctx.filesByPath[sf.Path()] != nil || unanalyzed[sf.Path()] || p.Program.IsSourceFileDefaultLibrary(sf.Path()) || p.Program.IsSourceFileFromExternalLibrary(sf) || strings.Contains(sf.FileName(), "/node_modules/") {
				continue
			}
			unanalyzed[sf.Path()] = true
			rel := relativePath(root, sf.FileName())
			ctx.Unanalyzed = append(ctx.Unanalyzed, &File{Index: len(ctx.Files) + len(ctx.Unanalyzed), Source: sf, Project: p, IsTest: MatchesAny(config.TestFiles, rel), IsEntry: MatchesAny(config.Entry, rel), RelPath: rel, projectIndex: len(p.files) + len(ctx.Unanalyzed)})
		}
	}
	timing.Mark(fmt.Sprintf("collect files (%d)", len(ctx.Files)))
	if err := checkImports(ctx); err != nil {
		return nil, err
	}
	if ctx.HasEntries {
		ctx.markPublicDeclarations()
		timing.Mark("public declarations")
	}
	return ctx, nil
}

func isTypeScriptSource(fileName string) bool {
	for _, ext := range []string{".d.ts", ".d.mts", ".d.cts"} {
		if strings.HasSuffix(fileName, ext) {
			return false
		}
	}
	for _, ext := range []string{".ts", ".tsx", ".mts", ".cts"} {
		if strings.HasSuffix(fileName, ext) {
			return true
		}
	}
	return false
}

func relativePath(root, fileName string) string {
	rel, err := filepath.Rel(root, fileName)
	if err != nil {
		return fileName
	}
	return rel
}

// MatchesAny reports whether path (relative to the config) matches one of
// the glob patterns.
func MatchesAny(patterns []string, path string) bool {
	for _, pattern := range patterns {
		if ok, _ := doublestar.Match(pattern, path); ok {
			return true
		}
	}
	return false
}

func (c *Context) IsAnalyzedPath(path tspath.Path) bool {
	return c.filesByPath[path] != nil
}

// ProjectsContaining returns the configured projects (in config order,
// duplicates included) whose program contains the file.
func (c *Context) ProjectsContaining(sf *ast.SourceFile) []*Project {
	var result []*Project
	for _, p := range c.Projects {
		if p.Program.GetSourceFileByPath(sf.Path()) != nil {
			result = append(result, p)
		}
	}
	return result
}

// checkerCount is how many checkers the project's analyzed files pay for.
func (p *Project) checkerCount() int {
	return max(1, min((len(p.files)+FilesPerChecker-1)/FilesPerChecker, Workers))
}

// MemoryBudget is the heap size up to which a run spends memory to be fast:
// the projects keep their checkers. Over it, the checkers are released
// between batches (see forEachBatch). The command sets it, and keeps the
// garbage collector off up to the same size (see cmd/slop-police/tune.go):
// a released checker frees nothing until the collector runs.
var MemoryBudget uint64 = math.MaxUint64

// heapSize is the size of the heap's objects, the dead ones that the garbage
// collector hasn't freed yet included.
func heapSize() uint64 {
	sample := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
	metrics.Read(sample)
	return sample[0].Value.Uint64()
}

// takeCheckers takes the project's checkers from its checker pool, unless it
// has them, under per-checker request IDs so that language service calls
// made with checkerCtx[i] use checkers[i].
func (c *Context) takeCheckers(p *Project) {
	if p.checkers != nil {
		return
	}
	count := p.checkerCount()
	p.checkers = make([]*checker.Checker, count)
	p.releases = make([]func(), count)
	p.checkerCtx = make([]context.Context, count)
	for i := range count {
		p.checkerCtx[i] = core.WithRequestID(c.requests, fmt.Sprintf("slop-police:%s:%d", p.ConfigPath, i))
		p.checkers[i], p.releases[i] = p.Program.GetTypeChecker(p.checkerCtx[i])
	}
}

// releaseCheckers returns the checkers of every project that keep rejects to
// their pools, which dispose them (see newProjectSession), so that their
// state can be collected.
func (c *Context) releaseCheckers(keep func(p *Project) bool) {
	for _, p := range c.Projects {
		if p.checkers == nil || keep(p) {
			continue
		}
		for _, release := range p.releases {
			release()
		}
		p.checkers, p.releases, p.checkerCtx = nil, nil, nil
	}
}

// forEachBatch runs fn for batches of the projects, with their checkers
// taken. A batch has as many projects as it takes to have Workers checkers,
// so that the parallel work in fn keeps every worker busy. The batches run
// one after another.
//
// The projects keep their checkers after fn: a checker that the next pass
// takes again would have to resolve the same types again. While the heap is
// over MemoryBudget, the checkers of the projects outside a batch are
// released before the batch's are taken, so that only about Workers
// checkers' state is live at a time, however many projects there are.
func (c *Context) forEachBatch(projects []*Project, fn func(batch []*Project)) {
	for start := 0; start < len(projects); {
		end, checkers := start, 0
		for end < len(projects) && checkers < Workers {
			checkers += projects[end].checkerCount()
			end++
		}
		batch := projects[start:end]
		start = end
		if heapSize() > MemoryBudget {
			c.releaseCheckers(func(p *Project) bool { return slices.Contains(batch, p) })
		}
		for _, p := range batch {
			c.takeCheckers(p)
		}
		fn(batch)
	}
}

// ForEachFile runs fn for every file in parallel (see forEachChecker). Each
// file of a project goes to the same checker as its neighbours, and a
// checker is used by one goroutine at a time.
func (c *Context) ForEachFile(files []*File, fn func(f *File, ch *checker.Checker)) {
	work := checkerWork{}
	for _, f := range files {
		work.add(f.Project, f.projectIndex, programFile{f, f.Source})
	}
	c.forEachChecker(work, func(_ *Project, pf programFile, ch *checker.Checker) { fn(pf.file, ch) })
}

// ForEachProgramFile runs fn like ForEachFile, but for every file that
// include accepts in the program of each of the projects. A program also has
// the files that its project's files import: files of other projects,
// ignored files and declaration files. source is the program's parse of the
// file, which another program can have another one of.
func (c *Context) ForEachProgramFile(projects []*Project, include func(p *Project, source *ast.SourceFile) bool, fn func(p *Project, source *ast.SourceFile, ch *checker.Checker)) {
	work := checkerWork{}
	for _, p := range projects {
		if work.byProject[p] != nil {
			continue
		}
		for i, source := range p.Program.GetSourceFiles() {
			if !include(p, source) {
				continue
			}
			// The project's own files go to the checkers that ForEachFile
			// checks them with.
			f := c.filesByPath[source.Path()]
			if f != nil && f.Project == p {
				i = f.projectIndex
			}
			work.add(p, i, programFile{f, source})
		}
	}
	c.forEachChecker(work, func(p *Project, pf programFile, ch *checker.Checker) { fn(p, pf.source, ch) })
}

// checkerWork is the files to visit with each checker of the projects.
type checkerWork struct {
	projects  []*Project
	byProject map[*Project][][]programFile // by checker index
}

// programFile is a program's parse of a file, and the file if it's analyzed.
type programFile struct {
	file   *File
	source *ast.SourceFile
}

// add adds the file at index in its project: neighbours go to the same
// checker.
func (w *checkerWork) add(p *Project, index int, pf programFile) {
	if w.byProject == nil {
		w.byProject = map[*Project][][]programFile{}
	}
	if w.byProject[p] == nil {
		w.byProject[p] = make([][]programFile, p.checkerCount())
		w.projects = append(w.projects, p)
	}
	i := (index / chunkSize) % len(w.byProject[p])
	w.byProject[p][i] = append(w.byProject[p][i], pf)
}

// forEachChecker runs fn for the files of each checker in work, one
// goroutine per checker, Workers checkers at a time. The checkers go to the
// workers from one queue in project order, so that a worker that finishes a
// small project starts on the next one at once.
//
// The projects keep their checkers afterwards, like in forEachBatch. While
// the heap is over MemoryBudget, a worker that starts on a checker first
// releases the checkers of the projects that no worker is in the middle of.
func (c *Context) forEachChecker(work checkerWork, fn func(p *Project, pf programFile, ch *checker.Checker)) {
	type group struct {
		project *Project
		index   int
	}
	var groups []group
	started, pending := map[*Project]int{}, map[*Project]int{}
	for _, p := range work.projects {
		for i, files := range work.byProject[p] {
			if len(files) > 0 {
				groups = append(groups, group{p, i})
				pending[p]++
			}
		}
	}
	var mu sync.Mutex
	c.Parallel(len(groups), func(gi int) {
		g := groups[gi]
		mu.Lock()
		started[g.project]++
		if heapSize() > MemoryBudget {
			c.releaseCheckers(func(p *Project) bool { return started[p] > 0 && pending[p] > 0 })
		}
		c.takeCheckers(g.project)
		ch := g.project.checkers[g.index]
		mu.Unlock()
		for _, pf := range work.byProject[g.project][g.index] {
			func() {
				defer rethrow("", pf.file)
				fn(g.project, pf, ch)
			}()
		}
		mu.Lock()
		pending[g.project]--
		mu.Unlock()
	})
}

// Parallel runs fn(i) for i in [0, n) on Workers goroutines. A panic in fn
// stops the remaining calls and is raised again in the caller's goroutine.
func (c *Context) Parallel(n int, fn func(i int)) {
	var next atomic.Int64
	var wg sync.WaitGroup
	var crash atomic.Pointer[CrashError]
	for range min(Workers, n) {
		wg.Go(func() {
			defer func() {
				if r := recover(); r != nil {
					crash.CompareAndSwap(nil, asCrash(r, "", nil))
					next.Store(int64(n))
				}
			}()
			for i := int(next.Add(1) - 1); i < n; i = int(next.Add(1) - 1) {
				fn(i)
			}
		})
	}
	wg.Wait()
	if crash := crash.Load(); crash != nil {
		panic(crash)
	}
}

// ReferenceQuery is a find-all-references request at Node, a node of
// Project's program, with the position argument the API passes.
type ReferenceQuery struct {
	Project  *Project
	Node     *ast.Node
	Position int
}

// AnyReference runs each query exactly like the API server's
// getReferencedSymbolsForNode (find-all-references over all source files of
// the project's program, on one checker) and reports whether accept takes any
// reference node of the query (by its index). Queries run in parallel on the
// project's checkers, projects in batches (see forEachBatch).
func (c *Context) AnyReference(queries []ReferenceQuery, accept func(query int, ref *ast.Node) bool) []bool {
	byProject := map[*Project][]int{}
	var projects []*Project
	for i, q := range queries {
		if byProject[q.Project] == nil {
			projects = append(projects, q.Project)
		}
		byProject[q.Project] = append(byProject[q.Project], i)
	}
	// Find-all-references looks up every file's name table. Building them is
	// a pure, cached computation; doing it up front runs it in parallel.
	var files []*ast.SourceFile
	for _, p := range projects {
		files = append(files, p.Program.GetSourceFiles()...)
	}
	c.Parallel(len(files), func(i int) { files[i].GetNameTable() })

	type worker struct {
		project *Project
		index   int
		next    *atomic.Int64
	}
	found := make([]bool, len(queries))
	c.forEachBatch(projects, func(batch []*Project) {
		var workers []worker
		for _, p := range batch {
			next := &atomic.Int64{}
			for i := range min(len(p.checkers), len(byProject[p])) {
				workers = append(workers, worker{p, i, next})
			}
		}
		c.Parallel(len(workers), func(wi int) {
			w := workers[wi]
			service := ls.NewLanguageService(w.project.project.ID(), w.project.Program, w.project.snapshot, "")
			indexes := byProject[w.project]
			for i := int(w.next.Add(1) - 1); i < len(indexes); i = int(w.next.Add(1) - 1) {
				q := queries[indexes[i]]
				entries := service.GetReferencedSymbolsForNode(w.project.checkerCtx[w.index], q.Position, q.Node, w.project.Program.GetSourceFiles())
				found[indexes[i]] = anyAccepted(entries, func(ref *ast.Node) bool { return accept(indexes[i], ref) })
			}
		})
	})
	return found
}

func anyAccepted(entries []*ls.SymbolAndEntries, accept func(ref *ast.Node) bool) bool {
	for _, entry := range entries {
		if entry.DefinitionNode() == nil {
			continue
		}
		for _, ref := range entry.References() {
			if ref.IsNodeEntry() && accept(ref.Node()) {
				return true
			}
		}
	}
	return false
}
