package coreapi

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

// queryImport is the one package outside the standard library the contract
// may import: the read types a LoadFunc returns and a request names a
// harness with.
const queryImport = "github.com/nguyenngocanh94/mate/internal/query"

// The contract is types, constants and errors that the core and every UI
// import without importing one another, so it imports the standard library
// and internal/query and nothing else. A path whose first element has no
// dot is the standard library's.
func TestCoreAPIImportsOnlyTheStandardLibraryAndQuery(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	checked := 0
	fset := token.NewFileSet()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(".", e.Name()), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", e.Name(), err)
		}
		checked++
		for _, imp := range file.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatalf("%s: unquote import %s: %v", e.Name(), imp.Path.Value, err)
			}
			first, _, _ := strings.Cut(path, "/")
			if path != queryImport && strings.Contains(first, ".") {
				t.Errorf("%s imports %s; internal/coreapi imports only the standard library and %s", e.Name(), path, queryImport)
			}
		}
	}
	if checked == 0 {
		t.Fatalf("no Go files were checked; the boundary test is vacuous")
	}
}

// A request crosses the core's socket as JSON (CON-212), every field under
// its name in snake_case - a field added later too.
func TestActionRequestFieldsAreSnakeCaseInJSON(t *testing.T) {
	typ := reflect.TypeOf(ActionRequest{})
	for i := range typ.NumField() {
		f := typ.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if want := snakeCase(f.Name); name != want {
			t.Errorf("ActionRequest.%s is %q in JSON, want %q", f.Name, name, want)
		}
	}
}

// snakeCase is a Go field name in snake_case: TargetKind is target_kind.
func snakeCase(name string) string {
	var b strings.Builder
	for i, r := range name {
		if unicode.IsUpper(r) {
			if i > 0 {
				b.WriteByte('_')
			}
			r = unicode.ToLower(r)
		}
		b.WriteRune(r)
	}
	return b.String()
}
