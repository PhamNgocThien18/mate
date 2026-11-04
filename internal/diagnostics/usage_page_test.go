package diagnostics

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/telemetry"
)

// The usage page of the HU-403 scout (CON-201): these are its commands,
// shortened. Each one builds or tests, whatever it wrote or probed first,
// so it is "Run tests / build" and not "Mixed activity".
func TestHU403BuildCommandsAreTestsEvenWhenTheyEditFirst(t *testing.T) {
	w := "cd /Users/erics/workspace/.worktrees/mihuv2-hu-403-washi-bbox; "
	commands := []string{
		w + "python3 - <<'EOF'\np='ios/HU403UITests/HU403UITests.swift'\ns=open(p).read()\nopen(p,'w').write(s)\nEOF\n" +
			"udid=72C411F4; xcodebuild -project ios/Mihu.xcodeproj -scheme Mihu -destination id=$udid -derivedDataPath ios/DerivedData build-for-testing > /tmp/bft.log 2>&1; echo exit=$?; " +
			"scripts/sim-run.sh $udid xcodebuild -project ios/Mihu.xcodeproj -scheme Mihu test-without-building -only-testing:HU403UITests > /tmp/ui.log 2>&1; grep -a HU403 /tmp/ui.log | head -60",
		w + "udid=72C411F4; scripts/sim-run.sh $udid xcodebuild -project ios/Mihu.xcodeproj -scheme Mihu test-without-building -only-testing:HU403UITests > /tmp/ui.log 2>&1; echo exit=$?; ls /tmp/hu-403/",
		w + `grep -n "pixelScale" ios/NoteSnapshot.swift | head -2; S=$PWD/.agents/skills/ios-remote-build/scripts; "$S/build.sh" $PWD --test NoteRender > /tmp/build.log 2>&1; echo exit=$?; tail -5 /tmp/build.log`,
		w + `rm -f /tmp/hu-403/measure-*.txt; S=$PWD/.agents/skills/ios-remote-build/scripts; "$S/run-tests.sh" http://vm/tests.zip --worktree mihuv2@x --only 'NoteRenderTests' > /tmp/run.log 2>&1; echo exit=$?; cat /tmp/hu-403/measure-*.txt`,
		w + `sed -i '' 's/let s = NoteSnapshot.pixelScale/let s: CGFloat = 3/' ios/NoteRenderTests.swift; S=$PWD/.agents/skills/ios-remote-build/scripts; "$S/build.sh" $PWD --test NoteRender > /tmp/build.log 2>&1`,
	}
	for i, command := range commands {
		if got := executionWork(Execution{Tool: "Bash", Command: jsonInput(command)}); fmt.Sprint(got) != "[test]" {
			t.Errorf("command %d (json): kinds %v", i, got)
		}
		if got := executionWork(Execution{Tool: "Bash", Command: command}); fmt.Sprint(got) != "[test]" {
			t.Errorf("command %d: kinds %v", i, got)
		}
	}
	// A script is a build only when its name says so.
	for command, want := range map[string]string{
		"scripts/staging.sh status":     "unknown",
		"./run-tests.sh --only Foo":     "test",
		"scripts/test-ui.sh":            "test",
		"timeout 600 go test ./...":     "test",
		"scripts/sim-run.sh $udid open": "unknown",
	} {
		if got := executionKind(Execution{Tool: "Bash", Command: command}); got != want {
			t.Errorf("%q: kind %s, want %s", command, got, want)
		}
	}
	// A build beside a review or a git write is still two kinds of work.
	if got := executionKind(Execution{Tool: "Bash", Command: "go test ./... && git commit -am x"}); got != "mixed" {
		t.Errorf("build + commit: kind %s", got)
	}
}

// A step says how long its commands ran, so the page can show where the
// time waiting on the machine went.
func TestStepCarriesTheMachineTimeOfItsCommands(t *testing.T) {
	in := Input{Facts: []telemetry.Fact{promptFact("s", "p", 0)}}
	for i, tool := range []string{"Read", "Bash"} {
		call := fixtureCall(i, "p")
		in.Calls = append(in.Calls, call)
		summary := "src.go"
		if tool == "Bash" {
			summary = "go test ./..."
		}
		in.Actions = append(in.Actions, Action{ID: call.ID + "#action", SessionID: "s", CallID: call.ID, Tool: tool, Summary: summary, Target: summary,
			StartedAt: stamp(at(i*10 + 1)), EndedAt: stamp(at(i*10 + 1 + 3*i))})
	}
	p := Project(in, Options{Now: at(30), Closed: true})
	steps := p.PromptTurns[0].Overview.Steps
	if len(steps) != 2 || steps[1].Kind != "test" {
		t.Fatalf("steps %+v", steps)
	}
	if steps[1].ToolElapsedMs == nil || *steps[1].ToolElapsedMs != 3000 {
		t.Fatalf("test step machine time %v", steps[1].ToolElapsedMs)
	}
	if steps[0].ToolElapsedMs == nil || *steps[0].ToolElapsedMs != 0 {
		t.Fatalf("read step machine time %v", steps[0].ToolElapsedMs)
	}
}

// jsonInput is a command as Claude records a Bash call: its JSON input.
func jsonInput(command string) string {
	raw, _ := json.Marshal(map[string]string{"command": command})
	return string(raw)
}

func largeOutputInput(bytes int64, contextBefore, contextAfter int64) Input {
	first, next := fixtureCall(0, "p"), fixtureCall(1, "p")
	first.ContextAfter, next.ContextAfter = contextBefore, contextAfter
	opened := telemetry.Fact{ID: "png", Kind: "tool_call", SessionID: "s", HarnessTurnRef: "p", ResponseID: "r0", SourceRef: "png",
		Tool: "Read", Targets: []telemetry.Target{{Path: "/tmp/hu-403/t2-c.png"}}, Command: `{"file_path":"/tmp/hu-403/t2-c.png"}`, OccurredAt: at(1), StartedAt: atp(1), SourceOffset: 101}
	closed := opened
	closed.Kind, closed.OccurredAt, closed.CompletedAt, closed.SourceOffset = "tool_result", at(2), atp(2), 201
	closed.Output = &telemetry.Output{Bytes: bytes, SHA256: "x"}
	return Input{Calls: []Call{first, next}, Facts: []telemetry.Fact{promptFact("s", "p", 0), opened, closed}}
}

// The HU-403 page opened on "PNG returned 230 KB" while the context grew
// by 635 tokens: the image never reached the model as text, so the finding
// stays in the list but out of the top.
func TestLargeOutputTheContextNeverTookInIsNotATopFinding(t *testing.T) {
	p := Project(largeOutputInput(230_052, 100_000, 100_635), Options{Closed: true})
	found := findingsOf(p, "large_output")
	if len(found) != 1 {
		t.Fatalf("large_output findings %+v", p.Findings)
	}
	if !strings.Contains(found[0].Detail, "+635 tokens") || !strings.Contains(found[0].Detail, "never reached the model") {
		t.Fatalf("detail %q", found[0].Detail)
	}
	if len(p.TopFindingIDs) != 0 {
		t.Fatalf("an output the model never took in is a top finding: %v", p.TopFindingIDs)
	}

	p = Project(largeOutputInput(230_052, 100_000, 157_000), Options{Closed: true})
	if len(p.TopFindingIDs) != 1 || !strings.HasPrefix(p.TopFindingIDs[0], "large_output#") {
		t.Fatalf("an output that filled the context is not a top finding: %v", p.TopFindingIDs)
	}
}

// Info findings do not share the top with a warning.
func TestTopFindingsAreWarningsWhenThereAreAny(t *testing.T) {
	in := largeOutputInput(230_052, 100_000, 157_000)
	failed := execution("fail", 3, 4)
	failed.ExitCode, failed.Status = intp(1), "failed"
	in.Facts = append(in.Facts, failed)
	p := Project(in, Options{Closed: true})
	if len(p.Findings) < 2 {
		t.Fatalf("findings %+v", p.Findings)
	}
	if len(p.TopFindingIDs) != 1 || !strings.HasPrefix(p.TopFindingIDs[0], "execution_failure#") {
		t.Fatalf("top %v", p.TopFindingIDs)
	}
}

// Claude's transcripts never carry a child agent's usage, but the page
// says so only when the crew started one.
func TestChildAgentGapOnlyWhenTheCrewStartedOne(t *testing.T) {
	gap := gapText("child_usage_unavailable")
	in := Input{Calls: []Call{fixtureCall(0, "p")}, Facts: []telemetry.Fact{promptFact("s", "p", 0),
		{ID: "gap", Kind: "context", SessionID: "s", Gaps: []string{"child_usage_unavailable", "native_process_unavailable"}},
		{ID: "bash", Kind: "tool_call", SessionID: "s", HarnessTurnRef: "p", ResponseID: "r0", Tool: "Bash", Command: "ls", OccurredAt: at(1), StartedAt: atp(1)}}}
	p := Project(in, Options{Closed: true})
	if strings.Contains(strings.Join(p.Freshness.Missing, "\n"), gap) {
		t.Fatalf("child-agent gap without a child agent: %v", p.Freshness.Missing)
	}
	if !strings.Contains(strings.Join(p.Freshness.Missing, "\n"), gapText("native_process_unavailable")) {
		t.Fatalf("other gaps were dropped: %v", p.Freshness.Missing)
	}
	in.Facts = append(in.Facts, telemetry.Fact{ID: "task", Kind: "tool_call", SessionID: "s", HarnessTurnRef: "p", ResponseID: "r0", Tool: "Task", Command: `{"prompt":"look"}`, OccurredAt: at(2), StartedAt: atp(2)})
	p = Project(in, Options{Closed: true})
	if !strings.Contains(strings.Join(p.Freshness.Missing, "\n"), gap) {
		t.Fatalf("child-agent gap missing after a Task call: %v", p.Freshness.Missing)
	}
}
