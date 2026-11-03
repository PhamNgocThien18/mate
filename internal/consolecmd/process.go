package consolecmd

import (
	"context"
	"io"
)

// Invocation is an external command with whole argv and a working directory.
type Invocation struct {
	Name string
	Args []string
	Dir  string
}

type Runner func(context.Context, Invocation, io.Reader, io.Writer, io.Writer) error

// ReservedKeys are the console's own tree keys. Configured bindings may use
// any other key; the UI's contract test keeps this equal to its handlers.
func ReservedKeys() []string {
	return []string{
		"?", "G", "a", "backspace", "ctrl+c", "d", "down", "end", "enter",
		"esc", "g", "home", "j", "k", "l", "m", "n", "o", "pgdown", "pgup",
		"q", "r", "s", "shift+tab", "tab", "up", "v", "y",
	}
}
