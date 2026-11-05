package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/nguyenngocanh94/mate/internal/box"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// A ship crew that has handed back - `pr-open`, or `wait-mate` for a local
// ship - is resting (CON-73). The Mate's next message to it is a fix
// round, and a fix round in the old session runs on everything that
// session has read: on hellovietnam that was 250-434k of context per call
// and two to three times the tokens the first round cost. So `mate send`
// and `mate brief append` open a fresh session for a resting crew instead
// of typing into its pane: spawn.RelaunchCrew with a spawn.FreshSession,
// whose opening input is the brief, the last hand-back, the message and
// the diff since the last review. `--same-session` is the captain's way
// to type into the old pane anyway.

// restingCrew reports whether the crew is resting: a ship whose latest
// status line hands the work back and was written in its current session
// (at or past the status offset the last fresh relaunch recorded, so a
// message right after a relaunch reaches the new pane rather than opening
// a third session). It returns the verb that line was written with. A
// scout never rests: its hand-back is its report, and a message to it is a
// follow-up question, not a fix round.
func restingCrew(w *store.Workspace, project, crew string, meta map[string]string) (resting bool, verb string, err error) {
	if w.CrewIsScout(project, crew, meta) {
		return false, "", nil
	}
	entries, _, err := w.ReadStatus(project, crew, 0)
	if err != nil {
		return false, "", err
	}
	since, _ := strconv.ParseInt(strings.TrimSpace(meta[spawn.MetaSessionStatusOffset]), 10, 64)
	for i := len(entries) - 1; i >= 0; i-- {
		if strings.TrimSpace(entries[i].Line) == "" {
			continue
		}
		st := box.ParseStatus(entries[i].Line)
		if st.State == box.StateUnknown {
			continue
		}
		if st.State != box.StateWaitMate || entries[i].Offset < since {
			return false, "", nil
		}
		verb = st.Verb
		if verb == "" {
			verb = string(box.StateWaitMate)
		}
		return true, verb, nil
	}
	return false, "", nil
}

// latestReviewedCommit is the commit the latest review of crew saw: the
// `head=` of the newest review record (`mate review` writes
// crews/<reviewer>/review.meta with `crew=` and `head=`) that names the
// crew. It is where a fresh session's diff starts; false when no review
// has run, and the caller falls back to the base branch.
func latestReviewedCommit(w *store.Workspace, project, crew string) (string, bool) {
	crews, err := spawn.ListCrews(w, project)
	if err != nil {
		return "", false
	}
	head := ""
	var newest int64 = -1
	for _, c := range crews {
		path := w.CrewReviewMeta(project, c.Crew)
		rec, err := store.ReadMeta(path)
		if err != nil || rec["crew"] != crew || rec["head"] == "" {
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if at := info.ModTime().UnixNano(); at > newest {
			newest, head = at, rec["head"]
		}
	}
	return head, head != ""
}

// alreadyTried is the part of the Mate's message from "already tried" to
// its end, one line, or "" when the message has none. It goes into the
// relaunch note, so the fresh session's first prompt says what not to
// repeat where a relaunch's "Progress so far" goes.
func alreadyTried(text string) string {
	lower := strings.ToLower(text)
	i := strings.Index(lower, "already tried")
	if i < 0 {
		return ""
	}
	return strings.Join(strings.Fields(text[i:]), " ")
}

// freshSession opens a fresh session for a resting crew with the message as
// its third part, and records the message in sent.log the way a typed line
// is, so the inbox and the digest see the crew answered. The relaunch's
// own refusals (a closed crew, a lost worktree) are the caller's error.
func freshSession(ctx context.Context, w *store.Workspace, deps spawn.Deps, project, crew, message, source string) (spawn.RelaunchResult, error) {
	reviewed, _ := latestReviewedCommit(w, project, crew)
	res, err := spawn.RelaunchCrew(ctx, w, deps, project, crew, alreadyTried(message),
		spawn.RelaunchOptions{Fresh: &spawn.FreshSession{Message: message, Reviewed: reviewed}})
	if err != nil {
		return res, err
	}
	if err := w.AppendSent(project, store.SentEntry{
		Source: source,
		Target: store.CrewTarget(crew),
		Text:   message,
	}); err != nil {
		return res, err
	}
	return res, nil
}

// freshSessionLine is the one line that says a fresh session was opened
// and why, printed before the relaunch report.
func freshSessionLine(project, crew, verb string, res spawn.RelaunchResult) string {
	return fmt.Sprintf("fresh session %d for %s/%s: it had handed back (%s), so the message opens a new session instead of typing into the old one",
		res.Ordinal, project, crew, verb)
}

// writeFreshSessionReport prints what a fresh session established: the
// line saying why, the relaunch facts, the input file, and the turn line a
// Mate needs. The input line is what a Mate reads to check what the crew
// was given; the status path is unchanged and already known.
func writeFreshSessionReport(stdout io.Writer, project, crew, verb, source string, res spawn.RelaunchResult) {
	fmt.Fprintln(stdout, freshSessionLine(project, crew, verb, res))
	old := "the resting agent was stopped"
	if !res.Stopped {
		old = "no live agent was recorded"
	}
	fmt.Fprintf(stdout, "relaunched %s/%s: agent %s in pane %s (harness %s%s, repo %s, branch %s, worktree %s; %s)\n",
		res.Project, res.Crew, res.Agent, res.Pane, res.Harness, relaunchProfileNote(res), res.Repo, res.Branch, res.Worktree, old)
	if res.DeliveryWarning != "" {
		fmt.Fprintf(stdout, "warning: %s\nlast pane lines:\n%s\n", res.DeliveryWarning, res.PaneTail)
	}
	fmt.Fprintf(stdout, "input %s\n", res.InputPath)
	if source == store.SourceMate {
		printTurnEnd(stdout, turnSendLine(crew))
	}
}
