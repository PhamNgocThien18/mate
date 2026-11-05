package recall

// What `mate recall` prints. These tests ran in cmd/mate (recall_test.go)
// before the digest moved here (CON-167); their expected text is
// unchanged. The text another command prints comes from deps(), a
// stand-in for cmd/mate's recallDeps.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/gitx"
	"github.com/nguyenngocanh94/mate/internal/matehost"
	"github.com/nguyenngocanh94/mate/internal/memory"
	"github.com/nguyenngocanh94/mate/internal/process"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// TestRecallPrintsSection12Point5InOrder is B1 on a workspace with every
// file present: seven parts, live state first and memory last, each file
// framed, and the three findings recall adds of its own - the stale anchor,
// the In flight mismatch both ways, the stale lesson.
func TestRecallPrintsSection12Point5InOrder(t *testing.T) {
	w, now, first := fixture(t)
	out := render(t, w, now, Options{})
	t.Logf("\n%s", out)

	want := []string{
		"== 1. Live state ==",
		"== 2. Project facts (mate project facts shop) ==",
		"== 3. PROJECT.md ==",
		"== 4. backlog.md, without Done ==",
		"== 5. memory.md ==",
		"== 6. WORKSPACE.md ==",
		"== 7. Memory check (mate memory check shop) ==",
	}
	if got := partHeadings(out); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("parts = %q, want %q (no part 8 without mate.db)", got, want)
	}
	if !strings.HasPrefix(out, "# mate recall shop · ") || !strings.Contains(strings.SplitN(out, "\n", 3)[1], "Contract: read this once; it is your state") {
		t.Fatalf("the digest does not open with its title and contract line:\n%s", out)
	}
	mustOrder(t, out,
		// 1: mode, next work, the backlog table, inbox, outbox.
		"mode: manual\n", "next work: pick - ", "crews (mate backlog shop):", "ID  STATE", "k1  spawned", "5 open, 1 closed",
		"inbox: 3 unresolved", `stuck, quiet too long: "no status for 15m"`, `k3 needs an answer: "choose red or blue for the button" (crews/k3.status)`, `k4 wait-mate: "ready in branch mate/k4" (crews/k4.status)`,
		"outbox: nothing queued", "news: none open",
		// 2: facts with head.
		"commits: 2 on main", "head: ",
		// 3: PROJECT.md framed, then the stale anchor.
		"----- begin PROJECT.md (", "- A landing page for the ESP32 kit.", "----- end PROJECT.md -----",
		"warning: PROJECT.md line 7 is anchored at shop:main@"+first+", and shop:main has 1 newer commit(s)",
		// 4: backlog without Done, then the mismatches.
		"----- begin backlog.md (", "## Held for the captain", `- k3, asked 2026-09-19: "red or blue?"`,
		"----- end backlog.md (## Done: 2 entries not shown) -----",
		"In flight in backlog.md but not an open crew in part 1: buyesp32",
		"Open crews in part 1 with no `- <id>` line under In flight: k1",
		"## Held for the captain is retired: open a news for each question",
		// 5, 6.
		"----- begin memory.md (", "The canary lesson is HERON-7.", "----- end memory.md -----",
		"----- begin WORKSPACE.md (", "Never merge on a Friday.", "----- end WORKSPACE.md -----",
		"(no rules yet)", "(has rules)",
		// 7.
		"budget: ", "memory.md:8: aging entry last reinforced 2026-08-01", "1 problem(s): load skill stow",
	)
	for _, gone := range []string{"rpi35 (scout, 2026-09-17)", "k5 (ship, 2026-09-18): merged", "## Done"} {
		if strings.Contains(strings.Split(out, "----- end backlog.md")[0], gone) {
			t.Errorf("part 4 printed %q from ## Done", gone)
		}
	}
	if strings.Contains(out, ": ABSENT (") || strings.Contains(out, ": (empty) (") {
		t.Errorf("every file is present and non-empty, yet the digest says ABSENT or (empty)")
	}
}

// TestRecallListsTheOpenNews: the open news are the questions the Mate
// holds for the captain (CON-136), so part 1 carries each with its question
// and the captain's answer; a closed one is gone.
func TestRecallListsTheOpenNews(t *testing.T) {
	w, now, _ := fixture(t)
	path := matehost.NewsPath(w.MateDir("shop"))
	tag, err := matehost.OpenNews(path, matehost.News{Title: "Push the contract tag?", Question: "Can the tag wait until the real API?"}, now)
	if err != nil {
		t.Fatal(err)
	}
	sync, err := matehost.OpenNews(path, matehost.News{Title: "Sync main", Question: "Synced?"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := matehost.CloseNews(path, sync, "synced", false, now); err != nil {
		t.Fatal(err)
	}
	out := render(t, w, now, Options{LiveOnly: true})
	mustOrder(t, out, "outbox: nothing queued", "news: 1 open (mate news list shop)",
		fmt.Sprintf("- #%d ", tag), "waits on the captain: Push the contract tag?", "  asked: Can the tag wait until the real API?")
	if strings.Contains(out, "Sync main") {
		t.Errorf("recall lists a closed news:\n%s", out)
	}
}

// TestRecallListsTheWorkBoard: the open rows of the work board (CON-158)
// follow the news in part 1, with the Mate's last line; an okayed row is
// off the board and gone.
func TestRecallListsTheWorkBoard(t *testing.T) {
	w, now, _ := fixture(t)
	path := matehost.WorkPath(w.MateDir("shop"))
	hook, err := matehost.AddWork(path, matehost.WorkAssigned, "Pre-push hook", "", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := matehost.WaitWork(path, hook, 3, "which hook?", now); err != nil {
		t.Fatal(err)
	}
	old, err := matehost.AddWork(path, matehost.WorkAssigned, "Old thing", "", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := matehost.DoneWork(path, old, "", now); err != nil {
		t.Fatal(err)
	}
	if err := matehost.OkayWork(path, old, now); err != nil {
		t.Fatal(err)
	}
	out := render(t, w, now, Options{LiveOnly: true})
	mustOrder(t, out, "news: none open", "work: 1 on the board (mate work list shop)",
		fmt.Sprintf("- #%d waiting on news #3 Pre-push hook", hook), "    which hook?")
	if strings.Contains(out, "Old thing") {
		t.Errorf("recall lists an okayed row:\n%s", out)
	}
}

// TestRecallTellsAbsentFromEmpty: a missing file and an empty one mean
// different things and never print alike; a seeded file that holds only its
// headings says so, which is what the onboarding-scout rule reads.
func TestRecallTellsAbsentFromEmpty(t *testing.T) {
	w, now, _ := fixture(t)
	if err := os.Remove(w.WorkspaceDoc()); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(w.BacklogFile("shop")); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, w.MemoryFile("shop"), "")
	writeFixture(t, w.ProjectDoc("shop"), memory.ProjectTemplate("shop"))
	if err := os.Remove(w.ProjectCrewDoc("shop")); err != nil {
		t.Fatal(err)
	}
	out := render(t, w, now, Options{})
	t.Logf("\n%s", out)
	mustOrder(t, out,
		"----- end PROJECT.md -----", "note: PROJECT.md holds its headings and nothing else yet",
		"----- backlog.md: ABSENT (", w.BacklogFile("shop"),
		"----- memory.md: (empty) (", w.MemoryFile("shop"),
		"----- WORKSPACE.md: ABSENT (",
		w.ProjectCrewDoc("shop")+" (ABSENT)",
	)
	if strings.Contains(out, "In flight in backlog.md") {
		t.Error("an absent backlog.md cannot disagree with the table")
	}
}

// TestRecallLiveIsPartOneOnly is the resume digest: the crews may have
// moved while the Mate was stopped, the files are still in its context.
func TestRecallLiveIsPartOneOnly(t *testing.T) {
	w, now, _ := fixture(t)
	out := render(t, w, now, Options{LiveOnly: true})
	if got := partHeadings(out); len(got) != 1 || got[0] != "== 1. Live state ==" {
		t.Fatalf("--live printed parts %q", got)
	}
	mustOrder(t, out, "Contract: live state only.", "k3  needs-decision", "inbox: 3 unresolved", "outbox: nothing queued")
	for _, gone := range []string{"PROJECT.md", "memory.md", "budget:"} {
		if strings.Contains(out, gone) {
			t.Errorf("--live printed %q", gone)
		}
	}
}

// TestRecallMaxBytesCutsFromTheBottom: the point of the order is that a cut
// digest keeps the state and loses the memory, and says what it lost.
func TestRecallMaxBytesCutsFromTheBottom(t *testing.T) {
	w, now, _ := fixture(t)
	full := render(t, w, now, Options{})
	live := render(t, w, now, Options{LiveOnly: true})
	for _, max := range []int{len(full) / 2, len(full) - 200, 10} {
		out := render(t, w, now, Options{MaxBytes: max})
		livePart := strings.SplitN(live, "\n== 1. Live state ==\n", 2)[1]
		if !strings.Contains(out, livePart) {
			t.Fatalf("--max-bytes %d lost part of part 1:\n%s", max, out)
		}
		if max > len(live)+500 && len(out) > max {
			t.Fatalf("--max-bytes %d printed %d bytes", max, len(out))
		}
		last := strings.TrimSpace(out[strings.LastIndex(strings.TrimRight(out, "\n"), "\n")+1:])
		if !strings.HasPrefix(last, fmt.Sprintf("recall cut to fit %d bytes; not shown: ", max)) ||
			!strings.Contains(last, "part 7 (Memory check") || !strings.Contains(last, "Run `/usr/local/bin/mate recall shop --part ") ||
			!strings.HasSuffix(last, ",7` once before acting on anything those parts cover.") {
			t.Fatalf("--max-bytes %d: last line = %q", max, last)
		}
		for _, l := range strings.Split(out, "\n") {
			if strings.HasPrefix(l, "== ") && strings.HasSuffix(l, " ==") {
				idx := strings.Index(out, l)
				rest := strings.TrimLeft(out[idx+len(l):], "\n")
				if strings.HasPrefix(rest, "recall cut") {
					t.Fatalf("--max-bytes %d left the heading %q with no body", max, l)
				}
			}
		}
	}
	if got := render(t, w, now, Options{MaxBytes: len(full) + 1000}); got != full {
		t.Fatal("a limit the digest fits under changed it")
	}
}

// TestRecallTimelineHints is part 8: only with mate.db, and only hints -
// the lines the Mate sent crews after their spawn, the questions crews
// asked, and the Mate's context.
func TestRecallTimelineHints(t *testing.T) {
	w, now, _ := fixture(t)
	d, err := db.Open(w)
	if err != nil {
		t.Fatal(err)
	}
	at := func(ago time.Duration) string { return db.FormatTime(now.Add(-ago)) }
	stmts := []struct {
		q    string
		args []any
	}{
		{`INSERT INTO actor(id, project, kind, name) VALUES ('shop/mate','shop','mate','mate'), ('shop/k2','shop','crew','k2'), ('shop/k9','shop','crew','k9')`, nil},
		{`INSERT INTO task(crew_actor_id, project, spawned_at) VALUES ('shop/k2','shop',?), ('shop/k9','shop',?)`, []any{at(3 * time.Hour), at(20 * 24 * time.Hour)}},
		{`INSERT INTO event(id, dedup, project, at, actor_id, kind) VALUES
			(1,'a','shop',?,'shop/mate','message.sent'),
			(2,'b','shop',?,'shop/mate','message.sent'),
			(3,'c','shop',?,'shop/mate','message.sent'),
			(4,'d','shop',?,'shop/k2','question.asked')`, []any{at(4 * time.Hour), at(2 * time.Hour), at(16 * 24 * time.Hour), at(90 * time.Minute)}},
		{`INSERT INTO message(event_id, from_actor_id, to_actor_id, channel, text) VALUES
			(1,'shop/mate','shop/k2','pane','the brief, before spawn'),
			(2,'shop/mate','shop/k2','pane','use the staging bucket, not prod'),
			(3,'shop/mate','shop/k9','pane','too old to matter')`, nil},
		{`INSERT INTO question(id, asked_event_id, crew_actor_id, text, asked_at) VALUES ('q1',4,'shop/k2','which bucket?',?)`, []any{at(90 * time.Minute)}},
		{`INSERT INTO pricing(model, context_window) VALUES ('m1', 200000)`, nil},
		{`INSERT INTO session(id, actor_id) VALUES ('s1','shop/mate')`, nil},
		{`INSERT INTO turn(id, actor_id, session_id, started_at, model, context_tokens_after) VALUES ('t1','shop/mate','s1',?,'m1',106000)`, []any{at(time.Hour)}},
	}
	for _, s := range stmts {
		if _, err := d.SQL().Exec(s.q, s.args...); err != nil {
			t.Fatalf("%s: %v", s.q, err)
		}
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	out := render(t, w, now, Options{})
	t.Logf("\n%s", out)
	heads := partHeadings(out)
	if heads[len(heads)-1] != "== 8. Timeline hints (mate.db: hints only, never memory) ==" {
		t.Fatalf("parts = %q, want part 8 last", heads)
	}
	part8 := out[strings.Index(out, "== 8."):]
	mustOrder(t, part8,
		"Mate context: 53% of the window after your latest turn",
		"lines you sent crews after their spawn, last 14 days: 1", `to k2: "use the staging bucket, not prod"`,
		"questions crews asked, last 14 days: 1", `k2 (unanswered): "which bucket?"`,
		"Nothing above is memory",
	)
	for _, gone := range []string{"the brief, before spawn", "too old to matter"} {
		if strings.Contains(part8, gone) {
			t.Errorf("part 8 printed %q", gone)
		}
	}
}

// A mate.db that cannot be opened is one line in part 8; parts 1 to 7
// print as they do without it.
func TestRecallUnreadableTimeline(t *testing.T) {
	w, now, _ := fixture(t)
	without := render(t, w, now, Options{})
	path, err := db.Path(w)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, path, "not a database")
	out := render(t, w, now, Options{})
	if !strings.HasPrefix(out, strings.TrimSuffix(without, "\n")) {
		t.Fatalf("an unreadable mate.db changed parts 1-7:\nwithout:\n%s\nwith:\n%s", without, out)
	}
	part8 := partBody(t, out, 8)
	if !strings.HasPrefix(part8, "== 8. Timeline hints (mate.db: hints only, never memory) ==\ntimeline unreadable: ") {
		t.Fatalf("part 8 = %q, want the timeline reported unreadable", part8)
	}
	if strings.Contains(part8, "Mate context:") {
		t.Errorf("part 8 printed hints from a database it could not open:\n%s", part8)
	}
}

// TestRecallNoRepo: a project without a repo still recalls; part 2 says it
// has none, part 3 compares nothing, and part 7 reports the repo-state
// line that nothing can anchor.
func TestRecallNoRepo(t *testing.T) {
	w := noRepoFixture(t)
	writeFixture(t, w.ProjectDoc("shop"), "# shop\n\n## Layout and state\n- A Hugo site. (crews/k1/report.md, shop:main@3f2a91c)\n")
	out := render(t, w, time.Now(), Options{})
	part2 := partBody(t, out, 2)
	if !strings.Contains(part2, "\nrepos: none\nhint: project shop has no repo yet") {
		t.Errorf("part 2 does not say the project has no repo:\n%s", part2)
	}
	if part3 := partBody(t, out, 3); strings.Contains(part3, "warning:") {
		t.Errorf("part 3 compared anchors with no repo:\n%s", part3)
	}
	if part7 := partBody(t, out, 7); !strings.Contains(part7, "PROJECT.md:4: `## Layout and state` line states repository state, but the project has no repo") {
		t.Errorf("part 7 does not report the unanchorable line:\n%s", part7)
	}
}

// TestRecallTwoRepos: part 2 is one facts block per repo, and part 3 warns
// about each anchor against its own repo's head.
func TestRecallTwoRepos(t *testing.T) {
	w := twoRepoFixture(t)
	shop := w.RepoDir("shop/shop")
	first := strings.TrimSpace(gitOutput(t, shop, "rev-parse", "--short", "main"))
	runGit(t, shop, "commit", "--allow-empty", "-m", "two")
	head := strings.TrimSpace(gitOutput(t, shop, "rev-parse", "--short", "main"))
	writeFixture(t, w.ProjectDoc("shop"), "# shop\n\n## Layout and state\n- Web. (crews/k1/report.md, shop:main@"+first+")\n- API. (project facts, api:develop@none)\n")
	out := render(t, w, time.Now(), Options{})
	mustOrder(t, partBody(t, out, 2),
		"shop: repo shop at "+shop+", default branch main", "head: "+head+" (anchor shop:main@"+head+")",
		"\n\nshop: repo api at "+w.RepoDir("shop/api")+", default branch develop", "head: none (anchor api:develop@none)")
	part3 := partBody(t, out, 3)
	if !strings.Contains(part3, "warning: PROJECT.md line 4 is anchored at shop:main@"+first+", and shop:main has 1 newer commit(s)") {
		t.Errorf("part 3 lacks the stale shop anchor:\n%s", part3)
	}
	if strings.Count(part3, "warning:") != 1 {
		t.Errorf("part 3 warns about more than the one stale anchor:\n%s", part3)
	}
}

// A repo whose anchors cannot be compared is said in part 3, one line per
// repo; and a memory check that fails before it measures the budget is
// said in part 7. Neither stops the digest.
func TestRecallReportsAFailedCheckInPlace(t *testing.T) {
	w, now, _ := fixture(t)
	d := deps()
	d.AnchorWarnings = func(context.Context, gitx.Git, string, store.RepoConfig, []memory.Anchor) ([]string, error) {
		return nil, errors.New("bad object")
	}
	d.MemoryCheck = func(context.Context, *store.Workspace, gitx.Git, string, time.Time) (MemoryReport, error) {
		return MemoryReport{}, errors.New("memory.md: permission denied")
	}
	out := renderWith(t, w, d, now, Options{})
	if part3 := partBody(t, out, 3); !strings.Contains(part3, "warning: could not compare PROJECT.md's anchors with shop:main: bad object\n") {
		t.Errorf("part 3 does not say the comparison failed:\n%s", part3)
	}
	if part7 := partBody(t, out, 7); !strings.HasSuffix(part7, "\nmemory check failed: memory.md: permission denied\n") {
		t.Errorf("part 7 does not say the check failed:\n%s", part7)
	}

	// A check that measured the budget before it failed prints what it has.
	d.MemoryCheck = func(context.Context, *store.Workspace, gitx.Git, string, time.Time) (MemoryReport, error) {
		return MemoryReport{Measured: true, BudgetLine: "budget: 10 of 100", Captain: 1}, errors.New("comparing anchors: bad object")
	}
	out = renderWith(t, w, d, now, Options{})
	if part7 := partBody(t, out, 7); !strings.HasSuffix(part7, "\nbudget: 10 of 100\nmemory ok: 1 captain entry, 0 lesson(s), 0 anchored PROJECT.md line(s)\n") {
		t.Errorf("part 7 dropped what the check measured:\n%s", part7)
	}
}

// --part prints only the parts asked for, whole, under a contract that says
// the rest of the digest still stands; a part the digest does not have is
// refused (CON-68).
func TestRecallPartPrintsOnlyThoseParts(t *testing.T) {
	w, now, _ := fixture(t)
	full := render(t, w, now, Options{})
	out := render(t, w, now, Options{Parts: []int{5, 3}})
	if !strings.Contains(out, "Contract: only the part(s) you asked for (5,3)") {
		t.Fatalf("no part contract:\n%s", out)
	}
	for _, heading := range []string{"\n== 1. ", "\n== 2. ", "\n== 4. ", "\n== 6. ", "\n== 7. "} {
		if strings.Contains(out, heading) {
			t.Errorf("--part 5,3 printed%s:\n%s", heading, out)
		}
	}
	for _, heading := range []string{"\n== 3. PROJECT.md ==\n", "\n== 5. memory.md ==\n"} {
		if !strings.Contains(out, heading) {
			t.Fatalf("--part 5,3 lacks %q:\n%s", heading, out)
		}
	}
	part5 := full[strings.Index(full, "\n== 5. memory.md ==\n"):strings.Index(full, "\n== 6. ")]
	if !strings.Contains(out, part5) {
		t.Errorf("part 5 was not printed whole")
	}
	_, err := Render(context.Background(), w, deps(), "shop", now, Options{Parts: []int{42}})
	var noPart *NoPartError
	if !errors.As(err, &noPart) || err.Error() != "mate recall: this digest has no part 42" {
		t.Errorf("a part the digest does not have: err = %v, want a NoPartError", err)
	}
	if _, err := ParseParts("3,x"); err == nil {
		t.Error("--part 3,x was accepted")
	}
	if got, err := ParseParts(" 4, 3,4 "); err != nil || fmt.Sprint(got) != "[4 3]" {
		t.Errorf("ParseParts(\" 4, 3,4 \") = %v, %v; want [4 3]", got, err)
	}
}

// With a tracker plugin, part 4 reads each In flight pointer through the
// plugin's read command and names the entries that are not pointers; with
// a tracker that has no read command it says so instead. The part order
// is unchanged.
func TestRecallCrossChecksInFlightWithTheTracker(t *testing.T) {
	w, now, _ := fixture(t)
	writeFixture(t, w.BacklogFile("shop"), "# Backlog\n\n## In flight\n- k2: HVN-12\n- k3: HVN-99\n- k4 ship the button\n\n## Held for the captain\n\n## Queued\n\n## Done\n")
	trackerPlugin(t, w, true)
	out := render(t, w, now, Options{})
	mustOrder(t, out,
		"== 4. backlog.md, without Done ==",
		"Open crews in part 1 with no `- <id>` line under In flight: k1, k6",
		"In flight entries that are not a `- <id>: <tracker-id>` pointer, with tracker-work holding the tasks: k4\n",
		"tracker tracker-work (./read.sh):\n",
		"- k2: HVN-12 · HVN-12 In Progress: fix the flaky test\n",
		"- k3: HVN-99 · unknown to the tracker (exit 1: not found: HVN-99)\n",
		"== 5. memory.md ==",
	)
	if strings.Contains(out, "second line") {
		t.Fatalf("more than the first line of the tracker's answer was printed:\n%s", out)
	}

	trackerPlugin(t, w, false)
	out = render(t, w, now, Options{})
	mustOrder(t, out,
		"== 4. backlog.md, without Done ==",
		"tracker tracker-work: no read command (tracker.read in .mate/plugins.yaml), so 2 In flight pointer(s) were not cross-checked\n",
		"== 5. memory.md ==",
	)

	// Without a tracker plugin part 4 says nothing about trackers.
	if err := os.Remove(filepath.Join(w.Root(), ".mate", "plugins.yaml")); err != nil {
		t.Fatal(err)
	}
	if out = render(t, w, now, Options{}); strings.Contains(out, "tracker") {
		t.Fatalf("a project without a tracker plugin mentions one:\n%s", out)
	}
}

// silentTracker is a tracker that never answers: it waits for the read's
// deadline, after checking there is one no later than trackerReadEach.
type silentTracker struct {
	t     *testing.T
	calls int
}

func (s *silentTracker) Run(ctx context.Context, spec process.Spec) (process.Result, error) {
	s.calls++
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > trackerReadEach {
		s.t.Errorf("tracker read %v runs without a deadline of at most %s", spec.Args, trackerReadEach)
		return process.Result{}, errors.New("no deadline")
	}
	return process.Result{}, context.DeadlineExceeded
}

// A tracker that does not answer costs each pointer its own line saying
// so, inside the read's deadline, and the digest goes on to part 5.
func TestRecallTrackerThatDoesNotAnswer(t *testing.T) {
	w, now, _ := fixture(t)
	writeFixture(t, w.BacklogFile("shop"), "# Backlog\n\n## In flight\n- k2: HVN-12\n- k3: HVN-99\n\n## Done\n")
	trackerPlugin(t, w, true)
	d := deps()
	silent := &silentTracker{t: t}
	d.Tracker = silent
	out := renderWith(t, w, d, now, Options{})
	mustOrder(t, out,
		"tracker tracker-work (./read.sh):\n",
		"- k2: HVN-12 · unreadable: context deadline exceeded\n",
		"- k3: HVN-99 · unreadable: context deadline exceeded\n",
		"== 5. memory.md ==",
	)
	if silent.calls != 2 {
		t.Errorf("the tracker was asked %d times, want once per pointer (2)", silent.calls)
	}
}

// SplitBacklog keeps every section but Done, counts Done's entries, and
// reads the In flight ids however their line goes on.
func TestSplitBacklog(t *testing.T) {
	kept, done, ids := SplitBacklog("# Backlog\n\n## In flight\n- k2 (ship): x\n- k3: HVN-9\n- k4\n\n## Done (10 most recent)\n- k1: merged\n- k0: merged\n")
	if strings.Contains(kept, "Done") || strings.Contains(kept, "k1: merged") {
		t.Errorf("kept = %q, still holds ## Done", kept)
	}
	if done != 2 || strings.Join(ids, ",") != "k2,k3,k4" {
		t.Errorf("done = %d, ids = %q; want 2 and k2,k3,k4", done, ids)
	}
}
