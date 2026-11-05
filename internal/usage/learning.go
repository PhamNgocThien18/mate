package usage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nguyenngocanh94/mate/internal/dashboard"
	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// LearningHeading is the section of a crew's learning record
// (store.CrewLearning) that the app owns: the crew's usage as the ledger
// has it at the moment the crew closes. The Mate writes the rest of the
// record (skill task-learning); this section it only reads.
const LearningHeading = "## " + store.LearningUsageSection

// RecordAtClose writes the crew's usage into its learning record when the
// crew closes - `crew stop`, `mate merge`, `mate land`, the console's stop
// and merge, the budget's hard-ceiling stop (CON-130). On mihuv2
// (2026-10-27/28) the skill asked the Mate to run `usage --why` once per
// task and 7 of 13 records had no numbers; a record without numbers cannot
// be compared with anything later. So the app writes them, in the same
// words `usage --why` prints, and the Mate's part of the record stays its
// own: an existing record keeps every other section, a missing one is
// started with the fields task-learning fills. A timeline that cannot be
// read is written as unknown rather than skipped, so the record says what
// is missing. The outcome is one line for the caller; it never fails the
// close.
func RecordAtClose(ctx context.Context, w *store.Workspace, project, crew, event string) string {
	path := w.CrewLearning(project, crew)
	block, err := usageBlock(ctx, w, project, crew, event)
	word := "written"
	if err != nil {
		block = fmt.Sprintf("unknown: %s\nmeasured at %s %s; `mate usage %s %s --why` once the timeline is readable\n", oneLineMsg(err), event, time.Now().UTC().Format(time.RFC3339), project, crew)
		word = "unknown (" + oneLineMsg(err) + ")"
	}
	if err := UpsertLearningSection(path, crew, LearningHeading, block); err != nil {
		return fmt.Sprintf("learning: usage not written: %v", err)
	}
	return fmt.Sprintf("learning: usage %s in %s", word, path)
}

// usageBlock is the head of `usage --why` for the record: the run, token
// buckets, call shape and timing lanes, with the scope every number has.
// CrewPerformance reads only Deps.Now of the dashboard's dependencies, so
// the zero Deps (the wall clock) is all it needs.
func usageBlock(ctx context.Context, w *store.Workspace, project, crew, event string) (string, error) {
	handle, err := db.OpenRead(w)
	if err != nil {
		return "", err
	}
	defer handle.Close()
	server, err := dashboard.New(dashboard.Options{Workspace: w, DB: handle, Deps: dashboard.Deps{}})
	if err != nil {
		return "", err
	}
	ledger, turns, perf, err := server.CrewPerformance(ctx, project, crew)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "scope: crew %s and its fresh sessions; reviewers are separate crews · measured at %s %s; a later reindex may add calls recorded before it\n",
		crew, event, time.Now().UTC().Format(time.RFC3339))
	WriteWhyHead(&b, ledger, turns, perf)
	return b.String(), nil
}

// UpsertLearningSection replaces the `heading` section of the record at
// path, or adds it, keeping every other line. A record that does not exist
// yet is started with the fields task-learning fills, so the Mate finds a
// file to complete rather than a bare section.
func UpsertLearningSection(path, crew, heading, body string) error {
	body = strings.TrimRight(body, "\n") + "\n"
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		text := fmt.Sprintf("# %s learning\nSource: (task-learning fills this: task, revision, reviews, observation time)\nLearning: pending\n\n%s\n%s", crew, heading, body)
		return os.WriteFile(path, []byte(text), 0o644)
	}
	lines := strings.Split(string(data), "\n")
	start, end := -1, len(lines)
	for i, l := range lines {
		if start < 0 {
			if strings.TrimSpace(l) == heading {
				start = i
			}
			continue
		}
		if strings.HasPrefix(l, "## ") || strings.HasPrefix(l, "# ") {
			end = i
			break
		}
	}
	section := heading + "\n" + body
	var text string
	if start < 0 {
		text = strings.TrimRight(string(data), "\n") + "\n\n" + section
	} else {
		before := strings.Join(lines[:start], "\n")
		after := strings.Join(lines[end:], "\n")
		text = before + "\n" + section
		if strings.TrimSpace(after) != "" {
			text += "\n" + after
		}
	}
	return os.WriteFile(path, []byte(strings.TrimRight(text, "\n")+"\n"), 0o644)
}
