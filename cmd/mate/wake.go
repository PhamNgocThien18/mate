package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/nguyenngocanh94/mate/internal/gitx"
	"github.com/nguyenngocanh94/mate/internal/wake"
)

// cmdWake implements `mate wake <project> [--peek] [--workspace <dir>]` (CON-68): the
// one read a Mate makes when a digest or a `resolve:` line wakes it. On
// hellovietnam (2026-10-06..14) a Mate woke 247 times and spent 6.9 model
// calls per wake, most of them a chain of tail, cat, peek, crew list and
// python run to learn what had happened; each call re-read its ~65k token
// prefix. This prints that in one output:
//
//  1. every open item, in the order the Mate acts on them (blocked,
//     needs-decision, wait-mate - the wake skill's order), each with what
//     deciding it needs: a question in full, a hand-back's branch summary,
//     its `## For the next crew` section and its reviews;
//  2. the status lines written since the Mate's last wake;
//  3. each open Crew's state;
//  4. a repo whose harness friction log is over budget, and the news that
//     asks the captain about its review (CON-200), when there is one.
//
// It reads the same view the digest is built from (internal/box), so a
// wake never disagrees with the digest that caused it. Part 2's cursor is
// its own (`mate/.wake-cursor`), moved by every run but a --peek, so a
// captain who looks at a wake does not take its lines from the Mate's next
// one, nor opens part 4's news; part 1 has none, so an item stays listed until it is dealt with,
// whatever was shown before. It asks Herdr nothing: a pane's health is `mate state`'s, and `mate peek`
// is still how a Mate looks at a blocked Crew.
//
// internal/wake builds the text (CON-169); this reads the flags, hands it
// git and part 4's frictionCheck, and prints what it returns.
func cmdWake(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("wake", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: mate wake <project> [--peek] [--workspace <dir>]")
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	peek := fs.Bool("peek", false, "print the wake without moving the cursor of part 2 or opening part 4's news")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return newUsageError("mate wake: want exactly 1 argument: <project>")
	}
	w, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return err
	}
	deps := wake.Deps{Git: gitx.New(), Friction: frictionCheck}
	text, err := wake.Render(context.Background(), w, deps, fs.Arg(0), time.Now(), !*peek)
	if err != nil {
		return err
	}
	_, err = io.WriteString(stdout, text)
	return err
}
