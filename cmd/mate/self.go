package main

import "os"

// selfBinary is the absolute path of this mate binary: the one a Mate is
// told to run, by its manual, by the recall digest's cut notice and by the
// SessionStart hook's fallback line.
func selfBinary() string {
	if exe, err := os.Executable(); err == nil {
		return exe
	}
	return "mate"
}
