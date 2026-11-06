package main

import (
	"io"
	"strings"
	"testing"
)

// An option's key is a to d: the REPL's Topic page takes the other
// letters as its own keys.
func TestNewsOpenOptionKeys(t *testing.T) {
	t.Parallel()
	for _, opts := range [][]string{
		{"--option", "f=mở session"},
		{"--option", "1=một"},
		{"--option", "ab=hai chữ"},
		{"--option", "a=1", "--option", "b=2", "--option", "c=3", "--option", "d=4", "--option", "e=5"},
	} {
		args := append([]string{"p", "--title", "t"}, opts...)
		err := cmdNewsOpen(args, io.Discard, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "option") {
			t.Errorf("%q: err = %v, want an option error", opts, err)
		}
	}
}

// A session proposes one answer to one news (CON-104).
func TestNewsProposeArgs(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"p", "3"}, {"p", "x", "chọn a"}, {"p", "3", "a", "b"}} {
		if err := cmdNewsPropose(args, io.Discard, io.Discard); err == nil {
			t.Errorf("%q: proposed", args)
		}
	}
}
