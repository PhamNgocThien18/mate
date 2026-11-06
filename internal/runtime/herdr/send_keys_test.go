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

// SendKeys is `agent send-keys <name>`: Herdr resolves the name to its pane
// inside the same request, so mate never looks a pane id up and presses into
// it as two separate calls (PR 96 counter-review B2). No `agent get`, no
// `pane` command, and never the pane the handle recorded.
func TestHerdrSendKeysIsAgentAddressedOneCallPerPress(t *testing.T) {
	t.Parallel()
	var argvs [][]string
	rt := herdr.NewHerdr(&process.FakeRunner{Handler: func(_ context.Context, spec process.Spec) (process.Result, error) {
		if !hasSessionBeforeTerminator(spec.Args) {
			t.Fatalf("send-keys missing --session in option region: %#v", spec.Args)
		}
		argvs = append(argvs, append([]string(nil), spec.Args...))
		if argvHas(spec.Args, "agent", "send-keys") {
			return process.Result{Stdout: []byte(`{"id":"cli:agent:send-keys","result":{"type":"ok"}}`)}, nil
		}
		t.Fatalf("unexpected argv %#v", spec.Args)
		return process.Result{}, nil
	}})
	h := runtime.AgentHandle{Session: runtime.SessionHandle{Name: "lab"}, Name: "mate-g4-01", Kind: claude.KindClaude, Tab: runtime.TabHandle{PaneID: "w9:p9"}}
	if err := rt.SendKeys(context.Background(), h, []string{"down"}); err != nil {
		t.Fatal(err)
	}
	if len(argvs) != 1 {
		t.Fatalf("herdr calls = %d, want exactly one (the press itself): %#v", len(argvs), argvs)
	}
	got := strings.Join(argvs[0], " ")
	if !strings.Contains(got, "agent send-keys mate-g4-01 down") {
		t.Fatalf("argv = %q, want the agent-addressed press", got)
	}
	if strings.Contains(got, "w9:p9") || strings.Contains(got, " pane ") {
		t.Fatalf("send-keys addressed a pane: %q", got)
	}
}

func TestHerdrSendKeysRefusesEmptyOrMalformedKeys(t *testing.T) {
	t.Parallel()
	rt := herdr.NewHerdr(&process.FakeRunner{Handler: func(_ context.Context, spec process.Spec) (process.Result, error) {
		t.Fatalf("no Herdr call may be made for a refused key list: %#v", spec.Args)
		return process.Result{}, nil
	}})
	h := runtime.AgentHandle{Session: runtime.SessionHandle{Name: "lab"}, Name: "mate-g4-01"}
	for _, keys := range [][]string{nil, {}, {""}, {"down enter"}, {"--session"}, {"-x"}} {
		err := rt.SendKeys(context.Background(), h, keys)
		var coded *observability.Error
		if !errors.As(err, &coded) || coded.Code != observability.CodeUsage {
			t.Fatalf("keys %#v: err = %v, want usage refusal", keys, err)
		}
	}
}

func TestHerdrSendKeysReportsAMissingAgentAsNotFound(t *testing.T) {
	t.Parallel()
	rt := herdr.NewHerdr(&process.FakeRunner{Handler: func(_ context.Context, spec process.Spec) (process.Result, error) {
		if argvHas(spec.Args, "agent", "send-keys") {
			// Herdr 0.8.2's own answer for a gone agent, including one whose
			// pane was just closed (measured 2026-09-14).
			return process.Result{ExitCode: 1, Stderr: []byte(`{"error":{"code":"agent_not_found","message":"agent target mate-g4-01 not found"},"id":"cli:agent:send-keys"}`)}, nil
		}
		t.Fatalf("unexpected argv %#v", spec.Args)
		return process.Result{}, nil
	}})
	h := runtime.AgentHandle{Session: runtime.SessionHandle{Name: "lab"}, Name: "mate-g4-01"}
	err := rt.SendKeys(context.Background(), h, []string{"enter"})
	if !runtime.IsAgentNotFound(err) {
		t.Fatalf("err = %v, want agent_not_found", err)
	}
}
