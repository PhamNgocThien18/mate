package core

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const module = "github.com/nguyenngocanh94/mate/"

// forbidden says whether a package of this module is one the engine may not
// reach: a command, or a UI. The engine is what `mate core` runs with no
// console open (CON-211); a UI is a client of it, never a part.
func forbidden(rel string) bool {
	for _, prefix := range []string{"cmd", "internal/ui"} {
		if rel == prefix || strings.HasPrefix(rel, prefix+"/") {
			return true
		}
	}
	return false
}

// TestCoreReachesNeitherACommandNorAUI walks this package's imports through
// every package of the module they lead to, the way the linker would, and
// fails on the first command or UI package on the way. Test files are left
// out: a test may drive a UI, the engine may not link one.
func TestCoreReachesNeitherACommandNorAUI(t *testing.T) {
	root := filepath.Join("..", "..")
	via := map[string]string{"internal/core": ""}
	queue := []string{"internal/core"}
	for len(queue) > 0 {
		pkg := queue[0]
		queue = queue[1:]
		for _, imp := range packageImports(t, filepath.Join(root, filepath.FromSlash(pkg))) {
			rel, ok := strings.CutPrefix(imp, module)
			if !ok {
				continue
			}
			if forbidden(rel) {
				t.Errorf("internal/core reaches %s: %s", imp, chain(via, pkg)+" -> "+rel)
				continue
			}
			if _, seen := via[rel]; !seen {
				via[rel] = pkg
				queue = append(queue, rel)
			}
		}
	}
	if len(via) < 2 {
		t.Fatalf("walked %d packages; the import walk found nothing to check", len(via))
	}
}

// packageImports is every import of the package in dir, over all of its
// non-test files whatever their build constraints: a file built only on
// another system links on that system.
func packageImports(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var out []string
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", filepath.Join(dir, name), err)
		}
		for _, imp := range file.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatalf("%s: unquote import %s: %v", name, imp.Path.Value, err)
			}
			out = append(out, path)
		}
	}
	return out
}

// chain is the import path from internal/core to pkg, for the failure
// message: which package let the forbidden one in.
func chain(via map[string]string, pkg string) string {
	path := []string{pkg}
	for from := via[pkg]; from != ""; from = via[from] {
		path = append([]string{from}, path...)
	}
	return strings.Join(path, " -> ")
}

// The walk itself must fail when the forbidden package is there, or a green
// run proves nothing.
func TestForbiddenNamesCommandsAndUIs(t *testing.T) {
	for rel, want := range map[string]bool{
		"cmd/mate":             true,
		"cmd":                  true,
		"internal/ui":          true,
		"internal/ui/console":  true,
		"internal/uikit":       false,
		"internal/watch":       false,
		"internal/core":        false,
		"internal/commandline": false,
	} {
		if got := forbidden(rel); got != want {
			t.Errorf("forbidden(%q) = %v, want %v", rel, got, want)
		}
	}
	via := map[string]string{"internal/core": "", "internal/watch": "internal/core"}
	if got := chain(via, "internal/watch"); got != "internal/core -> internal/watch" {
		t.Errorf("chain = %q", got)
	}
}
