package runtime

import (
	"fmt"
	"strings"

	"github.com/nguyenngocanh94/mate/internal/observability"
)

// ValidateSendText refuses text Herdr or the harness would turn into something
// other than one typed line: nothing at all, a newline (which submits, and
// submitting is the caller's separate, verified step), or any other control
// character except the 0x1f from-app marker mate prefixes deliberately.
func ValidateSendText(text string) error {
	if text == "" {
		return observability.NewError(observability.CodeUsage, "send-text requires text")
	}
	for _, r := range text {
		if r == 0x1f {
			continue
		}
		if r == '\n' || r == '\r' {
			return observability.NewError(observability.CodeUsage,
				"send-text refuses a newline: submitting is a separate, verified step")
		}
		if r < 0x20 || r == 0x7f {
			return observability.NewError(observability.CodeUsage,
				fmt.Sprintf("send-text refuses control character %q", r))
		}
	}
	return nil
}

// CheckSendKeys refuses a key list Herdr would misread: nothing, an empty
// token, a token carrying whitespace (two presses in one argument), or a token
// that would be parsed as an option. Only the caller's measured key names
// belong here.
func CheckSendKeys(keys []string) error {
	if len(keys) == 0 {
		return observability.NewError(observability.CodeUsage, "send-keys requires at least one key")
	}
	for _, k := range keys {
		if k == "" || strings.ContainsAny(k, " \t\r\n") || strings.HasPrefix(k, "-") {
			return observability.NewError(observability.CodeUsage, fmt.Sprintf("send-keys refuses key %q: one bare key name per press", k))
		}
	}
	return nil
}
