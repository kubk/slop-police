package lint

import (
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// gitignore matches paths against the .gitignore files from the repository
// root (or root, outside a repository) down to each path's directory, as git
// does for untracked files: the last matching pattern wins, a deeper
// .gitignore overrides the ones above, and nothing inside an ignored
// directory can be included again.
type gitignore struct {
	base     string
	patterns map[string][]gitignorePattern // by directory relative to base
	dirs     map[string]bool               // whether a directory is ignored
}

type gitignorePattern struct {
	glob    string // relative to the directory of the .gitignore
	negate  bool
	dirOnly bool
}

func newGitignore(root string) *gitignore {
	base := root
	for dir := root; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			base = dir
			break
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	return &gitignore{base: base, patterns: map[string][]gitignorePattern{}, dirs: map[string]bool{}}
}

// ignored reports whether the file at fileName is ignored.
func (g *gitignore) ignored(fileName string) bool {
	rel, err := filepath.Rel(g.base, fileName)
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return false
	}
	if dir := path.Dir(rel); dir != "." && g.dirIgnored(dir) {
		return true
	}
	return g.matches(rel, false)
}

func (g *gitignore) dirIgnored(dir string) bool {
	if ignored, ok := g.dirs[dir]; ok {
		return ignored
	}
	parent := path.Dir(dir)
	ignored := parent != "." && g.dirIgnored(parent) || g.matches(dir, true)
	g.dirs[dir] = ignored
	return ignored
}

// matches applies the patterns of the .gitignore files above rel.
func (g *gitignore) matches(rel string, isDir bool) bool {
	ignored := false
	dir := ""
	for {
		sub := strings.TrimPrefix(rel, dir)
		for _, pattern := range g.load(strings.TrimSuffix(dir, "/")) {
			if pattern.dirOnly && !isDir {
				continue
			}
			if ok, _ := doublestar.Match(pattern.glob, sub); ok {
				ignored = !pattern.negate
			}
		}
		next := strings.IndexByte(sub, '/')
		if next < 0 {
			return ignored
		}
		dir += sub[:next+1]
	}
}

func (g *gitignore) load(dir string) []gitignorePattern {
	if patterns, ok := g.patterns[dir]; ok {
		return patterns
	}
	data, _ := os.ReadFile(filepath.Join(g.base, dir, ".gitignore"))
	var patterns []gitignorePattern
	for line := range strings.Lines(string(data)) {
		line = strings.TrimRight(line, "\r\n")
		if !strings.HasSuffix(line, `\ `) {
			line = strings.TrimRight(line, " ")
		}
		if line == "" || line[0] == '#' {
			continue
		}
		var pattern gitignorePattern
		if line[0] == '!' {
			pattern.negate = true
			line = line[1:]
		} else if line[0] == '\\' && len(line) > 1 && (line[1] == '!' || line[1] == '#') {
			line = line[1:]
		}
		if strings.HasSuffix(line, "/") {
			pattern.dirOnly = true
			line = strings.TrimRight(line, "/")
		}
		// A pattern with a slash is relative to the .gitignore; one without
		// matches at any depth.
		if strings.Contains(line, "/") {
			pattern.glob = strings.TrimPrefix(line, "/")
		} else {
			pattern.glob = "**/" + line
		}
		if pattern.glob != "" && pattern.glob != "**/" {
			patterns = append(patterns, pattern)
		}
	}
	g.patterns[dir] = patterns
	return patterns
}
