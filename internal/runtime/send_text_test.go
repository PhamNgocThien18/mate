package runtime_test

import (
	"context"
	"errors"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/harness/codex"
	"github.com/nguyenngocanh94/mate/internal/runtime"
)

func TestFakeSendTextRecordsTypingAndRunsTheScreenHook(t *testing.T) {
	t.Parallel()
	f := runtime.NewFake()
	session := runtime.SessionHandle{Name: "lab"}
	h := runtime.AgentHandle{Session: session, Name: "crew-1", Kind: codex.KindCodex, Tab: runtime.TabHandle{PaneID: "w1:p1"}}
	f.SeedAgent(h, runtime.AgentIdle)
	f.SetReadOutput(h, "empty")
	f.OnSendText = func(handle runtime.AgentHandle, text string) {
		f.SetReadOutput(handle, "typed: "+text)
	}
	if err := f.SendText(context.Background(), h, "hello"); err != nil {
		t.Fatal(err)
	}
	screen, err := f.ReadAgent(context.Background(), h, harness.ReadRecentUnwrapped, 10)
	if err != nil || screen != "typed: hello" {
		t.Fatalf("screen after hook = %q err=%v", screen, err)
	}
	if len(f.SentText) != 1 || f.SentText[0].Text != "hello" {
		t.Fatalf("sent text = %+v", f.SentText)
	}
	if err := f.SendText(context.Background(), runtime.AgentHandle{Session: session, Name: "nobody"}, "x"); !runtime.IsAgentNotFound(err) {
		t.Fatalf("missing agent err = %v, want agent_not_found", err)
	}
	f.SendTextErr = errors.New("forced")
	if err := f.SendText(context.Background(), h, "x"); err == nil || err.Error() != "forced" {
		t.Fatalf("SendTextErr not honoured: %v", err)
	}
}
