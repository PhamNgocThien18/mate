package platform

import "runtime"

// URLOpener is URLOpenerFor the system mate runs on.
func URLOpener() (name string, args []string) { return URLOpenerFor(runtime.GOOS) }

// URLOpenerFor is the program that opens a URL with the desktop's default
// handler on goos, and the arguments that go before the URL: open on
// darwin, rundll32 url.dll,FileProtocolHandler on windows, and xdg-open,
// the freedesktop.org command, on linux and every other system. It names
// the program only: finding it on the machine, running it, and what to say
// when it is missing are the caller's.
func URLOpenerFor(goos string) (name string, args []string) {
	switch goos {
	case "darwin":
		return "open", nil
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler"}
	}
	return "xdg-open", nil
}
