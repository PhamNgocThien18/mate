// Package archtest holds the repo-wide import rule of docs/architecture.md
// section 1: every package of cmd/, internal/ and assets/ sits in one of
// seven layers, and a package imports only packages of its own layer or a
// lower one. The directory has test files only; nothing links it.
package archtest

import (
	"bufio"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const module = "github.com/nguyenngocanh94/mate/"

// root is the repository root, seen from this package's directory.
var root = filepath.Join("..", "..")

// scanned are the top-level directories whose packages the rule covers.
// scripts/ is outside the rule (docs/architecture.md, import rule 3).
var scanned = []string{"assets", "cmd", "internal"}

type layer int

const (
	leaf layer = iota + 1
	storage
	read
	integration
	service
	ui
	binary
)

// layerNames are the names docs/architecture.md section 1 gives the layers,
// lowest first; its table rows start with "<number> <name>".
var layerNames = map[layer]string{
	leaf:        "leaf",
	storage:     "storage",
	read:        "read",
	integration: "integration",
	service:     "service",
	ui:          "UI",
	binary:      "binary",
}

// layerIdents are the constants' names, for the line a message tells you
// to add to layers.
var layerIdents = map[layer]string{
	leaf: "leaf", storage: "storage", read: "read", integration: "integration",
	service: "service", ui: "ui", binary: "binary",
}

func (l layer) String() string { return fmt.Sprintf("%d %s", int(l), layerNames[l]) }

// docPath is how docs/architecture.md section 1 writes a package: under
// internal/ without the prefix, assets and cmd/ in full.
func docPath(pkg string) string { return strings.TrimPrefix(pkg, "internal/") }

// layers places every package that has a non-test .go file under cmd/,
// internal/ or assets/. It is the Packages column of the table in
// docs/architecture.md section 1, written as data, and
// TestLayerTableMatchesArchitectureDoc keeps the two equal: a package added
// here goes into that column in the same change.
var layers = map[string]layer{
	"assets":                   leaf,
	"internal/brief":           leaf,
	"internal/capability":      leaf,
	"internal/config":          leaf,
	"internal/consolecmd":      leaf,
	"internal/crewstate":       leaf,
	"internal/memory":          leaf,
	"internal/names":           leaf,
	"internal/observability":   leaf,
	"internal/platform":        leaf,
	"internal/process":         leaf,
	"internal/telemetry":       leaf,
	"internal/store":           storage,
	"internal/db":              storage,
	"internal/plugin":          storage,
	"internal/learnings":       storage,
	"internal/query":           read,
	"internal/coreapi":         read,
	"internal/box":             read,
	"internal/timeline/scene":  read,
	"internal/timeline/read":   read,
	"internal/diagnostics":     read,
	"internal/dispatch":        read,
	"internal/harness":         read,
	"internal/runtime":         read,
	"internal/harness/catalog": integration,
	"internal/harness/claude":  integration,
	"internal/harness/codex":   integration,
	"internal/harness/grok":    integration,
	"internal/harness/pi":      integration,
	"internal/screen":          integration,
	"internal/screen/fixture":  integration,
	"internal/screen/jev":      integration,
	"internal/screen/chain":    integration,
	"internal/send":            integration,
	"internal/notice":          integration,
	"internal/host":            integration,
	"internal/panerun":         integration,
	"internal/gitx":            integration,
	"internal/github":          integration,
	"internal/facts":           integration,
	"internal/quota":           integration,
	"internal/teardown":        integration,
	"internal/notifications":   integration,
	"internal/matehost":        integration,
	"internal/spawn":           service,
	"internal/outbox":          service,
	"internal/autopilot":       service,
	"internal/watch":           service,
	"internal/timeline":        service,
	"internal/recovery":        service,
	"internal/refresh":         service,
	"internal/prwatch":         service,
	"internal/migrate":         service,
	"internal/mateassets":      service,
	"internal/hook":            service,
	"internal/wake":            service,
	"internal/recall":          service,
	"internal/dashboard":       service,
	"internal/usage":           service,
	"internal/core":            service,
	"internal/ui/console":      ui,
	"cmd/mate":                 binary,
}

// testOnly are packages outside the rule: helpers that only _test.go files
// import (docs/architecture.md, import rule 3). TestImportsFollowLayers
// fails if a non-test file imports one, since that would link test code
// into mate.
var testOnly = map[string]string{
	"internal/brief/brieftest":        "brief fixtures for tests; only _test.go files import it",
	"internal/harness/harnesstest":    "harness test kit and TestLive* helpers; only _test.go files import it",
	"internal/harness/codex/codexlab": "Codex lab for the codex package's tests; only _test.go files import it",
}

// violationsFile lists the imports that break the rule today; see its
// header for the format.
const violationsFile = "violations.txt"

// debtCeiling is the number of debt lines in violations.txt. Debt only goes
// down: a fix deletes its line and lowers this number in the same change,
// and the test fails if the file holds more debt or less.
const debtCeiling = 2

// importSite is one import of a mate package in a non-test file.
type importSite struct {
	file string // relative to the repository root, slash-separated
	line int
}

// pkgImports is a package's imports of other mate packages, each with the
// files that make it, over all of its non-test files whatever their build
// constraints: a file built only on another system links on that system.
type pkgImports map[string][]importSite

// scanRepo parses every non-test .go file under the scanned directories,
// imports only, and groups the mate imports by package directory. It
// skips what the go tool skips: testdata, and directories starting with
// "." or "_".
func scanRepo(t *testing.T) map[string]pkgImports {
	t.Helper()
	fset := token.NewFileSet()
	pkgs := map[string]pkgImports{}
	for _, top := range scanned {
		err := filepath.WalkDir(filepath.Join(root, top), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			name := d.Name()
			if d.IsDir() {
				if name == "testdata" || (path != filepath.Join(root, top) && (strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_"))) {
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
			pkg := pathDir(rel)
			if pkgs[pkg] == nil {
				pkgs[pkg] = pkgImports{}
			}
			file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
			if err != nil {
				return fmt.Errorf("parse %s: %w", rel, err)
			}
			for _, imp := range file.Imports {
				p, err := strconv.Unquote(imp.Path.Value)
				if err != nil {
					return fmt.Errorf("%s: unquote import %s: %w", rel, imp.Path.Value, err)
				}
				dep, ok := strings.CutPrefix(p, module)
				if !ok {
					continue
				}
				pkgs[pkg][dep] = append(pkgs[pkg][dep], importSite{rel, fset.Position(imp.Pos()).Line})
			}
			return nil
		})
		if err != nil {
			t.Fatalf("scan %s: %v", top, err)
		}
	}
	if len(pkgs) < len(layers)/2 {
		t.Fatalf("found %d packages under %v; the scan is not reading the repository", len(pkgs), scanned)
	}
	return pkgs
}

func pathDir(rel string) string {
	i := strings.LastIndex(rel, "/")
	if i < 0 {
		return "."
	}
	return rel[:i]
}

// violationKind says whether a listed violation stays (exception: a design
// reason, written on the line) or must go (debt).
type violationKind string

const (
	exception violationKind = "exception"
	debt      violationKind = "debt"
)

type listedViolation struct {
	kind   violationKind
	note   string
	lineNo int
}

type importPair struct{ from, to string }

func (p importPair) String() string { return p.from + " -> " + p.to }

var violationLine = regexp.MustCompile(`^(exception|debt)\s+([^\s#]+)\s+->\s+([^\s#]+)\s*(?:#\s*(.*))?$`)

// loadViolations reads violations.txt. Each line that is not blank or a
// comment is one import pair:
//
//	exception <importer> -> <imported>  # reason (required)
//	debt      <importer> -> <imported>  # note (optional)
func loadViolations(t *testing.T) map[importPair]listedViolation {
	t.Helper()
	f, err := os.Open(violationsFile)
	if err != nil {
		t.Fatalf("open %s: %v", violationsFile, err)
	}
	defer f.Close()
	listed := map[importPair]listedViolation{}
	sc := bufio.NewScanner(f)
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		m := violationLine.FindStringSubmatch(line)
		if m == nil {
			t.Errorf("%s:%d: %q is not `exception|debt <importer> -> <imported>  # note`", violationsFile, n, line)
			continue
		}
		v := listedViolation{kind: violationKind(m[1]), note: strings.TrimSpace(m[4]), lineNo: n}
		pair := importPair{m[2], m[3]}
		if v.kind == exception && v.note == "" {
			t.Errorf("%s:%d: exception %s has no reason; an exception keeps a design reason on its line, anything else is debt", violationsFile, n, pair)
		}
		if prev, dup := listed[pair]; dup {
			t.Errorf("%s:%d: %s is already listed on line %d", violationsFile, n, pair, prev.lineNo)
			continue
		}
		listed[pair] = v
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read %s: %v", violationsFile, err)
	}
	return listed
}

// TestImportsFollowLayers is docs/architecture.md import rule 1: a package
// imports only packages of its own layer or a lower one. It fails on a
// package with no layer, on an import that breaks the rule and is not in
// violations.txt, and on a line of violations.txt that no longer happens,
// so the list only gets shorter.
func TestImportsFollowLayers(t *testing.T) {
	pkgs := scanRepo(t)
	listed := loadViolations(t)

	debts := 0
	for _, v := range listed {
		if v.kind == debt {
			debts++
		}
	}
	switch {
	case debts > debtCeiling:
		t.Errorf("%s has %d debt lines, above debtCeiling %d: debt only goes down. Fix the new import, or argue it as an exception with its design reason", violationsFile, debts, debtCeiling)
	case debts < debtCeiling:
		t.Errorf("%s has %d debt lines, below debtCeiling %d: lower debtCeiling in layers_test.go to %d", violationsFile, debts, debtCeiling, debts)
	}

	importers := map[string][]string{} // dep -> packages importing it
	for _, pkg := range sortedKeys(pkgs) {
		for dep := range pkgs[pkg] {
			importers[dep] = append(importers[dep], pkg)
		}
	}

	// Every package with a non-test file has a layer, or is test-only.
	for _, pkg := range sortedKeys(pkgs) {
		_, placed := layers[pkg]
		_, helper := testOnly[pkg]
		switch {
		case placed && helper:
			t.Errorf("%s is both in layers and in testOnly; keep it in one", pkg)
		case !placed && !helper:
			t.Errorf("%s", unplacedMessage(pkg, pkgs, importers))
		}
	}
	for _, pkg := range sortedKeys(layers) {
		if _, ok := pkgs[pkg]; !ok {
			t.Errorf("layers places %s, but it has no non-test .go file under %v; remove it from layers in internal/archtest/layers_test.go and from docs/architecture.md section 1", pkg, scanned)
		}
	}
	for _, pkg := range sortedKeys(testOnly) {
		if _, ok := pkgs[pkg]; !ok {
			t.Errorf("testOnly lists %s, but it has no non-test .go file; remove it from testOnly", pkg)
		}
	}

	// Every import of a mate package keeps to the layers.
	seen := map[importPair]bool{}
	byLayers := map[[2]layer][]string{}
	for _, pkg := range sortedKeys(pkgs) {
		from, ok := layers[pkg]
		if !ok {
			continue // reported above; a test-only package may import anything
		}
		for _, dep := range sortedKeys(pkgs[pkg]) {
			sites := pkgs[pkg][dep]
			if _, helper := testOnly[dep]; helper {
				t.Errorf("%s imports %s, a test-only package, in a non-test file (%s); only _test.go files may import it", pkg, dep, siteList(sites))
				continue
			}
			to, ok := layers[dep]
			if !ok {
				if _, scannedPkg := pkgs[dep]; !scannedPkg {
					t.Errorf("%s imports %s (%s), which is not a package under %v; code under the rule imports only packages of the layer table", pkg, dep, siteList(sites), scanned)
				}
				continue // a scanned package with no layer is reported above
			}
			if to <= from {
				continue
			}
			pair := importPair{pkg, dep}
			seen[pair] = true
			byLayers[[2]layer{from, to}] = append(byLayers[[2]layer{from, to}], pair.String())
			if _, ok := listed[pair]; ok {
				continue
			}
			t.Errorf("layer rule broken: %s (layer %s) imports %s (layer %s) at %s.\n"+
				"Rule (docs/architecture.md section 1, import rule 1): a package imports only packages of its own layer or a lower one.\n"+
				"Fix the import, move a type to a lower layer, or, if a layer is wrong, change it in layers_test.go and the doc together. "+
				"Only an import that must stay goes into internal/archtest/%s, as `exception %s  # reason`.",
				pkg, from, dep, to, siteList(sites), violationsFile, pair)
		}
	}

	for _, pair := range sortedPairs(listed) {
		if seen[pair] {
			continue
		}
		v := listed[pair]
		why := "no non-test file of " + pair.from + " imports " + pair.to + " any more"
		if _, ok := pkgs[pair.from][pair.to]; ok {
			why = "the import is within the rule now"
			if _, placed := layers[pair.from]; !placed {
				why = pair.from + " has no layer (reported above)"
			} else if _, placed := layers[pair.to]; !placed {
				why = pair.to + " has no layer (reported above)"
			}
		}
		t.Errorf("%s:%d: %s %s is listed but %s; delete the line, the list only gets shorter", violationsFile, v.lineNo, v.kind, pair, why)
	}

	if testing.Verbose() {
		for _, k := range sortedLayerPairs(byLayers) {
			t.Logf("%s -> %s: %s", k[0], k[1], strings.Join(byLayers[k], ", "))
		}
	}
}

// unplacedMessage says what to do about a package with no layer, and
// narrows the choice: its imports set the lowest layer it can take without
// a new violation, its importers the highest.
func unplacedMessage(pkg string, pkgs map[string]pkgImports, importers map[string][]string) string {
	lowest, lowBy := leaf, ""
	for _, dep := range sortedKeys(pkgs[pkg]) {
		if l, ok := layers[dep]; ok && l > lowest {
			lowest, lowBy = l, dep
		}
	}
	highest, highBy := binary, ""
	for _, imp := range importers[pkg] {
		if l, ok := layers[imp]; ok && l < highest {
			highest, highBy = l, imp
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s has non-test .go files but no layer.\n", pkg)
	if lowBy != "" {
		fmt.Fprintf(&b, "It imports %s, so its layer is at least %s.\n", lowBy, lowest)
	}
	if highBy != "" {
		fmt.Fprintf(&b, "%s imports it, so its layer is at most %s.\n", highBy, highest)
	}
	pick, row := "<layer>", "<n layer>"
	switch {
	case lowest > highest:
		b.WriteString("No layer satisfies both: one of those imports has to change.\n")
	case lowest == highest:
		fmt.Fprintf(&b, "Its one possible layer is %s.\n", lowest)
		pick, row = layerIdents[lowest], lowest.String()
	default:
		fmt.Fprintf(&b, "Its layer is one of %s to %s (import rule 2 of the doc says where a port or an adapter goes); the lowest is shown below.\n", lowest, highest)
		pick, row = layerIdents[lowest], lowest.String()
	}
	b.WriteString("Place it in both places, in one change:\n")
	fmt.Fprintf(&b, "  internal/archtest/layers_test.go, map layers:  %q: %s,\n", pkg, pick)
	fmt.Fprintf(&b, "  docs/architecture.md section 1, Packages column of row %q:  `%s`\n", row, docPath(pkg))
	b.WriteString("Layers: ")
	for l := leaf; l <= binary; l++ {
		if l > leaf {
			b.WriteString(", ")
		}
		b.WriteString(l.String())
	}
	b.WriteString(".\n")
	if len(importers[pkg]) == 0 {
		b.WriteString("If only _test.go files import it, add it to testOnly instead, with the reason, and to import rule 3 of the doc.\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func siteList(sites []importSite) string {
	parts := make([]string, len(sites))
	for i, s := range sites {
		parts[i] = fmt.Sprintf("%s:%d", s.file, s.line)
	}
	return strings.Join(parts, ", ")
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedPairs(m map[importPair]listedViolation) []importPair {
	pairs := make([]importPair, 0, len(m))
	for p := range m {
		pairs = append(pairs, p)
	}
	slices.SortFunc(pairs, func(a, b importPair) int { return strings.Compare(a.String(), b.String()) })
	return pairs
}

func sortedLayerPairs(m map[[2]layer][]string) [][2]layer {
	keys := make([][2]layer, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b [2]layer) int {
		if a[0] != b[0] {
			return int(a[0] - b[0])
		}
		return int(a[1] - b[1])
	})
	return keys
}

// TestLayerTableMatchesArchitectureDoc keeps the doc and the code one
// source: the Packages column of docs/architecture.md section 1 names, row
// by row, exactly the packages layers places in that layer. Paths in the
// doc are under internal/ except assets and cmd/.
func TestLayerTableMatchesArchitectureDoc(t *testing.T) {
	doc := filepath.Join(root, "docs", "architecture.md")
	data, err := os.ReadFile(doc)
	if err != nil {
		t.Fatalf("read %s: %v", doc, err)
	}
	_, section, ok := strings.Cut(string(data), "\n## 1. Packages by layer")
	if !ok {
		t.Fatalf("docs/architecture.md has no section \"## 1. Packages by layer\"")
	}
	section, _, _ = strings.Cut(section, "\n## ")
	row := regexp.MustCompile(`^\|\s*(\d)\s+(\S+)[^|]*\|[^|]*\|([^|]*)\|\s*$`)
	ticked := regexp.MustCompile("`([^`]+)`")
	docLayer := map[string]layer{}
	rows := 0
	for _, line := range strings.Split(section, "\n") {
		m := row.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		rows++
		n, _ := strconv.Atoi(m[1])
		l := layer(n)
		if name, ok := layerNames[l]; !ok || name != m[2] {
			t.Errorf("docs/architecture.md row %q names layer %s %s; the code has %s", strings.TrimSpace(line), m[1], m[2], l)
			continue
		}
		for _, p := range ticked.FindAllStringSubmatch(m[3], -1) {
			pkg := p[1]
			if pkg != "assets" && !strings.HasPrefix(pkg, "cmd/") {
				pkg = "internal/" + pkg
			}
			if prev, dup := docLayer[pkg]; dup {
				t.Errorf("docs/architecture.md section 1 lists %s in layer %s and again in %s", pkg, prev, l)
			}
			docLayer[pkg] = l
		}
	}
	if rows != len(layerNames) {
		t.Fatalf("docs/architecture.md section 1 has %d layer rows, want %d (one per layer)", rows, len(layerNames))
	}
	for _, pkg := range sortedKeys(layers) {
		got, ok := docLayer[pkg]
		switch {
		case !ok:
			t.Errorf("%s is in layer %s in layers_test.go but missing from the Packages column of docs/architecture.md section 1", pkg, layers[pkg])
		case got != layers[pkg]:
			t.Errorf("%s is in layer %s in layers_test.go but in layer %s in docs/architecture.md section 1; make them agree", pkg, layers[pkg], got)
		}
	}
	for _, pkg := range sortedKeys(docLayer) {
		if _, ok := layers[pkg]; !ok {
			t.Errorf("docs/architecture.md section 1 places %s in layer %s, but layers in layers_test.go does not", pkg, docLayer[pkg])
		}
	}
	for _, pkg := range sortedKeys(testOnly) {
		if !strings.Contains(section, "`"+docPath(pkg)+"`") {
			t.Errorf("testOnly lists %s, but docs/architecture.md section 1 (import rule 3) does not name `%s`", pkg, docPath(pkg))
		}
	}
}
