package lint

import (
	"regexp"
	"slices"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/module"
	"github.com/microsoft/typescript-go/internal/parser"
	"github.com/microsoft/typescript-go/internal/tspath"
	"github.com/microsoft/typescript-go/internal/vfs/osvfs"
	"github.com/microsoft/typescript-go/internal/vfs/vfsmatch"
)

// Component files (.astro, .vue, .svelte and .mdx) are in no program, since
// TypeScript can't read them, but their scripts import the analyzed files.
// Those imports count as uses: the component files are the ones that the
// include patterns of the configured tsconfigs match, their import
// statements are read from the syntax of their scripts, and they are
// resolved with the compiler options of the first project that includes
// them. Nothing is reported in component files, and each one counts, even
// when nothing imports it: Astro pages and MDX content are loaded by
// convention.
var componentExtensions = []string{".astro", ".vue", ".svelte", ".mdx"}

func isComponentFile(name string) bool {
	return slices.ContainsFunc(componentExtensions, func(extension string) bool { return strings.HasSuffix(name, extension) })
}

// UsedByComponent reports whether declaration (a node of the file) is what
// a component file imports, also through re-exports, or a class that such a
// class extends. The component may read any of its members.
func (f *File) UsedByComponent(declaration *ast.Node) bool {
	return f.componentUses[declaration.Pos()]
}

// componentImport is an import statement of a component file: the exports
// it names, or all of them for a namespace import, export * and import().
// An import that names nothing, like import "./x" or <script src>, only
// loads the module.
type componentImport struct {
	specifier string
	names     []string
	all       bool
}

// findComponentImports sets ImportedByComponent on the files that component
// files import and marks the declarations they import (see UsedByComponent).
func (c *Context) findComponentImports(gitignored *gitignore) {
	components := c.componentFiles(gitignored)
	if len(components) == 0 {
		return
	}
	byPath := make(map[tspath.Path]*File, len(c.Files)+len(c.Unanalyzed))
	for _, files := range [][]*File{c.Files, c.Unanalyzed} {
		for _, file := range files {
			byPath[file.Source.Path()] = file
		}
	}
	resolvers := map[*Project]*module.Resolver{}
	for _, component := range components {
		if p := component.project; resolvers[p] == nil {
			resolvers[p] = module.NewResolver(p.Program.Host(), p.Program.Options(), "", "")
		}
	}

	type resolvedImport struct {
		target *File
		componentImport
	}
	resolved := make([][]resolvedImport, len(components))
	c.Parallel(len(components), func(i int) {
		p := components[i].project
		host := p.Program.Host()
		text, ok := osvfs.FS().ReadFile(components[i].name)
		if !ok {
			return
		}
		for _, imp := range componentImports(components[i].name, text) {
			resolution, _ := resolvers[p].ResolveModuleName(imp.specifier, components[i].name, core.ResolutionModeESM, nil)
			if !resolution.IsResolved() {
				continue
			}
			// Through a project reference, the import resolves to the
			// referenced project's output, and the program has its source.
			path := tspath.ToPath(resolution.ResolvedFileName, host.GetCurrentDirectory(), host.FS().UseCaseSensitiveFileNames())
			if source := p.Program.GetSourceFileForResolvedModule(resolution.ResolvedFileName); source != nil {
				path = source.Path()
			}
			if target := byPath[path]; target != nil {
				resolved[i] = append(resolved[i], resolvedImport{target, imp})
			}
		}
	})

	type moduleUse struct {
		names map[string]bool
		all   bool
	}
	uses := map[*File]*moduleUse{}
	var targets []*File
	for _, imports := range resolved {
		for _, imp := range imports {
			use := uses[imp.target]
			if use == nil {
				use = &moduleUse{names: map[string]bool{}}
				uses[imp.target] = use
				targets = append(targets, imp.target)
				imp.target.ImportedByComponent = true
			}
			use.all = use.all || imp.all
			for _, name := range imp.names {
				use.names[name] = true
			}
		}
	}
	c.markDeclarations(targets, func(file *File) *map[int]bool { return &file.componentUses }, func(file *File, ch *checker.Checker) []*ast.Symbol {
		moduleSymbol, use := file.Source.Symbol, uses[file]
		if moduleSymbol == nil || use.all {
			return []*ast.Symbol{moduleSymbol}
		}
		var symbols []*ast.Symbol
		for _, export := range ch.GetExportsOfModule(moduleSymbol) {
			if use.names[export.Name] {
				symbols = append(symbols, export)
			}
		}
		return symbols
	})
}

type componentFile struct {
	name    string
	project *Project
}

// componentFiles returns the component files that the projects include,
// each with the first project that includes it: the files with a component
// extension in the directories that the include patterns cover, whose names
// match a pattern. The exclude patterns aren't available, so the files that
// .gitignore ignores are left out instead.
func (c *Context) componentFiles(gitignored *gitignore) []componentFile {
	type directory struct {
		path      string
		recursive bool
	}
	var directories []directory
	for _, p := range c.Projects {
		for path, recursive := range p.project.CommandLine.WildcardDirectories() {
			directories = append(directories, directory{path, recursive})
		}
	}
	// Outer directories first, so that the ones inside them are skipped.
	slices.SortFunc(directories, func(a, b directory) int { return strings.Compare(a.path, b.path) })
	// The programs' file systems only have the files they loaded.
	fs, cwd := osvfs.FS(), c.Projects[0].Program.Host().GetCurrentDirectory()
	var names []string
	var walked []directory
	for _, d := range directories {
		if slices.ContainsFunc(walked, func(w directory) bool {
			return w.path == d.path && (w.recursive || !d.recursive) || w.recursive && strings.HasPrefix(d.path, strings.TrimSuffix(w.path, "/")+"/")
		}) {
			continue
		}
		walked = append(walked, d)
		names = append(names, vfsmatch.ReadDirectory(fs, cwd, d.path, componentExtensions, nil, []string{core.IfElse(d.recursive, "**/*", "*")}, vfsmatch.UnlimitedDepth)...)
	}
	slices.Sort(names)
	var files []componentFile
	for _, name := range slices.Compact(names) {
		if gitignored.ignored(name) {
			continue
		}
		for _, p := range c.Projects {
			if spec, _ := p.project.CommandLine.GetMatchedIncludeSpec(name); spec != "" {
				files = append(files, componentFile{name, p})
				break
			}
		}
	}
	return files
}

var (
	// A script element, closed or self-closing, with its attributes and
	// content. Quoted attribute values may have a >, like Svelte's
	// generics="T extends Record<string, unknown>".
	scriptElement = regexp.MustCompile(`(?is)<script\b((?:[^>"']|"[^"]*"|'[^']*')*?)(?:/>|>(.*?)</script\s*>)`)
	srcAttribute  = regexp.MustCompile(`(?i)\bsrc\s*=\s*(?:"([^"]*)"|'([^']*)')`)
	typeAttribute = regexp.MustCompile(`(?i)\btype\s*=\s*(?:"([^"]*)"|'([^']*)')`)
	// Astro leaves these scripts as they are, so the browser resolves their
	// imports, not the bundler.
	inlineAttribute = regexp.MustCompile(`(?i)\b(is:inline|define:vars)\b`)
	// A frontmatter fence: a line of ---.
	fence = regexp.MustCompile(`(?m)^---[ \t]*\r?$`)
	// The start of an import or export statement of a .mdx file.
	esmStatement = regexp.MustCompile(`^(?:import|export)\b`)
)

// componentImports returns the import statements of a component file's
// scripts (see componentScripts) and the files that its <script src>
// elements load.
func componentImports(fileName, text string) []componentImport {
	var imports []componentImport
	scripts := componentScripts(fileName, text, func(src string) {
		if !strings.HasPrefix(src, ".") && !strings.HasPrefix(src, "/") {
			src = "./" + src
		}
		imports = append(imports, componentImport{specifier: src})
	})
	for _, script := range scripts {
		source := parser.ParseSourceFile(ast.SourceFileParseOptions{FileName: fileName, Path: tspath.Path(fileName)}, script, core.ScriptKindTS)
		var visit func(node *ast.Node) bool
		visit = func(node *ast.Node) bool {
			switch node.Kind {
			case ast.KindImportDeclaration:
				if specifier := node.ModuleSpecifier(); ast.IsStringLiteralLike(specifier) {
					imp := componentImport{specifier: specifier.Text()}
					if clause := node.ImportClause(); clause != nil {
						if clause.Name() != nil {
							imp.names = append(imp.names, "default")
						}
						switch bindings := clause.AsImportClause().NamedBindings; {
						case bindings == nil:
						case bindings.Kind == ast.KindNamespaceImport:
							imp.all = true
						default:
							for _, element := range bindings.AsNamedImports().Elements.Nodes {
								imp.names = append(imp.names, element.PropertyNameOrName().Text())
							}
						}
					}
					imports = append(imports, imp)
				}
			case ast.KindExportDeclaration:
				declaration := node.AsExportDeclaration()
				if specifier := declaration.ModuleSpecifier; specifier != nil && ast.IsStringLiteralLike(specifier) {
					imp := componentImport{specifier: specifier.Text()}
					if clause := declaration.ExportClause; clause == nil || clause.Kind == ast.KindNamespaceExport {
						imp.all = true
					} else {
						for _, element := range clause.AsNamedExports().Elements.Nodes {
							imp.names = append(imp.names, element.PropertyNameOrName().Text())
						}
					}
					imports = append(imports, imp)
				}
			case ast.KindCallExpression:
				if arguments := node.Arguments(); ast.IsImportCall(node) && len(arguments) > 0 && ast.IsStringLiteralLike(arguments[0]) {
					imports = append(imports, componentImport{specifier: arguments[0].Text(), all: true})
				}
			}
			return node.ForEachChild(visit)
		}
		visit(source.AsNode())
	}
	return imports
}

// componentScripts returns the scripts of a component file, and calls src
// with the src of each <script src> element. A .astro file has the script
// between its --- fences and its <script> elements, a .vue or .svelte file
// its <script> elements, and a .mdx file the paragraphs that start with
// import or export, outside code blocks.
func componentScripts(fileName, text string, src func(src string)) []string {
	var scripts []string
	switch {
	case strings.HasSuffix(fileName, ".mdx"):
		_, text = splitFrontmatter(text)
		var statement []string
		inCode, paragraphStart := false, true
		for line := range strings.Lines(text) {
			blank := strings.TrimSpace(line) == ""
			switch {
			case statement != nil && blank:
				scripts = append(scripts, strings.Join(statement, ""))
				statement = nil
			case statement != nil:
				statement = append(statement, line)
			case strings.HasPrefix(strings.TrimLeft(line, " \t"), "```"), strings.HasPrefix(strings.TrimLeft(line, " \t"), "~~~"):
				inCode = !inCode
			case !inCode && paragraphStart && esmStatement.MatchString(line):
				statement = []string{line}
			}
			paragraphStart = blank
		}
		if statement != nil {
			scripts = append(scripts, strings.Join(statement, ""))
		}
		return scripts
	case strings.HasSuffix(fileName, ".astro"):
		var frontmatter string
		frontmatter, text = splitFrontmatter(text)
		scripts = append(scripts, frontmatter)
	}
	for _, match := range scriptElement.FindAllStringSubmatch(text, -1) {
		attributes, content := match[1], match[2]
		if inlineAttribute.MatchString(attributes) {
			continue
		}
		if kind := typeAttribute.FindStringSubmatch(attributes); kind != nil && !strings.EqualFold(kind[1]+kind[2], "module") {
			continue
		}
		if source := srcAttribute.FindStringSubmatch(attributes); source != nil {
			src(source[1] + source[2])
			continue
		}
		scripts = append(scripts, content)
	}
	return scripts
}

// splitFrontmatter returns what a file has between --- fences at its start,
// and the rest of it.
func splitFrontmatter(text string) (frontmatter, rest string) {
	start := fence.FindStringIndex(text)
	if start == nil || strings.TrimSpace(text[:start[0]]) != "" {
		return "", text
	}
	end := fence.FindStringIndex(text[start[1]:])
	if end == nil {
		return "", text
	}
	return text[start[1] : start[1]+end[0]], text[start[1]+end[1]:]
}
