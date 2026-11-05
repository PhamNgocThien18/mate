package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/nguyenngocanh94/mate/internal/gitx"
	"github.com/nguyenngocanh94/mate/internal/spawn"
)

// cmdDiff implements `mate diff <project> <crew> [--stat|--full] [--workspace <dir>]`
// (docs/mvp.md task 21): the review surface for one crew's branch, read-only.
//
// It is the one command a reader - or a Mate about to decide whether a ship
// crew is done - runs before a merge, so it answers in the order a review is
// actually read: what was committed, then what changed. --stat replaces the
// patch with git's own summary; nothing else about the answer changes.
//
// Nothing here writes: no index, no ref, no file, no pane. A crew that is
// mid-turn is unaffected by it, which is why it needs no confirmation and no
// state check before it runs.
func cmdDiff(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("diff", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: mate diff <project> <crew> [--stat|--full] [--workspace <dir>]")
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	statFlag := fs.Bool("stat", true, "print git's change summary (default)")
	fullFlag := fs.Bool("full", false, "print the full patch for direct review")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 2 {
		fs.Usage()
		return newUsageError("mate diff: want exactly 2 arguments: <project> <crew>")
	}
	w, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return err
	}
	text, err := spawn.CrewDiff(context.Background(), w, gitx.New(), fs.Arg(0), fs.Arg(1), *statFlag && !*fullFlag)
	if err != nil {
		return err
	}
	fmt.Fprint(stdout, text)
	return nil
}
