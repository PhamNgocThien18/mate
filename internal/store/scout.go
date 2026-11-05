package store

import (
	"os"

	"github.com/nguyenngocanh94/mate/internal/brief"
)

// MetaKind is the crew `.meta` key holding the task's shape, "ship" or
// "scout" (brief.Kind). A crew spawned before it was recorded has none;
// CrewIsScout reads its brief.
const MetaKind = "kind"

// CrewIsScout reports whether a crew's task is a scout: its meta's kind,
// or, for a crew spawned before kind was recorded, the app's copy of its
// brief carrying a `## Deliverable`, which only a scout brief may have.
// A meta without kind and a brief that cannot be read is a ship.
func (w *Workspace) CrewIsScout(project, crew string, meta map[string]string) bool {
	switch meta[MetaKind] {
	case brief.Scout.String():
		return true
	case brief.Ship.String():
		return false
	}
	raw, err := os.ReadFile(w.CrewBrief(project, crew))
	if err != nil {
		return false
	}
	_, found := brief.SectionText(string(raw), brief.Deliverable)
	return found
}
