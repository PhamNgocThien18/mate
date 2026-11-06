package herdr

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// herdrImporters are the packages whose non-test files may import this
// package. Everything else reaches Herdr through the runtime.Adapter and
// runtime.ReadAdapter ports: only the code that builds the adapter knows
// which runtime it is (CON-190). Test files are outside the rule; live tests
// build a Herdr adapter of their own.
var herdrImporters = map[string]string{
	"internal/spawn": "LiveDeps builds the live adapter (deps.go)",
}

// TestOnlySpawnImportsHerdr scans every non-test .go file under cmd/,
// internal/ and assets/ and fails on an import of internal/runtime/herdr
// (or a package under it) from a package not in herdrImporters.
func TestOnlySpawnImportsHerdr(t *testing.T) {
	const module = "github.com/nguyenngocanh94/mate/"
	const self = "internal/runtime/herdr"
	root := filepath.Join("..", "..", "..")
	fset := token.NewFileSet()
	files := 0
	seen := map[string]bool{}
	for _, top := range []string{"assets", "cmd", "internal"} {
		dir := filepath.Join(root, top)
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			name := d.Name()
			if d.IsDir() {
				if name == "testdata" || (path != dir && (strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_"))) {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			pkg := rel[:strings.LastIndex(rel, "/")]
			file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			files++
			for _, imp := range file.Imports {
				p, err := strconv.Unquote(imp.Path.Value)
				if err != nil {
					return err
				}
				if p != module+self && !strings.HasPrefix(p, module+self+"/") {
					continue
				}
				if pkg == self || strings.HasPrefix(pkg, self+"/") {
					continue
				}
				if _, ok := herdrImporters[pkg]; ok {
					seen[pkg] = true
					continue
				}
				t.Errorf("%s:%d imports %s; outside %s only %v may, and the rest of mate reaches Herdr through runtime.Adapter", rel, fset.Position(imp.Pos()).Line, p, self, keys(herdrImporters))
			}
			return nil
		})
		if err != nil {
			t.Fatalf("scan %s: %v", top, err)
		}
	}
	if files == 0 {
		t.Fatal("no Go files were checked; the boundary test is vacuous")
	}
	for pkg, why := range herdrImporters {
		if !seen[pkg] {
			t.Errorf("herdrImporters allows %s (%s), but none of its non-test files imports %s; remove the entry", pkg, why, self)
		}
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
