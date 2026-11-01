package mateassets

import (
	"regexp"
	"strings"
	"testing"
)

// TestManualBootstrapIsRecall holds the session start to task 37: a session start
// is the `mate recall` digest, not a list of files to read one by one,
// and the M7 onboarding-scout rule (dispatched by the Mate since M19) and
// the after-compaction rule stay in it.
func TestManualBootstrapIsRecall(t *testing.T) {
	p := fixedParams()
	sec := part(t, renderedManual(t), "## Session start")
	for _, want := range []string{
		"`" + p.MatevBin + " recall shop`",
		"SessionStart hook",
		"`# mate recall shop`",
		"`recall cut to fit`",
		"`ABSENT`",
		"If you cannot name every In flight crew and every open news from your context, run `" + p.MatevBin + " recall shop` before you act on anything.",
		"`⟦mate⟧ stow:`",
	} {
		if !strings.Contains(sec, want) {
			t.Errorf("the session start does not say %s", want)
		}
	}
	if regexp.MustCompile("(?m)^\\d+\\. Read `").MatchString(sec) {
		t.Error("the session start still lists files to read one by one; the digest frames them")
	}
	// The onboarding scout is asked for only while PROJECT.md is a seed
	// (CON-144): the manual is rendered at every start and knows.
	if strings.Contains(sec, "Dispatch a short onboarding scout") {
		t.Error("the session start asks for an onboarding scout although PROJECT.md has content")
	}
	p.ProjectDocSeeded = true
	if sec := part(t, manualWith(t, p), "## Session start"); !strings.Contains(sec, "Dispatch a short onboarding scout") {
		t.Error("the session start does not ask for an onboarding scout while PROJECT.md holds only its headings")
	}
}

// TestCommandsSkillRecallNamesEightParts: the command reference lists the
// digest's parts in the order `mate recall` prints them
// (cmd/mate/recall_test.go holds the command to the same order).
func TestCommandsSkillRecallNamesEightParts(t *testing.T) {
	rec := part(t, skillText(t, commandsSkill), "## Recall")
	starts := []string{"Live state", "`mate project facts`", "`/ws/.mate/projects/shop/PROJECT.md`", "`/ws/.mate/projects/shop/mate/backlog.md` without `## Done`", "`/ws/.mate/projects/shop/mate/memory.md`", "`/ws/.mate/WORKSPACE.md`", "What `memory check` says", "Only when the timeline database exists"}
	for n, want := range starts {
		item := regexp.MustCompile("(?m)^" + string(rune('1'+n)) + `\. (.*)$`).FindStringSubmatch(rec)
		if item == nil || !strings.HasPrefix(item[1], want) {
			t.Errorf("part %d of ### Recall = %q, want it to open with %s", n+1, item, want)
		}
	}
	for _, want := range []string{"`--live`", "`--max-bytes <n>`", "part 1 always survives whole"} {
		if !strings.Contains(rec, want) {
			t.Errorf("### Recall does not say %s", want)
		}
	}
}
