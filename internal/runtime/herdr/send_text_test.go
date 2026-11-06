package herdr_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/harness/claude"
	"github.com/nguyenngocanh94/mate/internal/observability"
	"github.com/nguyenngocanh94/mate/internal/process"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/runtime/herdr"
)

const agentGetJSON = `{"id":"cli:agent:get","result":{"agent":{"agent":"claude","agent_status":"idle","cwd":"/w","interactive_ready":true,"name":"mate-shop","pane_id":"w9:p9","tab_id":"w9:t9","terminal_id":"t9","workspace_id":"w9"},"type":"agent_info"}}`

// SendText is `pane send-text`, which Herdr addresses by pane and not by
// agent name. The pane therefore has to be resolved live from `agent get`
// inside the same call; the recorded handle is only checked against it.
func TestHerdrSendTextResolvesThePaneLiveBeforeTyping(t *testing.T) {
	t.Parallel()
	var argvs [][]string
	rt := herdr.NewHerdr(&process.FakeRunner{Handler: func(_ context.Context, spec process.Spec) (process.Result, error) {
		if !hasSessionBeforeTerminator(spec.Args) {
			t.Fatalf("send-text missing --session in option region: %#v", spec.Args)
		}
		argvs = append(argvs, append([]string(nil), spec.Args...))
		switch {
		case argvHas(spec.Args, "agent", "get"):
			return process.Result{Stdout: []byte(agentGetJSON)}, nil
		case argvHas(spec.Args, "pane", "send-text"):
			return process.Result{Stdout: []byte(`{"id":"cli:pane:send-text","result":{"type":"ok"}}`)}, nil
		}
		t.Fatalf("unexpected argv %#v", spec.Args)
		return process.Result{}, nil
	}})
	h := runtime.AgentHandle{Session: runtime.SessionHandle{Name: "lab"}, Name: "mate-shop", Kind: claude.KindClaude, Tab: runtime.TabHandle{PaneID: "w9:p9"}}
	if err := rt.SendText(context.Background(), h, "say PONG"); err != nil {
		t.Fatal(err)
	}
	if len(argvs) != 2 {
		t.Fatalf("herdr calls = %d, want the resolve and the send: %#v", len(argvs), argvs)
	}
	got := strings.Join(argvs[1], " ")
	if !strings.Contains(got, "pane send-text w9:p9 \x1b[200~say PONG\x1b[201~") {
		t.Fatalf("argv = %q, want bracketed paste sent to the live pane", got)
	}
	if strings.Contains(got, "enter") || strings.Contains(got, "send-keys") {
		t.Fatalf("send-text submitted the line: %q", got)
	}
}

// A handle whose recorded pane no longer holds the agent is refused, not
// typed into: docs/mvp.md section 7 ("do not trust an old handle").
func TestHerdrSendTextRefusesAPaneThatMoved(t *testing.T) {
	t.Parallel()
	rt := herdr.NewHerdr(&process.FakeRunner{Handler: func(_ context.Context, spec process.Spec) (process.Result, error) {
		if argvHas(spec.Args, "agent", "get") {
			return process.Result{Stdout: []byte(agentGetJSON)}, nil
		}
		t.Fatalf("nothing may be typed after a pane mismatch: %#v", spec.Args)
		return process.Result{}, nil
	}})
	h := runtime.AgentHandle{Session: runtime.SessionHandle{Name: "lab"}, Name: "mate-shop", Tab: runtime.TabHandle{PaneID: "w1:p1"}}
	err := rt.SendText(context.Background(), h, "hello")
	var coded *observability.Error
	if !errors.As(err, &coded) || coded.Code != observability.CodeStateConflict {
		t.Fatalf("err = %v, want a state_conflict refusal", err)
	}
}

func TestHerdrSendTextRefusesTextThatIsNotOneLine(t *testing.T) {
	t.Parallel()
	rt := herdr.NewHerdr(&process.FakeRunner{Handler: func(_ context.Context, spec process.Spec) (process.Result, error) {
		t.Fatalf("no Herdr call may be made for refused text: %#v", spec.Args)
		return process.Result{}, nil
	}})
	h := runtime.AgentHandle{Session: runtime.SessionHandle{Name: "lab"}, Name: "mate-shop"}
	for _, text := range []string{"", "two\nlines", "carriage\rreturn", "bell\a", "escape\x1b[201~"} {
		err := rt.SendText(context.Background(), h, text)
		var coded *observability.Error
		if !errors.As(err, &coded) || coded.Code != observability.CodeUsage {
			t.Fatalf("text %q: err = %v, want usage refusal", text, err)
		}
	}
	// The 0x1f from-app marker is the one control byte mate types on purpose.
	if err := herdr.NewHerdr(&process.FakeRunner{Handler: func(_ context.Context, spec process.Spec) (process.Result, error) {
		if argvHas(spec.Args, "agent", "get") {
			return process.Result{Stdout: []byte(agentGetJSON)}, nil
		}
		return process.Result{Stdout: []byte(`{"id":"cli:pane:send-text","result":{"type":"ok"}}`)}, nil
	}}).SendText(context.Background(), runtime.AgentHandle{Session: runtime.SessionHandle{Name: "lab"}, Name: "mate-shop"}, "\x1f digest"); err != nil {
		t.Fatalf("the marker prefix must be typeable: %v", err)
	}
}
