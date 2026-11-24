package lint

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/microsoft/typescript-go/internal/bundled"
	"github.com/microsoft/typescript-go/internal/collections"
	"github.com/microsoft/typescript-go/internal/compiler"
	"github.com/microsoft/typescript-go/internal/tsoptions"
	"github.com/microsoft/typescript-go/internal/vfs/osvfs"
)

// defaultConfigFile is the config slop-police runs with when the directory
// has no slop-police.json, in the order it's printed.
type defaultConfigFile struct {
	Projects  []string `json:"projects"`
	Ignore    []string `json:"ignore"`
	TestFiles []string `json:"testFiles"`
}

// defaultConfig builds the config for a root without slop-police.json and
// saves it there, so later runs use it as the user's own: the
// tsconfigs under root, generated code and shadcn components (see
// components.json) as ignored, and common test file names. It has no entry
// points: one that a guess misses, like a script or a framework's file,
// would be reported as never imported.
func defaultConfig(root string, pluginRules []*Rule) (*Config, error) {
	gitignored := newGitignore(root)
	var tsconfigs []string
	ignore := []string{"**/*.gen.ts", "**/*.generated.{ts,tsx}", "**/__generated__/**"}
	_ = filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		base := entry.Name()
		if entry.IsDir() {
			rel, _ := filepath.Rel(gitignored.base, name)
			if name != root && (base == "node_modules" || base == ".git" || gitignored.dirIgnored(filepath.ToSlash(rel))) {
				return filepath.SkipDir
			}
			return nil
		}
		switch {
		case gitignored.ignored(name):
		case base == "tsconfig.json" || strings.HasPrefix(base, "tsconfig.") && strings.HasSuffix(base, ".json"):
			tsconfigs = append(tsconfigs, name)
		case base == "components.json":
			if dir := shadcnDirectory(filepath.Dir(name)); dir != "" {
				ignore = append(ignore, relativePath(root, dir)+"/**")
			}
		}
		return nil
	})

	file := defaultConfigFile{
		Projects:  defaultProjects(root, tsconfigs),
		Ignore:    ignore,
		TestFiles: []string{"**/*.test.{ts,tsx}", "**/*.spec.{ts,tsx}", "**/*.test-d.ts", "**/__tests__/**", "**/__mocks__/**", "**/__fixtures__/**", "**/fixtures/**"},
	}
	if len(file.Projects) == 0 {
		return nil, configErrorf(`%s not found, and there's no tsconfig.json with TypeScript files to run without it.

To set it up, add a %s with your "projects" and "entry" points:
  https://github.com/kubk/slop-police#configuration`, configFileName, configFileName)
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return nil, err
	}
	config, err := ParseConfig(data, pluginRules)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(root, configFileName), append(data, '\n'), 0o644); err != nil {
		return nil, configErrorf("Unable to save a default %s: %v", configFileName, err)
	}
	config.Default = string(data)
	return config, nil
}

// defaultProjects picks the tsconfigs to analyze among the ones found, as
// paths relative to root. Each file belongs to the nearest tsconfig, the one
// an editor uses for it, so deeper tsconfigs come first, and tsconfig.json
// before its siblings. A tsconfig is left out when it adds no TypeScript
// files to the ones before it, like a solution-style tsconfig or a build
// variant, and when it's a shared base that others extend and that lists
// no files of its own: as a project, it would take in every file under its
// directory with options meant to be extended.
func defaultProjects(root string, tsconfigs []string) []string {
	depth := func(name string) int { return strings.Count(name, string(filepath.Separator)) }
	slices.SortFunc(tsconfigs, func(a, b string) int {
		if d := depth(b) - depth(a); d != 0 {
			return d
		}
		if filepath.Dir(a) == filepath.Dir(b) && (filepath.Base(a) == "tsconfig.json") != (filepath.Base(b) == "tsconfig.json") {
			if filepath.Base(a) == "tsconfig.json" {
				return -1
			}
			return 1
		}
		return strings.Compare(a, b)
	})
	host := compiler.NewCompilerHost(root, bundled.WrapFS(osvfs.FS()), bundled.LibPath(), nil, nil)
	parsed := make([]*tsoptions.ParsedCommandLine, len(tsconfigs))
	extended := map[string]bool{}
	for i, name := range tsconfigs {
		parsed[i], _ = tsoptions.GetParsedCommandLineOfConfigFile(name, nil, nil, host, nil)
		for _, base := range parsed[i].ExtendedSourceFiles() {
			extended[base] = true
		}
	}
	var projects []string
	covered := map[string]bool{}
	for i, name := range tsconfigs {
		if parsed[i] == nil {
			continue
		}
		if raw, ok := parsed[i].Raw.(*collections.OrderedMap[string, any]); ok && extended[name] && !raw.Has("include") && !raw.Has("files") {
			continue
		}
		adds := false
		for _, fileName := range parsed[i].FileNames() {
			if isTypeScriptSource(fileName) && !covered[fileName] {
				covered[fileName] = true
				adds = true
			}
		}
		if adds {
			projects = append(projects, relativePath(root, name))
		}
	}
	return projects
}

// shadcnDirectory returns the directory where shadcn/ui puts the components
// it copies in, from the components.json in dir, or "" when it isn't there.
// Its aliases, like @/components/ui or src/ui, usually stand for a
// directory under dir or under dir/src.
func shadcnDirectory(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "components.json"))
	if err != nil {
		return ""
	}
	var config struct {
		Aliases struct{ Components, UI string } `json:"aliases"`
	}
	if json.Unmarshal(data, &config) != nil {
		return ""
	}
	alias := config.Aliases.UI
	if alias == "" && config.Aliases.Components != "" {
		alias = config.Aliases.Components + "/ui"
	}
	if alias == "" {
		return ""
	}
	for _, prefix := range []string{"@/", "~/", "#/"} {
		alias = strings.TrimPrefix(alias, prefix)
	}
	for _, candidate := range []string{filepath.Join(dir, "src", alias), filepath.Join(dir, alias)} {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}
	return ""
}
