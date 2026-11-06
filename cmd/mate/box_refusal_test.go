package main

import (
	"errors"

	"github.com/nguyenngocanh94/mate/internal/send"
)

// boxSendRefusal reports whether an error is one of internal/send's five
// "nothing was typed" refusals. The console shows all five the same way -
// the reason on the outcome line and nothing else done - so this exists for
// the tests that have to tell a refusal from a transport failure.
func boxSendRefusal(err error) bool {
	return errors.Is(err, send.ErrComposerPending) ||
		errors.Is(err, send.ErrAgentBusy) ||
		errors.Is(err, send.ErrComposerUnknown) ||
		errors.Is(err, send.ErrDialogOpen) ||
		errors.Is(err, send.ErrPaneChanged)
}
