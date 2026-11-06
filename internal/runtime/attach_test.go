package runtime_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/observability"
	"github.com/nguyenngocanh94/mate/internal/runtime"
)

// The detach key is the shared Herdr binding and detaching does not stop the
// agent (ADR 0003, re-verified live for G4-07).
func TestDetachKeyIsTheSharedHerdrBinding(t *testing.T) {
	t.Parallel()
	if runtime.DetachKey != "ctrl+b q" {
		t.Fatalf("DetachKey = %q", runtime.DetachKey)
	}
}

func TestTTYHandoffRefusesWhenStdinIsNotATerminal(t *testing.T) {
	t.Parallel()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close(); _ = w.Close() }()
	handoff := runtime.TTYHandoff{Binary: "herdr", Stdin: r, Stdout: os.Stdout, Stderr: os.Stderr}
	err = handoff.Attach(context.Background(), []string{"agent", "attach", "mate-g407"})
	if err == nil {
		t.Fatal("a handoff without a terminal must be refused, not attempted")
	}
	var coded *observability.Error
	if !errors.As(err, &coded) || coded.Code != observability.CodeUsage {
		t.Fatalf("err = %v, want usage", err)
	}
}

// /dev/null is a character device but not a terminal. A mode-bit check
// would let it through, and `go test` hands a process exactly that.
func TestTTYHandoffRefusesDevNull(t *testing.T) {
	t.Parallel()
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		t.Skipf("cannot open %s: %v", os.DevNull, err)
	}
	defer func() { _ = devNull.Close() }()
	info, err := devNull.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeCharDevice == 0 {
		t.Skip("this platform does not report os.DevNull as a character device")
	}
	handoff := runtime.TTYHandoff{Binary: "herdr", Stdin: devNull, Stdout: devNull, Stderr: devNull}
	err = handoff.Attach(context.Background(), []string{"agent", "attach", "mate-g407"})
	var coded *observability.Error
	if !errors.As(err, &coded) || coded.Code != observability.CodeUsage {
		t.Fatalf("err = %v, want usage; /dev/null is not a terminal", err)
	}
}

func TestTTYHandoffRefusesEmptyArgv(t *testing.T) {
	t.Parallel()
	handoff := runtime.TTYHandoff{Binary: "herdr"}
	if err := handoff.Attach(context.Background(), nil); err == nil {
		t.Fatal("empty argv must be refused")
	}
}

func TestAttachExitErrorMapping(t *testing.T) {
	t.Parallel()
	cases := []struct {
		exit int
		want observability.Code
	}{
		{2, observability.CodeUsage},
		{1, observability.CodeUnknown},
		{101, observability.CodeUnknown},
	}
	for _, tc := range cases {
		err := runtime.AttachExitError(tc.exit)
		var coded *observability.Error
		if !errors.As(err, &coded) {
			t.Fatalf("exit %d: err = %v", tc.exit, err)
		}
		if coded.Code != tc.want {
			t.Fatalf("exit %d mapped to %q, want %q", tc.exit, coded.Code, tc.want)
		}
	}
	if runtime.AttachExitError(0) != nil {
		t.Fatal("exit 0 is a clean detach, not an error")
	}
}

// exec.ExitError carries the child's status; the handoff must report it as a
// coded error rather than leaking os/exec detail to the CLI envelope.
func TestAttachExitErrorFromExitError(t *testing.T) {
	t.Parallel()
	cmd := exec.Command("sh", "-c", "exit 2")
	runErr := cmd.Run()
	var exitErr *exec.ExitError
	if !errors.As(runErr, &exitErr) {
		t.Fatalf("setup: %v", runErr)
	}
	err := runtime.AttachRunError(runErr)
	var coded *observability.Error
	if !errors.As(err, &coded) || coded.Code != observability.CodeUsage {
		t.Fatalf("err = %v, want usage", err)
	}
	if strings.Contains(coded.Message, "exec:") {
		t.Fatalf("message leaks os/exec detail: %q", coded.Message)
	}
}
