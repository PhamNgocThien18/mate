package platform_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

// The platform ratchet keeps the operating system calls only some systems
// have in this package (CON-184), so a port changes one package. It scans
// every non-test .go file under cmd/ and internal/, outside
// internal/platform, for these uses:
//
//   - syscall.Flock, a syscall.LOCK_ constant, syscall.O_NOFOLLOW;
//   - syscall.Kill, syscall.Getpgid;
//   - Setsid or Setpgid, as a field (syscall.SysProcAttr{Setsid: true}) or
//     a call (unix.Setpgid);
//   - runtime.GOOS;
//   - an import of golang.org/x/sys/unix or github.com/creack/pty, and a dot
//     import of syscall or runtime, whose uses the scan could not tell;
//   - "osascript" as a word of a string literal.
//
// Only a file listed below may have any. A use in any other file fails the
// test, and so does a listed file that has none left, so the lists only get
// shorter. What a file needs from the system belongs in this package; a
// file joins platformLasting only when it is an adapter bound to one system
// or one tool.

type platformEntry struct {
	file   string // module-relative, slash-separated
	reason string
}

// platformLasting are adapters bound to one operating system or one tool.
var platformLasting = []platformEntry{
	{file: "internal/host/ghostty.go", reason: "drives Ghostty through AppleScript (osascript), which only macOS has"},
	{file: "internal/matehost/input.go", reason: "reads the terminal's keys with poll(2) (golang.org/x/sys/unix)"},
	{file: "internal/runtime/herdr.go", reason: "starts the Herdr server in a session of its own (Setsid)"},
	{file: "internal/runtime/herdr_session_stream.go", reason: "runs a Herdr session stream on a pseudo-terminal (creack/pty), polls it (golang.org/x/sys/unix) and ends the stream's process group (syscall.Kill)"},
}

// platformDebt are files that choose by operating system themselves; CON-185
// moves that choice out of them and removes their entries.
var platformDebt = []platformEntry{
	{file: "cmd/mate/console_plugins.go", reason: "CON-185: picks the program that opens a URL by runtime.GOOS"},
	{file: "cmd/mate/dashboard.go", reason: "CON-185: picks the program that opens the browser by runtime.GOOS"},
	{file: "cmd/mate/notifications.go", reason: "CON-185: turns notifications off outside darwin by runtime.GOOS"},
}

const ratchetFile = "internal/platform/ratchet_test.go"

type platformUse struct {
	file string // module-relative, slash-separated
	line int
	what string
}

func (u platformUse) String() string { return fmt.Sprintf("%s:%d: %s", u.file, u.line, u.what) }

func TestOSCallsStayInPlatform(t *testing.T) {
	root := moduleRoot(t)
	allowed := map[string]platformEntry{}
	for _, e := range append(append([]platformEntry{}, platformLasting...), platformDebt...) {
		if _, twice := allowed[e.file]; twice {
			t.Errorf("%s is listed twice in %s", e.file, ratchetFile)
		}
		if strings.TrimSpace(e.reason) == "" {
			t.Errorf("%s is listed in %s without a reason", e.file, ratchetFile)
		}
		allowed[e.file] = e
	}

	byFile := map[string][]platformUse{}
	var outside []platformUse
	for _, path := range platformScanFiles(t, root) {
		for _, u := range scanPlatformUses(t, root, path) {
			if _, ok := allowed[u.file]; ok {
				byFile[u.file] = append(byFile[u.file], u)
				continue
			}
			outside = append(outside, u)
		}
	}

	files := make([]string, 0, len(byFile))
	for f := range byFile {
		files = append(files, f)
	}
	sort.Strings(files)
	var inventory strings.Builder
	for _, f := range files {
		var what []string
		for _, u := range byFile[f] {
			what = append(what, u.what)
		}
		fmt.Fprintf(&inventory, "  %-42s %s\n", f, strings.Join(what, ", "))
	}
	t.Logf("operating system calls outside internal/platform, by allowed file:\n%s", inventory.String())

	for _, e := range append(append([]platformEntry{}, platformLasting...), platformDebt...) {
		if len(byFile[e.file]) == 0 {
			t.Errorf("%s is allowed the calls (%s) but uses none of them; remove its entry from %s", e.file, e.reason, ratchetFile)
		}
	}
	if len(outside) > 0 {
		var all strings.Builder
		for _, u := range outside {
			all.WriteString("  " + u.String() + "\n")
		}
		t.Errorf("%d operating system calls outside internal/platform, in files %s does not list:\n%s"+
			"Call internal/platform instead, and add there what it lacks. Only an adapter bound to one system or one tool joins platformLasting, with its reason.",
			len(outside), ratchetFile, all.String())
	}
}

// TestPlatformScanFindsEachUse pins what the scan counts: each listed call
// under whatever name the file gives syscall or runtime, never the
// portable rest of those packages, and never a package of mate's own that
// happens to be named runtime.
func TestPlatformScanFindsEachUse(t *testing.T) {
	root := t.TempDir()
	for name, src := range map[string]string{
		"a.go": `package a

import (
	"fmt"
	"os/exec"
	goruntime "runtime"
	sys "syscall"

	ptylib "github.com/creack/pty"
	"golang.org/x/sys/unix"
)

func f(cmd *exec.Cmd, fd int) {
	_ = sys.Flock(fd, sys.LOCK_EX|sys.LOCK_NB)
	_ = sys.Kill(1, sys.SIGTERM)
	_, _ = sys.Getpgid(1)
	_ = sys.O_NOFOLLOW
	cmd.SysProcAttr = &sys.SysProcAttr{Setsid: true}
	cmd.SysProcAttr.Setpgid = true
	fmt.Println(goruntime.GOOS, goruntime.GOARCH, sys.EPERM, ptylib.Start, unix.Getpid)
	_ = exec.Command("/usr/bin/osascript", "-")
	_ = "osascripts, xosascript"
}
`,
		"b.go": `package b

import (
	. "syscall"

	"github.com/nguyenngocanh94/mate/internal/runtime"
)

var _, _ = runtime.GOOS, Kill
`,
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var got []string
	for _, name := range []string{"a.go", "b.go"} {
		for _, u := range scanPlatformUses(t, root, filepath.Join(root, name)) {
			got = append(got, u.String())
		}
	}
	want := []string{
		`a.go:9: import "github.com/creack/pty"`,
		`a.go:10: import "golang.org/x/sys/unix"`,
		"a.go:14: syscall.Flock",
		"a.go:14: syscall.LOCK_EX",
		"a.go:14: syscall.LOCK_NB",
		"a.go:15: syscall.Kill",
		"a.go:16: syscall.Getpgid",
		"a.go:17: syscall.O_NOFOLLOW",
		"a.go:18: Setsid",
		"a.go:19: Setpgid",
		"a.go:20: runtime.GOOS",
		`a.go:21: "osascript"`,
		`b.go:4: import . "syscall"`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("uses:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// moduleRoot is the directory holding go.mod, found upward from the test's
// working directory.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test directory")
		}
		dir = parent
	}
}

// platformScanFiles is every non-test .go file under cmd/ and internal/,
// outside internal/platform, skipping what the go tool skips (testdata, and
// directories starting with "." or "_") and nested modules.
func platformScanFiles(t *testing.T, root string) []string {
	t.Helper()
	self := filepath.Join(root, "internal", "platform")
	var files []string
	for _, top := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				name := d.Name()
				if path == self || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || name == "testdata" || name == "vendor" {
					return filepath.SkipDir
				}
				if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return files
}

// platformSyscalls are the syscall names counted, besides the LOCK_
// constants: the rest of package syscall (SIGTERM, EPERM, Signal) is the
// same on every system mate builds for.
var platformSyscalls = map[string]bool{"Flock": true, "Kill": true, "Getpgid": true, "O_NOFOLLOW": true}

// platformImports are the packages whose import is itself a use.
var platformImports = map[string]bool{"golang.org/x/sys/unix": true, "github.com/creack/pty": true}

func scanPlatformUses(t *testing.T, root, path string) []platformUse {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		t.Fatal(err)
	}
	rel = filepath.ToSlash(rel)
	var uses []platformUse
	add := func(pos token.Pos, what string) {
		uses = append(uses, platformUse{file: rel, line: fset.Position(pos).Line, what: what})
	}

	// std is Go's syscall or runtime package under the name this file
	// gives it.
	std := map[string]string{}
	for _, imp := range f.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		if platformImports[p] {
			add(imp.Pos(), "import "+imp.Path.Value)
			continue
		}
		if p != "syscall" && p != "runtime" {
			continue
		}
		name := p
		if imp.Name != nil {
			name = imp.Name.Name
		}
		switch name {
		case "_":
		case ".":
			add(imp.Pos(), "import . "+imp.Path.Value)
		default:
			std[name] = p
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.ImportSpec:
			return false
		case *ast.SelectorExpr:
			sel := n.Sel.Name
			if x, ok := n.X.(*ast.Ident); ok {
				switch std[x.Name] {
				case "syscall":
					if platformSyscalls[sel] || strings.HasPrefix(sel, "LOCK_") {
						add(n.Pos(), "syscall."+sel)
					}
				case "runtime":
					if sel == "GOOS" {
						add(n.Pos(), "runtime.GOOS")
					}
				}
			}
			if sel == "Setsid" || sel == "Setpgid" {
				add(n.Sel.Pos(), sel)
			}
		case *ast.KeyValueExpr:
			if k, ok := n.Key.(*ast.Ident); ok && (k.Name == "Setsid" || k.Name == "Setpgid") {
				add(k.Pos(), k.Name)
			}
		case *ast.BasicLit:
			if n.Kind != token.STRING {
				return true
			}
			if s, err := strconv.Unquote(n.Value); err == nil && hasWord(s, "osascript") {
				add(n.Pos(), `"osascript"`)
			}
		}
		return true
	})
	return uses
}

// hasWord reports whether w is a whole word of s: "/usr/bin/osascript" and
// "osascript -e" have "osascript", "osascripts" has not.
func hasWord(s, w string) bool {
	for _, word := range strings.FieldsFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-'
	}) {
		if word == w {
			return true
		}
	}
	return false
}
