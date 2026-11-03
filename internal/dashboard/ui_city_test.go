package dashboard

import (
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"regexp"
	"strings"
	"testing"
)

// Agent City (docs/dashboard.md section 12) is a third UI beside the admin
// one and Mate Office, under ui/city/ and served at /city/. These are its
// proofs at the Go end: it is in the binary, every module it imports is
// served as JavaScript, and the other two UIs link to it. The off-origin rule
// is TestUIMakesNoOffOriginRequest's, which walks ui/city/ too, vendored
// three.js included.

func TestCityIsServedFromTheBinary(t *testing.T) {
	f := newFixture(t)
	body := fetchText(t, f, "/city/")
	for _, want := range []string{
		`<title>Agent City</title>`,
		`<link rel="stylesheet" href="city.css">`,
		`<script type="module" src="app.js"></script>`,
		`<a href="../office/"`,
		`<a href="../"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("GET /city/ does not contain %q", want)
		}
	}
	want, err := fs.ReadFile(uiFS(), "city/index.html")
	if err != nil {
		t.Fatalf("city/index.html is not embedded: %v", err)
	}
	if body != string(want) {
		t.Error("GET /city/ is not the embedded city/index.html")
	}
}

// TestCityModulesAreServed follows the page's script and stylesheet and then
// every relative ES module import, from the URL a browser would resolve it
// against: a module served under any other content type is refused by the
// browser, so each must come back as JavaScript.
func TestCityModulesAreServed(t *testing.T) {
	f := newFixture(t)
	importRe := regexp.MustCompile(`(?m)(?:^|[;\s])(?:import|export)\s[^'"]*?from\s*['"](\.[^'"]+)['"]`)
	base, _ := url.Parse(f.http.URL + "/city/")
	queue := referencedAssets(fetchText(t, f, "/city/"))
	if len(queue) < 2 {
		t.Fatalf("city/index.html references %v; it should load city.css and app.js", queue)
	}
	seen := map[string]bool{}
	var modules []string
	for len(queue) > 0 {
		ref := queue[0]
		queue = queue[1:]
		u := base.ResolveReference(&url.URL{Path: ref})
		if seen[u.Path] {
			continue
		}
		seen[u.Path] = true
		resp, err := f.http.Client().Get(f.http.URL + u.Path)
		if err != nil {
			t.Fatalf("GET %s: %v", u.Path, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s = %d; the city names a file the binary does not hold", u.Path, resp.StatusCode)
			continue
		}
		if !strings.HasSuffix(u.Path, ".js") {
			continue
		}
		modules = append(modules, u.Path)
		if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "javascript") {
			t.Errorf("GET %s content type = %q; a browser refuses a module that is not JavaScript", u.Path, ct)
		}
		src := fetchText(t, f, u.Path)
		dir, _ := url.Parse(f.http.URL + u.Path)
		for _, m := range importRe.FindAllStringSubmatch(src, -1) {
			queue = append(queue, dir.ResolveReference(&url.URL{Path: m[1]}).Path)
		}
	}
	for _, want := range []string{"/city/app.js", "/city/city.js", "/city/room.js", "/city/history.js", "/city/chatter.js", "/city/agent-mascot.js", "/city/vendor/three.js"} {
		found := false
		for _, m := range modules {
			found = found || m == want
		}
		if !found {
			t.Errorf("following the imports from app.js never reached %s (reached %v)", want, modules)
		}
	}
}

// TestTheOtherUIsLinkToTheCity: the city is a menu entry beside Mate Office
// in the admin header, and a tab in the Office's view switch.
func TestTheOtherUIsLinkToTheCity(t *testing.T) {
	f := newFixture(t)
	if admin := fetchText(t, f, "/"); !strings.Contains(admin, `href="city/"`) {
		t.Error("the admin header has no link to Agent City (href=\"city/\")")
	}
	if office := fetchText(t, f, "/office/office.js"); !strings.Contains(office, `href: "../city/"`) {
		t.Error("the Office's view switch has no City tab (href: \"../city/\")")
	}
}

// TestCityBehaviour runs ui_city_test.cjs - the Mate's work board (CON-158):
// its tab, groups, tick with undo, topic link, and that none of it is a
// need - against app.js in node, as TestOfficeBehaviour does for the
// Office. Not skipped on a machine without node: node is not a dependency
// of this repository, so the subtest is simply not registered there.
func TestCityBehaviour(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Log("no node on PATH, so ui_city_test.cjs is not run here")
		return
	}
	t.Run("node", func(t *testing.T) {
		cmd := exec.Command(node, "--test", "--test-timeout=30000", path.Join(".", "ui_city_test.cjs"))
		// The fixture's times are shown as local clock readings; pin the
		// zone so the run does not depend on where the machine is.
		cmd.Env = append(os.Environ(), "TZ=UTC")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("ui_city_test.cjs failed:\n%s", out)
		}
		if !strings.Contains(string(out), "fail 0") {
			t.Fatalf("unexpected node output:\n%s", out)
		}
	})
}
