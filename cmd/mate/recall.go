package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/nguyenngocanh94/mate/internal/recall"
)

// cmdRecall implements `mate recall <project> [--live] [--part N[,N...]] [--max-bytes N]`
// (docs/mvp.md M8 task 37, B1): the one ordered digest a Mate reads at a
// session start, in the order of docs/research/firstmate-memory-2026-09-24.md
// §12.5 - live state first, memory last - so a digest cut short loses the
// most stable, most recoverable part and never the crews.
//
// It is the read half of firstmate's bin/fm-session-start.sh and nothing
// else: it reads Mate state and git metadata. External task trackers are
// read by the agent according to the captain's own instructions.
//
// internal/recall builds the digest (CON-167); this reads the flags, hands
// it recallDeps and this binary's path, and prints what it returns.
func cmdRecall(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("recall", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: mate recall <project> [--live] [--part <n>[,<n>...]] [--max-bytes <n>] [--workspace <dir>]")
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	liveFlag := fs.Bool("live", false, "print only part 1, the live state")
	partFlag := fs.String("part", "", "print only these parts, by number, e.g. 4 or 3,5")
	maxFlag := fs.Int("max-bytes", 0, "cut the digest from the bottom to at most this many bytes; part 1 always survives whole (0: no limit)")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return newUsageError("mate recall: want exactly 1 argument: <project>")
	}
	if *maxFlag < 0 {
		return newUsageError("mate recall: --max-bytes must not be negative")
	}
	parts, err := recall.ParseParts(*partFlag)
	if err != nil {
		return &usageError{err}
	}
	if len(parts) > 0 && *liveFlag {
		return newUsageError("mate recall: --live is --part 1; pass one of them")
	}
	w, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return err
	}
	text, err := recall.Render(context.Background(), w, recallDeps(), fs.Arg(0), time.Now(), recall.Options{
		LiveOnly: *liveFlag,
		Parts:    parts,
		MaxBytes: *maxFlag,
		Binary:   selfBinary(),
	})
	var noPart *recall.NoPartError
	if errors.As(err, &noPart) {
		return &usageError{err}
	}
	if err != nil {
		return err
	}
	_, err = io.WriteString(stdout, text)
	return err
}
