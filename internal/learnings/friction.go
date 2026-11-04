package learnings

import (
	"regexp"
	"strconv"
	"strings"
)

// The harness kit's files a repo's friction count reads (CON-200): the
// friction log, and the review workflow whose Budgets table says when the
// log is overdue for `/harness-review`.
const (
	FrictionPath = "docs/harness/friction.md"
	ReviewPath   = "docs/harness/workflows/review.md"
)

// Friction is what CountFriction found in a friction log: its full
// entries, how many of them are still open, and the file's line count
// (`wc -l`, as the kit's mechanical trigger counts it).
type Friction struct {
	Entries int
	Open    int
	Lines   int
}

// FrictionBudget is the most open entries and lines a friction log may
// hold before a review is due.
type FrictionBudget struct {
	Open  int
	Lines int
}

// DefaultFrictionBudget is the kit's default, for a repo whose review.md
// is missing or states none.
var DefaultFrictionBudget = FrictionBudget{Open: 5, Lines: 120}

// Over reports whether f crosses b on either count.
func (f Friction) Over(b FrictionBudget) bool { return f.Open > b.Open || f.Lines > b.Lines }

var frictionEntry = regexp.MustCompile(`^## \d{4}-\d{2}-\d{2}\b`)

// CountFriction counts a friction log's entries by their heading,
// `## YYYY-MM-DD — title`, wherever they stand. The kit's protocol says
// "newest at the bottom" while its `## Archive` section is the file's
// last, so on mihuv2 (2026-11-04) crews had appended 29 entries below the
// archive: counting by position would miss them. An entry is open while
// its `Outcome:` says `open` (`open — recurred ...` included) or it has
// none. The format's example in a code fence, and the archive's one-line
// `- YYYY-MM-DD — ...` items, are not entries.
func CountFriction(text string) Friction {
	f := Friction{Lines: strings.Count(text, "\n")}
	in, fenced, closed := false, false, false
	end := func() {
		if in {
			f.Entries++
			if !closed {
				f.Open++
			}
		}
		in, closed = false, false
	}
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "```") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		if strings.HasPrefix(line, "## ") {
			end()
			in = frictionEntry.MatchString(line)
			continue
		}
		if in && strings.HasPrefix(line, "Outcome:") {
			closed = !strings.HasPrefix(strings.TrimSpace(strings.TrimPrefix(line, "Outcome:")), "open")
		}
	}
	end()
	return f
}

var firstNumber = regexp.MustCompile(`\d+`)

// ParseFrictionBudget reads the budgets from review.md's Budgets table,
// `| Open friction entries | ≤ 5 | ...` and `| Live friction log | ≤ ~120
// lines | ...`: a repo may have changed them at onboarding. A row that is
// missing or states no number keeps the default.
func ParseFrictionBudget(review string) FrictionBudget {
	b := DefaultFrictionBudget
	for _, line := range strings.Split(review, "\n") {
		cells := strings.Split(strings.TrimSpace(line), "|")
		if len(cells) < 4 || cells[0] != "" {
			continue
		}
		n, err := strconv.Atoi(firstNumber.FindString(cells[2]))
		if err != nil {
			continue
		}
		switch what := strings.ToLower(strings.TrimSpace(cells[1])); {
		case strings.HasPrefix(what, "open friction"):
			b.Open = n
		case strings.HasPrefix(what, "live friction"):
			b.Lines = n
		}
	}
	return b
}
