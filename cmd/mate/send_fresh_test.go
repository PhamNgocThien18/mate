package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/send"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// promptRecorder keeps every first prompt the runtime delivered, so a test
// can read what a fresh session was told.
type promptRecorder struct {
	*runtime.Fake
	prompts []string
}

func (p *promptRecorder) PromptAgent(ctx context.Context, handle runtime.AgentHandle, text string) error {
	p.prompts = append(p.prompts, text)
	return p.Fake.PromptAgent(ctx, handle, text)
}

// restingShip spawns a ship over the fake runtime, has it commit, write a
// hand-back and open a PR, and returns the spawn result: the shape of a
// crew `mate send` must open a fresh session for (CON-73).
func restingShip(t *testing.T, w *store.Workspace, deps spawn.Deps, verb string) spawn.CrewResult {
	t.Helper()
	res := spawnFakeCrew(t, w, deps, "shop", "k3")
	if err := os.WriteFile(filepath.Join(res.Worktree, "button.go"), []byte("package shop\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitOrFatal(t, res.Worktree, "add", "button.go")
	runGitOrFatal(t, res.Worktree, "-c", "user.email=k3@example.com", "-c", "user.name=k3", "commit", "-q", "-m", "add the button")
	if err := os.WriteFile(w.CrewHandback("shop", "k3"), []byte("## For the next crew\n- the button is in button.go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"working: adding the button", verb} {
		if err := w.AppendStatus("shop", "k3", line); err != nil {
			t.Fatal(err)
		}
	}
	return res
}

// A message to a crew that has written pr-open opens a fresh session: the
// old pane is never typed into, the new session's input is the four parts
// in order, the message is in sent.log, and the report says why.
func TestSendToARestingCrewOpensAFreshSessionWithFourParts(t *testing.T) {
	t.Parallel()
	w := liveCrewWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeSpawnDeps(t, rt)
	res := restingShip(t, w, deps, "pr-open: https://github.com/x/shop/pull/7")
	typed := false
	rt.OnSendText = func(runtime.AgentHandle, string) { typed = true }
	rec := &promptRecorder{Fake: rt}
	deps.Runtime = rec

	meta, err := w.ReadCrewMeta("shop", "k3")
	if err != nil {
		t.Fatal(err)
	}
	resting, verb, err := restingCrew(w, "shop", "k3", meta)
	if err != nil || !resting || verb != "pr-open" {
		t.Fatalf("restingCrew = %v %q %v, want true pr-open", resting, verb, err)
	}

	message := "Review: F1 the button has no aria-label; F2 the test only checks the happy path. Already tried: nothing, first round."
	fresh, err := freshSession(context.Background(), w, deps, "shop", "k3", message, store.SourceMate)
	if err != nil {
		t.Fatalf("freshSession: %v", err)
	}
	if typed {
		t.Fatal("the resting pane was typed into; a fresh session must replace it instead")
	}
	if fresh.Ordinal != 2 || fresh.Pane == res.Pane {
		t.Fatalf("fresh = ordinal %d pane %q (was %q), want session 2 in a new pane", fresh.Ordinal, fresh.Pane, res.Pane)
	}

	data, err := os.ReadFile(w.CrewSessionInput("shop", "k3", 2))
	if err != nil {
		t.Fatalf("session input: %v", err)
	}
	text := string(data)
	briefText, _ := os.ReadFile(res.BriefPath)
	at := -1
	for _, part := range []string{
		"# Part 1: your brief", strings.TrimSpace(string(briefText)),
		"# Part 2: your last hand-back", "- the button is in button.go",
		"# Part 3: the Mate's message", message,
		"# Part 4: change since the last review (git diff --stat main...HEAD)", "button.go |",
	} {
		i := strings.Index(text, part)
		if i < 0 || i < at {
			t.Fatalf("session input lacks %q or has it out of order (at %d, after %d):\n%s", part, i, at, text)
		}
		at = i
	}
	if len(rec.prompts) != 1 || !strings.Contains(rec.prompts[0], w.CrewSessionInput("shop", "k3", 2)) {
		t.Fatalf("prompts = %q, want one pointing at the session input", rec.prompts)
	}
	if !strings.Contains(rec.prompts[0], "Progress so far: Already tried: nothing, first round.") {
		t.Fatalf("prompt %q does not carry the Already tried part as the note", rec.prompts[0])
	}

	entries, _, err := w.ReadSent("shop", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Target != store.CrewTarget("k3") || entries[0].Text != message || entries[0].Source != store.SourceMate {
		t.Fatalf("sent.log = %+v, want the message recorded as sent to crew:k3 by the mate", entries)
	}

	var out bytes.Buffer
	writeFreshSessionReport(&out, "shop", "k3", verb, store.SourceMate, fresh)
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if lines[0] != "fresh session 2 for shop/k3: it had handed back (pr-open), so the message opens a new session instead of typing into the old one" {
		t.Fatalf("first line = %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "relaunched shop/k3: agent "+fresh.Agent+" in pane "+fresh.Pane) || !strings.HasSuffix(lines[1], "; the resting agent was stopped)") {
		t.Fatalf("second line = %q", lines[1])
	}
	if lines[2] != "input "+w.CrewSessionInput("shop", "k3", 2) {
		t.Fatalf("third line = %q", lines[2])
	}
	if !strings.HasPrefix(lines[3], "turn: end it now") {
		t.Fatalf("fourth line = %q, want the turn-end line", lines[3])
	}

	// Right after the relaunch the hand-back belongs to the session that
	// is gone: the crew is not resting on it, so a second message reaches
	// the new pane rather than opening a third session.
	meta, _ = w.ReadCrewMeta("shop", "k3")
	if resting, _, _ := restingCrew(w, "shop", "k3", meta); resting {
		t.Fatal("the crew still reads as resting after its fresh session opened")
	}
	// Once the new session hands back, it rests again.
	if err := w.AppendStatus("shop", "k3", "pr-open: https://github.com/x/shop/pull/7"); err != nil {
		t.Fatal(err)
	}
	if resting, _, _ := restingCrew(w, "shop", "k3", meta); !resting {
		t.Fatal("a hand-back written in the new session must make the crew resting again")
	}
}

// The diff starts at the commit the latest review saw when one is recorded.
func TestSendToARestingCrewDiffsFromTheLatestReview(t *testing.T) {
	t.Parallel()
	w := liveCrewWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeSpawnDeps(t, rt)
	res := restingShip(t, w, deps, "wait-mate: ready on mate/k3")
	reviewed := strings.TrimSpace(gitOut(t, res.Worktree, "rev-parse", "HEAD"))
	if err := os.MkdirAll(w.CrewDir("shop", "rv1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteMeta(w.CrewReviewMeta("shop", "rv1"), map[string]string{"crew": "k3", "head": reviewed, "base": "main"}); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteCrewMeta("shop", "rv1", map[string]string{"harness": "codex", "kind": "scout", "state": "finished", "stopped_at": "2026-10-15T09:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	if got, ok := latestReviewedCommit(w, "shop", "k3"); !ok || got != reviewed {
		t.Fatalf("latestReviewedCommit = %q %v, want %s", got, ok, reviewed)
	}
	if err := os.WriteFile(filepath.Join(res.Worktree, "after.go"), []byte("package shop\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitOrFatal(t, res.Worktree, "add", "after.go")
	runGitOrFatal(t, res.Worktree, "-c", "user.email=k3@example.com", "-c", "user.name=k3", "commit", "-q", "-m", "after the review")

	fresh, err := freshSession(context.Background(), w, deps, "shop", "k3", "F1 is still open", store.SourceMate)
	if err != nil {
		t.Fatalf("freshSession: %v", err)
	}
	data, _ := os.ReadFile(fresh.InputPath)
	if !strings.Contains(string(data), "git diff --stat "+reviewed+"...HEAD") || !strings.Contains(string(data), "after.go") || strings.Contains(string(data), "button.go |") {
		t.Fatalf("session input must diff from the reviewed commit only:\n%s", data)
	}
}

// --same-session keeps the old behaviour: the line is typed into the
// resting crew's pane and no session is opened.
func TestSendSameSessionTypesIntoTheRestingPane(t *testing.T) {
	t.Parallel()
	w := liveCrewWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeSpawnDeps(t, rt)
	res := restingShip(t, w, deps, "pr-open: https://github.com/x/shop/pull/7")
	handle := runtime.AgentHandle{Session: runtime.SessionHandle{Name: res.Session}, Name: res.Agent}
	rt.SetReadOutput(handle, codexEmptyScreen)
	rt.OnSendText = func(h runtime.AgentHandle, text string) { rt.SetReadOutput(h, codexPendingScreen(text)) }
	rt.OnSendKeys = func(h runtime.AgentHandle, keys []string) { rt.SetReadOutput(h, codexBusyScreen) }

	// The same call cmdSend makes under --same-session: the plain send.
	report, err := sendToCrew(context.Background(), w, deps, "shop", "k3", "F1 again", store.SourceMate, send.Options{})
	if err != nil {
		t.Fatalf("sendToCrew: %v", err)
	}
	if !report.Delivered() {
		t.Fatalf("report = %+v, want delivered", report)
	}
	meta, _ := w.ReadCrewMeta("shop", "k3")
	if meta[spawn.MetaPane] != res.Pane || meta[spawn.MetaSessions] != "1" {
		t.Fatalf("meta pane %q sessions %q; --same-session must not relaunch", meta[spawn.MetaPane], meta[spawn.MetaSessions])
	}
	if _, err := os.Stat(w.CrewSessionInput("shop", "k3", 2)); !os.IsNotExist(err) {
		t.Fatal("a session input was written under --same-session")
	}
}

// A crew that is working, and a scout that handed back, are not resting:
// restingCrew says so and the plain send is what they get.
func TestRestingCrewIsOnlyAShipThatHandedBack(t *testing.T) {
	t.Parallel()
	w := liveCrewWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeSpawnDeps(t, rt)
	spawnFakeCrew(t, w, deps, "shop", "k3")
	if err := w.AppendStatus("shop", "k3", "working: on it"); err != nil {
		t.Fatal(err)
	}
	meta, _ := w.ReadCrewMeta("shop", "k3")
	if resting, _, _ := restingCrew(w, "shop", "k3", meta); resting {
		t.Fatal("a working crew reads as resting")
	}
	if err := w.AppendStatus("shop", "k3", "needs-decision: A or B?"); err != nil {
		t.Fatal(err)
	}
	if resting, _, _ := restingCrew(w, "shop", "k3", meta); resting {
		t.Fatal("a crew with a question reads as resting")
	}
	if err := w.AppendStatus("shop", "k3", "wait-mate: done"); err != nil {
		t.Fatal(err)
	}
	if resting, verb, _ := restingCrew(w, "shop", "k3", meta); !resting || verb != "wait-mate" {
		t.Fatalf("a local ship that handed back: resting %v verb %q, want true wait-mate", resting, verb)
	}
	scout := map[string]string{}
	for k, v := range meta {
		scout[k] = v
	}
	scout[spawn.MetaKind] = "scout"
	if resting, _, _ := restingCrew(w, "shop", "k3", scout); resting {
		t.Fatal("a scout that handed back reads as resting; its hand-back is its report")
	}
}

func TestAlreadyTriedIsTheTailOfTheMessage(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"F1 open. Already tried: go test ./..., still red on TestX.": "Already tried: go test ./..., still red on TestX.",
		"F1 open; we ALREADY TRIED\n  a rebase  and a retry":         "ALREADY TRIED a rebase and a retry",
		"F1 open, nothing more":                                      "",
	}
	for in, want := range cases {
		if got := alreadyTried(in); got != want {
			t.Errorf("alreadyTried(%q) = %q, want %q", in, got, want)
		}
	}
}
