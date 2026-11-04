package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

const mainLines = `{"type":"user","timestamp":"2026-11-04T01:00:00.000Z","message":{"role":"user","content":"hi"}}
{"type":"assistant","timestamp":"2026-11-04T01:00:01.000Z","message":{"id":"msg_1","model":"claude-opus-5-5","usage":{"input_tokens":10,"cache_creation_input_tokens":100,"cache_read_input_tokens":1000,"output_tokens":5,"output_tokens_details":{"thinking_tokens":2}}}}
{"type":"assistant","timestamp":"2026-11-04T01:00:02.000Z","message":{"id":"msg_1","model":"claude-opus-5-5","usage":{"input_tokens":10,"cache_creation_input_tokens":100,"cache_read_input_tokens":1000,"output_tokens":5,"output_tokens_details":{"thinking_tokens":2}}}}
{"type":"attachment","timestamp":"2026-11-04T01:00:03.000Z"}
{"type":"assistant","timestamp":"2026-11-04T02:00:00.000Z","message":{"id":"msg_2","model":"claude-opus-5-5","usage":{"input_tokens":1,"cache_creation_input_tokens":0,"cache_read_input_tokens":2000,"output_tokens":50}}}
`

// A subagent's records restate a growing snapshot: the last one counts.
const subLines = `{"type":"assistant","isSidechain":true,"agentId":"a1","timestamp":"2026-11-04T01:30:00.000Z","message":{"id":"msg_s","model":"claude-sonnet-5-5","usage":{"input_tokens":3,"cache_creation_input_tokens":7,"cache_read_input_tokens":11,"output_tokens":20,"output_tokens_details":{"thinking_tokens":4}}}}
{"type":"assistant","isSidechain":true,"agentId":"a1","timestamp":"2026-11-04T01:30:01.000Z","message":{"id":"msg_s","model":"claude-sonnet-5-5","usage":{"input_tokens":3,"cache_creation_input_tokens":7,"cache_read_input_tokens":11,"output_tokens":60,"output_tokens_details":{"thinking_tokens":9}}}}
`

func fixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	session := filepath.Join(dir, "abc.jsonl")
	if err := os.WriteFile(session, []byte(mainLines), 0o600); err != nil {
		t.Fatal(err)
	}
	subs := filepath.Join(dir, "abc", "subagents")
	if err := os.MkdirAll(subs, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subs, "agent-a1.jsonl"), []byte(subLines), 0o600); err != nil {
		t.Fatal(err)
	}
	return session
}

func TestOneCallPerMessageIDLastRecordWins(t *testing.T) {
	files, err := expand([]string{fixture(t)})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("want session + subagent, got %v", files)
	}
	r, err := sum(files, window{})
	if err != nil {
		t.Fatal(err)
	}
	// main: msg_1 once (10+100+1000+5) + msg_2 (1+0+2000+50); sub: msg_s last record (3+7+11+60).
	if got, want := r.Totals.Calls, 3; got != want {
		t.Errorf("calls: want %d, got %d", want, got)
	}
	if got, want := r.Totals.Total(), int64(1115+2051+81); got != want {
		t.Errorf("total: want %d, got %d", want, got)
	}
	if got, want := r.Totals.Output, int64(5+50+60); got != want {
		t.Errorf("output: want %d, got %d (last subagent record must win)", want, got)
	}
	if got, want := r.Totals.Thinking, int64(2+9); got != want {
		t.Errorf("thinking: want %d, got %d", want, got)
	}
	if r.Models["claude-opus-5-5"] != 2 || r.Models["claude-sonnet-5-5"] != 1 {
		t.Errorf("models: %v", r.Models)
	}
}

func TestWindowKeepsOnlyRecordsInside(t *testing.T) {
	files, _ := expand([]string{fixture(t)})
	w := window{since: mustTime(t, "2026-11-04T01:15:00Z"), until: mustTime(t, "2026-11-04T01:45:00Z")}
	r, err := sum(files, w)
	if err != nil {
		t.Fatal(err)
	}
	if r.Totals.Calls != 1 || r.Totals.Output != 60 {
		t.Errorf("window: want only the subagent call, got %+v", r.Totals)
	}
	if r.Skipped != 3 {
		t.Errorf("skipped: want 3 records outside the window, got %d", r.Skipped)
	}
}

func TestDirectoryArgumentAndTable(t *testing.T) {
	session := fixture(t)
	var out bytes.Buffer
	if err := run([]string{filepath.Dir(session)}, &out); err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`(?m)^TOTAL\s+3\s+14\s+3011\s+107\s+115\s+11\s+3247$`).MatchString(out.String()) {
		t.Errorf("table lacks the totals row: %s", out.String())
	}
	if !strings.Contains(out.String(), "model claude-sonnet-5-5: 1 calls") {
		t.Errorf("table lacks the model line: %s", out.String())
	}
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := parseTime(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
