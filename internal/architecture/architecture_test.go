// Package architecture checks the dependency rules between the packages
// under internal/. The rules are what keeps the layout from eroding as the
// codebase grows, so they are tested instead of left to code review.
package architecture

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const module = "github.com/example/go-skeleton/"

// allowed lists, for each area under internal/, the areas its packages may
// import. Everything else in the module is forbidden; packages outside the
// module are not restricted. Test files are not checked.
var allowed = map[string][]string{
	// The shared kernel: errors and the caller's identity. It imports
	// nothing, so everything can import it.
	"domain": {},
	// Infrastructure clients know nothing about features.
	"platform": {"domain", "platform"},
	// A feature never imports another feature or the servers: what it needs
	// from another feature it declares as an interface, and the application
	// wiring connects the two.
	"feature": {"domain", "platform", "gen"},
	// The servers hold what all endpoints share. The features plug into
	// them, not the other way around.
	"server": {"domain", "platform", "gen", "server"},
	// Only the wiring sees everything.
	"application": {"domain", "platform", "gen", "feature", "server", "config", "application"},
	"config":      {},
	// Test helpers. No area lists testkit, so only test files can import it.
	"testkit": {},
	"gen":     {"gen"},
}

func TestDependencyRules(t *testing.T) {
	for _, file := range goFiles(t) {
		area, pkg := split(file.dir)
		rules, ok := allowed[area]
		if !ok {
			t.Errorf("%s: no dependency rules for internal/%s; add them to this test", file.path, area)
			continue
		}
		for _, imp := range file.ast.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			target, ok := strings.CutPrefix(path, module+"internal/")
			if !ok {
				if strings.HasPrefix(path, module) && area != "application" {
					t.Errorf("%s imports %s: only the application wiring may import packages outside internal/", file.path, path)
				}
				continue
			}
			targetArea, targetPkg := split(target)
			switch {
			case area == "feature" && targetArea == "feature":
				if targetPkg != pkg {
					t.Errorf("%s imports %s: a feature must not import another feature", file.path, path)
				}
			case !slices.Contains(rules, targetArea):
				t.Errorf("%s imports %s: internal/%s must not depend on internal/%s", file.path, path, area, targetArea)
			}
		}
	}
}

// Tags tie a struct to a storage or wire format. The types every feature
// shares must stay free of them; each feature maps to formats of its own.
func TestDomainHasNoStructTags(t *testing.T) {
	for _, file := range goFiles(t) {
		if area, _ := split(file.dir); area != "domain" {
			continue
		}
		ast.Inspect(file.ast, func(n ast.Node) bool {
			if field, ok := n.(*ast.Field); ok && field.Tag != nil {
				t.Errorf("%s: struct tag %s in the domain package", file.path, field.Tag.Value)
			}
			return true
		})
	}
}

type goFile struct {
	path string // relative to internal/, for messages
	dir  string // relative to internal/, slash-separated
	ast  *ast.File
}

// goFiles parses the imports and declarations of every non-test Go file
// under internal/.
func goFiles(t *testing.T) []goFile {
	t.Helper()
	const root = ".." // this package lives in internal/architecture
	var files []goFile
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		parsed, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		files = append(files, goFile{path: "internal/" + rel, dir: rel[:strings.LastIndex(rel, "/")], ast: parsed})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("found no Go files under internal/")
	}
	return files
}

// split returns the area of a package path relative to internal/ (its first
// element) and, for features, the feature's name (its second).
func split(dir string) (area, pkg string) {
	parts := strings.SplitN(dir, "/", 3)
	if len(parts) > 1 {
		pkg = parts[1]
	}
	return parts[0], pkg
}
