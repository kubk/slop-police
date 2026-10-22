package lint

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"

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

// ChunkSize is how many neighbouring files (which tend to share imports, and
// so type-checking work) are assigned to the same checker.
const ChunkSize = 64

type Project struct {
	ConfigPath string
	Program    *compiler.Program

	project  *project.Project
	snapshot *project.Snapshot
	files    []*File

	// Checkers are taken from the project's checker pool and held for the
	// whole run under per-checker request IDs, so that language service calls
	// made with checkerCtx[i] use checkers[i] (and its warm caches).
	checkersOnce sync.Once
	checkers     []*checker.Checker
	checkerCtx   []context.Context
	checkerLocks []sync.Mutex
}

type File struct {
	Index   int
	Source  *ast.SourceFile
	Project *Project
	IsTest  bool
	RelPath string

	projectIndex int // position among the project's analyzed files
	lineOnce     sync.Once
	lineStarts   []core.TextPos
}

type Context struct {
	Root     string
	Projects []*Project // in config order, duplicates included
	Files    []*File    // analyzed files, deduplicated by first project
	Timing   *Timing

	filesByPath map[tspath.Path]*File
}

// NewContext opens the configured projects the way the TypeScript API server
// does (one project session, all projects opened in one request) and
// collects the analyzed files: TypeScript sources among the projects' root
// files, each owned by the first project that lists it, minus ignored ones.
func NewContext(root string, config *Config, timing *Timing) (*Context, error) {
	ctx := &Context{Root: root, Timing: timing, filesByPath: map[tspath.Path]*File{}}
	fs := bundled.WrapFS(osvfs.FS())
	session := project.NewSession(&project.SessionInit{
		BackgroundCtx: context.Background(),
		FS:            fs,
		Options: &project.SessionOptions{
			CurrentDirectory:   root,
			DefaultLibraryPath: bundled.LibPath(),
			PositionEncoding:   lsproto.PositionEncodingKindUTF8,
			CheckerPoolOptions: project.CheckerPoolOptions{MaxCheckers: Workers + 1},
		},
	})
	request := &project.APISnapshotRequest{OpenProjects: &collections.Set[string]{}}
	configPaths := make([]string, len(config.Projects))
	for i, projectPath := range config.Projects {
		configPaths[i] = projectPath
		if !filepath.IsAbs(projectPath) {
			configPaths[i] = filepath.Join(root, projectPath)
		}
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
		path := tspath.ToPath(configPath, root, fs.UseCaseSensitiveFileNames())
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
			if matchesAny(config.Ignore, rel) {
				continue
			}
			file := &File{Index: len(ctx.Files), Source: sf, Project: p, IsTest: matchesAny(config.TestFiles, rel), RelPath: rel, projectIndex: len(p.files)}
			p.files = append(p.files, file)
			ctx.filesByPath[sf.Path()] = file
			ctx.Files = append(ctx.Files, file)
		}
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
		return filepath.ToSlash(fileName)
	}
	return filepath.ToSlash(rel)
}

func matchesAny(patterns []string, path string) bool {
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

// FileOf returns the analyzed file containing node, or nil.
func (c *Context) FileOf(node *ast.Node) *File {
	return c.filesByPath[ast.GetSourceFileOfNode(node).Path()]
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

// runContext is never cancelled: the checker pool keeps a request's checker
// association until its context is done.
var runContext, _ = context.WithCancel(context.Background())

func (c *Context) ensureCheckers(p *Project) {
	p.checkersOnce.Do(func() {
		count := max(1, min((len(p.files)+FilesPerChecker-1)/FilesPerChecker, Workers))
		p.checkers = make([]*checker.Checker, count)
		p.checkerCtx = make([]context.Context, count)
		p.checkerLocks = make([]sync.Mutex, count)
		for i := range count {
			p.checkerCtx[i] = core.WithRequestID(runContext, fmt.Sprintf("slop-police:%s:%d", p.ConfigPath, i))
			p.checkers[i], _ = p.Program.GetTypeChecker(p.checkerCtx[i])
		}
	})
}

// ForEachFile runs fn for every file in parallel. Each file always goes to
// the same checker of its project, and a checker is used by one goroutine at
// a time.
func (c *Context) ForEachFile(files []*File, fn func(f *File, ch *checker.Checker)) {
	type group struct {
		project *Project
		index   int
		files   []*File
	}
	type key struct {
		project *Project
		index   int
	}
	groupIndex := map[key]int{}
	var groups []*group
	for _, f := range files {
		c.ensureCheckers(f.Project)
		k := key{f.Project, (f.projectIndex / ChunkSize) % len(f.Project.checkers)}
		i, ok := groupIndex[k]
		if !ok {
			i = len(groups)
			groupIndex[k] = i
			groups = append(groups, &group{project: k.project, index: k.index})
		}
		groups[i].files = append(groups[i].files, f)
	}
	c.Parallel(len(groups), func(i int) {
		g := groups[i]
		g.project.checkerLocks[g.index].Lock()
		defer g.project.checkerLocks[g.index].Unlock()
		for _, f := range g.files {
			fn(f, g.project.checkers[g.index])
		}
	})
}

// Parallel runs fn(i) for i in [0, n) on Workers goroutines.
func (c *Context) Parallel(n int, fn func(i int)) {
	var next atomic.Int64
	var wg sync.WaitGroup
	for range min(Workers, n) {
		wg.Go(func() {
			for i := int(next.Add(1) - 1); i < n; i = int(next.Add(1) - 1) {
				fn(i)
			}
		})
	}
	wg.Wait()
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
// the project's program, on one checker) and reports whether any reference
// node is accepted. Queries run in parallel on the project's checkers.
func (c *Context) AnyReference(queries []ReferenceQuery, accept func(ref *ast.Node) bool) []bool {
	byProject := map[*Project][]int{}
	var projects []*Project
	for i, q := range queries {
		if byProject[q.Project] == nil {
			projects = append(projects, q.Project)
		}
		byProject[q.Project] = append(byProject[q.Project], i)
		c.ensureCheckers(q.Project)
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
	var workers []worker
	for _, p := range projects {
		next := &atomic.Int64{}
		for i := range min(len(p.checkers), len(byProject[p])) {
			workers = append(workers, worker{p, i, next})
		}
	}
	found := make([]bool, len(queries))
	c.Parallel(len(workers), func(wi int) {
		w := workers[wi]
		w.project.checkerLocks[w.index].Lock()
		defer w.project.checkerLocks[w.index].Unlock()
		service := ls.NewLanguageService(w.project.project.ID(), w.project.Program, w.project.snapshot, "")
		indexes := byProject[w.project]
		for i := int(w.next.Add(1) - 1); i < len(indexes); i = int(w.next.Add(1) - 1) {
			q := queries[indexes[i]]
			entries := service.GetReferencedSymbolsForNode(w.project.checkerCtx[w.index], q.Position, q.Node, w.project.Program.GetSourceFiles())
			found[indexes[i]] = anyAccepted(entries, accept)
		}
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

// ResolveSymbol is getSymbolAtLocation, followed by getAliasedSymbol for
// alias symbols when followAliases is set.
func ResolveSymbol(ch *checker.Checker, node *ast.Node, followAliases bool) *ast.Symbol {
	symbol := ch.GetSymbolAtLocation(node)
	if followAliases && symbol != nil && symbol.Flags&ast.SymbolFlagsAlias != 0 {
		symbol = ch.GetAliasedSymbol(symbol)
	}
	return symbol
}
