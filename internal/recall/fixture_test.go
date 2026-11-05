package recall

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"text/tabwriter"
	"time"

	"github.com/nguyenngocanh94/mate/internal/facts"
	"github.com/nguyenngocanh94/mate/internal/gitx"
	"github.com/nguyenngocanh94/mate/internal/matehost"
	"github.com/nguyenngocanh94/mate/internal/memory"
	"github.com/nguyenngocanh94/mate/internal/process"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// fixture is cmd/mate's recallFixture (over its backlogFixtureWorkspace),
// which these tests ran on before the digest moved here: one project "shop"
// with six crews, one per state - k1 spawned, k2 working, k3
// needs-decision, k4 wait-mate, k5 finished (closed), and k6 which said
// `working:` but reads blocked by an open stale incident - plus every file
// recall frames: a PROJECT.md with one stale-anchored repo-state line, a
// backlog.md whose In flight names a crew that is not open and leaves out
// one that is, a memory.md with a stale lesson, WORKSPACE.md, and the
// project's CREW.md with a rule. The repository has two commits, so the
// anchor at the first one is behind main. It returns the first commit.
func fixture(t *testing.T) (*store.Workspace, time.Time, string) {
	t.Helper()
	w, err := store.Init(t.TempDir(), store.Defaults{})
	if err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	repo := filepath.Join(w.ProjectHome("shop"), "shop")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, repo)
	if err := w.AddProject("shop", store.ProjectConfig{Repos: []store.RepoConfig{{Path: repo, DefaultBranch: "main"}}}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}

	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	started := func(ago time.Duration) string { return now.Add(-ago).Format(time.RFC3339) }
	crews := []struct {
		id     string
		meta   map[string]string
		status []string
	}{
		{"k1", map[string]string{"harness": "codex", "branch": "mate/k1", "task": "Add health endpoint", "started_at": started(72 * time.Hour)}, nil},
		{"k2", map[string]string{"harness": "codex", "branch": "mate/k2", "task": "Fix flaky test", "started_at": started(2 * time.Hour)}, []string{"working: running the suite"}},
		{"k3", map[string]string{"harness": "codex", "branch": "mate/k3", "task": "Pick a colour", "started_at": started(4 * time.Minute)}, []string{"needs-decision: choose red or blue for the button"}},
		{"k4", map[string]string{"harness": "codex", "branch": "mate/k4", "task": "Ship the button", "started_at": started(30 * time.Minute)}, []string{"wait-mate: ready in branch mate/k4"}},
		{"k5", map[string]string{"harness": "codex", "branch": "mate/k5", "task": "Old ship", "started_at": started(120 * time.Hour),
			"state": "finished", "teardown": "clean", "stopped_at": started(1 * time.Hour)}, nil},
		{"k6", map[string]string{"harness": "codex", "branch": "mate/k6", "task": "Investigate a crash", "started_at": started(10 * time.Minute)}, []string{"working: reading logs"}},
	}
	for _, c := range crews {
		if err := w.WriteCrewMeta("shop", c.id, c.meta); err != nil {
			t.Fatalf("WriteCrewMeta %s: %v", c.id, err)
		}
		for _, line := range c.status {
			if err := w.AppendStatus("shop", c.id, line); err != nil {
				t.Fatalf("AppendStatus %s: %v", c.id, err)
			}
		}
	}
	if err := w.AppendIncident("shop", store.IncidentEntry{
		Time: now.Add(-15 * time.Minute), Crew: "k6", Kind: "stale",
		State: store.IncidentOpen, Text: "no status for 15m",
	}); err != nil {
		t.Fatalf("AppendIncident: %v", err)
	}

	first := strings.TrimSpace(gitOutput(t, repo, "rev-parse", "--short", "HEAD"))
	writeFixture(t, filepath.Join(repo, "README.md"), "# shop\n")
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "commit", "-m", "readme")
	if err := os.MkdirAll(w.MateDir("shop"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The captain holds this project in manual, so the hand-backs above are
	// the captain's inbox items the recall lists.
	if err := w.SetMode("shop", false); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, w.ProjectDoc("shop"), "# shop\n\n## What this project is\n- A landing page for the ESP32 kit. (captain, 2026-09-18)\n\n## Layout and state\n- main has one README and no build. (crews/k1/report.md §Durable facts, main@"+first+", 2026-09-19)\n\n## How to work here\n\n## Research on file\n\n## Captain's decisions\n")
	writeFixture(t, w.BacklogFile("shop"), "# Backlog\n\n## In flight\n- k2 (ship, mate/k2): fix the flaky test\n- k3: pick a colour\n- k4 ship the button\n- k6 (scout): crash\n- buyesp32 (ship): stopped at needs-decision\n\n## Held for the captain\n- k3, asked 2026-09-19: \"red or blue?\" Waiting on: the captain.\n\n## Queued\n- none\n\n## Done (10 most recent)\n- k5 (ship, 2026-09-18): merged\n- rpi35 (scout, 2026-09-17): crews/rpi35/report.md\n")
	writeFixture(t, w.MemoryFile("shop"), "# Memory\n"+memory.HeaderPointer+"\n\n## Captain\n- Speaks Vietnamese. (captain, 2026-09-17)\n\n## Lessons\n- Scouts commit their reports unless told not to. (sent.log to rpi35 2026-09-18) <!--a:2026-08-01-->\n- The canary lesson is HERON-7. (captain, 2026-09-19) <!--a:2026-09-19-->\n")
	writeFixture(t, w.WorkspaceDoc(), "# Workspace\n- Never merge on a Friday.\n")
	writeFixture(t, w.ProjectCrewDoc("shop"), "<!-- rules for every crew of shop -->\n- Run the tests before wait-mate.\n")
	return w, now, first
}

// noRepoFixture is a workspace whose project shop has no repo yet.
func noRepoFixture(t *testing.T) *store.Workspace {
	t.Helper()
	w, err := store.Init(t.TempDir(), store.Defaults{})
	if err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	if err := w.AddProject("shop", store.ProjectConfig{}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}
	return w
}

// twoRepoFixture is project shop with repo shop (main, one commit) and a
// second repo api whose default branch develop has no commit yet.
func twoRepoFixture(t *testing.T) *store.Workspace {
	t.Helper()
	w, err := store.Init(t.TempDir(), store.Defaults{})
	if err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	shop := filepath.Join(w.ProjectHome("shop"), "shop")
	if err := os.MkdirAll(shop, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, shop)
	if err := w.AddProject("shop", store.ProjectConfig{Repos: []store.RepoConfig{{Path: shop, DefaultBranch: "main"}}}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}
	api := filepath.Join(w.ProjectHome("shop"), "api")
	runGit(t, w.Root(), "init", "-q", "-b", "develop", api)
	if _, err := w.AddRepo("shop", store.RepoConfig{Name: "api", Path: api, DefaultBranch: "develop"}); err != nil {
		t.Fatalf("AddRepo: %v", err)
	}
	return w
}

// trackerPlugin installs a tracker plugin instance in the workspace's
// plugins.yaml (CON-77): a package directory holding read.sh, which answers
// for HVN-12 and knows no other id. With read false the block has no read
// command.
func trackerPlugin(t *testing.T, w *store.Workspace, read bool) {
	t.Helper()
	pkg := t.TempDir()
	writeFixture(t, filepath.Join(pkg, "mate-plugin.yaml"), "apiVersion: mate.plugin/v1\nname: Tracker\n")
	script := "#!/bin/sh\ncase \"$1\" in\n  HVN-12) echo \"HVN-12 In Progress: fix the flaky test\"; echo \"second line\";;\n  *) echo \"not found: $1\" >&2; exit 1;;\nesac\n"
	writeFixture(t, filepath.Join(pkg, "read.sh"), script)
	if err := os.Chmod(filepath.Join(pkg, "read.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	block := "    tracker:\n      read: [./read.sh]\n"
	if !read {
		block = "    tracker: {}\n"
	}
	writeFixture(t, filepath.Join(w.Root(), ".mate", "plugins.yaml"),
		"apiVersion: mate.plugins/v1\ninstances:\n  tracker-work:\n    path: "+pkg+"\n"+block)
}

// deps stands in for cmd/mate's recallDeps. Each command the digest quotes
// prints a short version of its own output over the same workspace and git:
// the crews table's ID and STATE columns and its footer, the facts of
// internal/facts, an anchor warning for each anchor behind its branch,
// memory.Check's problems. cmd/mate's TestRecallQuotesTheRealCommands
// checks the real commands are what the binary hands in.
func deps() Deps {
	return Deps{
		Git:     gitx.New(),
		Tracker: process.ExecRunner{},
		Crews: func(w *store.Workspace, project string, now time.Time, out io.Writer) error {
			summaries, err := spawn.ListCrews(w, project)
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tSTATE")
			closed := 0
			for _, s := range summaries {
				if s.Closed {
					closed++
					continue
				}
				fmt.Fprintf(tw, "%s\t%s\n", s.Crew, s.State)
			}
			if err := tw.Flush(); err != nil {
				return err
			}
			fmt.Fprintf(out, "%d open, %d closed\n", len(summaries)-closed, closed)
			return nil
		},
		NextWork: func(cfg store.ProjectConfig) string {
			return "next work: " + cfg.NextWorkMode() + " - when nothing the captain asked for is left"
		},
		WorkLine: func(x matehost.Work) string {
			status := x.Status
			if x.Status == matehost.WorkWaiting && x.Topic > 0 {
				status = fmt.Sprintf("waiting on news #%d", x.Topic)
			}
			s := fmt.Sprintf("#%d %s %s\n", x.ID, status, x.Title)
			if x.Last != "" {
				s += "    " + x.Last + "\n"
			}
			return s
		},
		Facts: func(ctx context.Context, w *store.Workspace, git gitx.Git, project string, cfg store.ProjectConfig) []string {
			if len(cfg.Repos) == 0 {
				return facts.NoRepoLines(project)
			}
			var lines []string
			for i, r := range cfg.Repos {
				if i > 0 {
					lines = append(lines, "")
				}
				f, err := facts.Gather(ctx, git, project, r.Name, w.RepoDir(r.Path), r.DefaultBranch)
				if err != nil {
					lines = append(lines, fmt.Sprintf("%s: repo %s: project facts failed: %v", project, r.Name, err))
					continue
				}
				lines = append(lines, f.Lines()...)
			}
			return lines
		},
		AnchorWarnings: func(ctx context.Context, git gitx.Git, repo string, r store.RepoConfig, anchors []memory.Anchor) ([]string, error) {
			var out []string
			for _, a := range anchors {
				if a.SHA == memory.NoCommit {
					continue
				}
				n, err := git.CommitCount(ctx, repo, a.SHA+"..refs/heads/"+r.DefaultBranch)
				if err != nil {
					return nil, err
				}
				if n > 0 {
					out = append(out, fmt.Sprintf("warning: %s line %d is anchored at %s:%s@%s, and %s:%s has %d newer commit(s)",
						memory.ProjectFileName, a.Line, r.Name, r.DefaultBranch, a.SHA, r.Name, r.DefaultBranch, n))
				}
			}
			return out, nil
		},
		MemoryCheck: func(ctx context.Context, w *store.Workspace, git gitx.Git, project string, now time.Time) (MemoryReport, error) {
			cfg, err := w.LoadProject(project)
			if err != nil {
				return MemoryReport{}, err
			}
			text, _ := readOptional(w.MemoryFile(project))
			doc, _ := readOptional(w.ProjectDoc(project))
			rep := MemoryReport{Measured: true, BudgetLine: fmt.Sprintf("budget: %d bytes", len(text)+len(doc))}
			entries, problems := memory.Check(text, now, w.Root(), w.ProjectDir(project))
			for _, e := range entries {
				if e.Section == memory.CaptainSection {
					rep.Captain++
				} else {
					rep.Lessons++
				}
			}
			anchors, projectProblems := memory.CheckProject(doc, anchorRepos(cfg))
			rep.Anchored = len(anchors)
			for _, p := range append(problems, projectProblems...) {
				rep.Problems = append(rep.Problems, p.String())
			}
			return rep, nil
		},
	}
}

func render(t *testing.T, w *store.Workspace, now time.Time, opts Options) string {
	t.Helper()
	return renderWith(t, w, deps(), now, opts)
}

func renderWith(t *testing.T, w *store.Workspace, d Deps, now time.Time, opts Options) string {
	t.Helper()
	if opts.Binary == "" {
		opts.Binary = "/usr/local/bin/mate"
	}
	out, err := Render(context.Background(), w, d, "shop", now, opts)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	return out
}

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatalf("git not found on PATH (required for this test): %v", err)
	}
	runGit(t, dir, "init", "-b", "main")
	runGit(t, dir, "config", "user.email", "mate-test@example.com")
	runGit(t, dir, "config", "user.name", "mate test")
	runGit(t, dir, "commit", "--allow-empty", "-m", "init")
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(out)
}

func writeFixture(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// partHeadings returns the `== N. title ==` lines in the order printed.
func partHeadings(out string) []string {
	var got []string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "== ") && strings.HasSuffix(l, " ==") {
			got = append(got, l)
		}
	}
	return got
}

// mustOrder fails unless every needle is in out, each after the one before.
func mustOrder(t *testing.T, out string, needles ...string) {
	t.Helper()
	at := 0
	for _, n := range needles {
		i := strings.Index(out[at:], n)
		if i < 0 {
			t.Fatalf("%q missing, or out of order after byte %d, in:\n%s", n, at, out)
		}
		at += i + len(n)
	}
}

// partBody returns part n's body, from its heading to the next.
func partBody(t *testing.T, out string, n int) string {
	t.Helper()
	start := strings.Index(out, fmt.Sprintf("== %d. ", n))
	if start < 0 {
		t.Fatalf("no part %d in:\n%s", n, out)
	}
	body := out[start:]
	if end := strings.Index(body[1:], "\n== "); end >= 0 {
		body = body[:end+1]
	}
	return body
}
