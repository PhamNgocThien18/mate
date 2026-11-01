package dashboard

import (
	"context"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/nguyenngocanh94/mate/internal/brief"
	"github.com/nguyenngocanh94/mate/internal/diagnostics"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// Journey is the `journey` field of GET /api/projects/{p}/tasks/{crew}
// (docs/design/task-page.md): what the task page's Journey tab needs that
// the ledger, the turns and the performance projection do not carry. Every
// part is read from one file or one query and says in Missing when it
// could not be read, so the page draws "not recorded" rather than zero.
type Journey struct {
	// Brief is the Mate's brief: the captain's own words and the `# Task`
	// section, not the role and rules boilerplate. nil when there is none.
	Brief *JourneyBrief `json:"brief"`
	// Handback is the crew's hand-back, verbatim and parsed. nil when the
	// crew has not handed back.
	Handback *JourneyHandback `json:"handback"`
	// Meta is the crew's `.meta`, the keys the page reads: pr_url,
	// pr_state, pr_sync, merge_commit, lane, size, teardown, cleanup,
	// launched_at, started_at, stopped_at, state, worktree.
	Meta map[string]string `json:"meta"`
	// Reviews are the reviewer crews of this task with their verdicts.
	Reviews []JourneyReview `json:"reviews"`
	// MateTurns are the Mate's exchanges that touched this crew: the one
	// that spawned it, the ones its digests woke, the one that closed it.
	// Each is the whole exchange; what else the Mate did in it is not
	// separated out, and Activities says what it did.
	MateTurns []JourneyMateTurn `json:"mate_turns"`
	// Incidents are the observer's lines about this crew in incidents.log.
	Incidents []JourneyIncident `json:"incidents"`
	// Workflow is the project's declared workflow (project.yaml
	// `workflow`) checked against what the crew ran, read and loaded;
	// nil when the project declares none.
	Workflow []WorkflowCheck `json:"workflow"`
	// ChangedFiles are the files the crew's work touched, from its branch
	// or its merge commit; nil when neither could be read.
	ChangedFiles []string `json:"changed_files"`
	// Missing names, in words, what this answer could not read.
	Missing []string `json:"missing"`
}

type JourneyBrief struct {
	Path         string `json:"path"`
	Bytes        int64  `json:"bytes"`
	CaptainWords string `json:"captain_words"`
	Task         string `json:"task"`
}

type JourneyHandback struct {
	Path     string `json:"path"`
	Bytes    int64  `json:"bytes"`
	Markdown string `json:"markdown"`
	// Closes are the tracker lines at the top ("Closes HU-246", "Part of
	// HU-228"), verbatim.
	Closes     []string            `json:"closes"`
	Criteria   []HandbackCriterion `json:"criteria"`
	Deviations []string            `json:"deviations"`
	StillOpen  []string            `json:"still_open"`
	// Learning is the text after "Learning:" when the crew wrote one.
	Learning string `json:"learning,omitempty"`
}

type HandbackCriterion struct {
	Name    string `json:"name"`
	Result  string `json:"result"`
	Pass    bool   `json:"pass"`
	Command string `json:"command,omitempty"`
	Output  string `json:"output,omitempty"`
}

type JourneyReview struct {
	Crew    string `json:"crew"`
	Verdict string `json:"verdict"`
}

type JourneyMateTurn struct {
	ID         string         `json:"id"`
	Source     string         `json:"source"`
	Prompt     string         `json:"prompt"`
	PromptAt   string         `json:"prompt_at"`
	Response   string         `json:"response"`
	StartedAt  string         `json:"started_at"`
	EndedAt    string         `json:"ended_at"`
	DurationMs *int64         `json:"duration_ms"`
	ModelCalls int64          `json:"model_calls"`
	ToolCalls  int64          `json:"tool_calls"`
	Model      string         `json:"model,omitempty"`
	Tokens     Tokens         `json:"tokens"`
	Activities []MateActivity `json:"activities"`
}

type JourneyIncident struct {
	At    string `json:"at"`
	Kind  string `json:"kind"`
	State string `json:"state"`
	Text  string `json:"text"`
}

// WorkflowCheck is one declared step and whether the crew took it.
type WorkflowCheck struct {
	Step string `json:"step"`
	// State is "done", "missed", "n/a" (the step's `files` matched nothing
	// the crew changed) or "unknown" (the pattern is bad, or `files` could
	// not be checked because the changed files are unknown).
	State string `json:"state"`
	// Evidence is the command, path, skill or hand-back line that matched,
	// or why the state is not known.
	Evidence string `json:"evidence,omitempty"`
	At       string `json:"at,omitempty"`
}

const (
	WorkflowDone    = "done"
	WorkflowMissed  = "missed"
	WorkflowNA      = "n/a"
	WorkflowUnknown = "unknown"
)

// journey reads everything the Journey tab needs beyond the task body.
func (s *Server) journey(ctx context.Context, project, crew string, ledger Task, performance diagnostics.Performance, branch Branch, env envelope) Journey {
	out := Journey{Reviews: []JourneyReview{}, MateTurns: []JourneyMateTurn{}, Incidents: []JourneyIncident{}, Missing: []string{}}
	missing := func(what string) { out.Missing = append(out.Missing, what) }

	if data, err := os.ReadFile(s.ws.CrewBrief(project, crew)); err == nil {
		words, _ := brief.SectionText(string(data), brief.CaptainsWords)
		task, _ := brief.ExtractTask(string(data))
		out.Brief = &JourneyBrief{Path: s.ws.CrewBrief(project, crew), Bytes: int64(len(data)), CaptainWords: words, Task: strings.TrimSpace(task)}
	} else {
		missing("brief")
	}

	var handback string
	if data, err := os.ReadFile(s.ws.CrewHandback(project, crew)); err == nil {
		handback = string(data)
		hb := parseHandback(handback)
		hb.Path, hb.Bytes = s.ws.CrewHandback(project, crew), int64(len(data))
		out.Handback = &hb
	} else {
		missing("hand-back")
	}

	meta, err := s.ws.ReadCrewMeta(project, crew)
	if err != nil || len(meta) == 0 {
		missing("meta")
	}
	out.Meta = journeyMeta(meta)

	for _, reviewer := range ledger.Reviews {
		out.Reviews = append(out.Reviews, JourneyReview{Crew: reviewer, Verdict: s.reviewVerdict(project, reviewer)})
	}

	if turns, err := s.mateTurnsFor(ctx, project, crew, env.LastEventID); err == nil {
		out.MateTurns = turns
	} else {
		missing("Mate's turns: " + err.Error())
	}

	if lines, _, err := s.ws.ReadIncidents(project, 0); err == nil {
		for _, l := range lines {
			if l.Crew != crew {
				continue
			}
			out.Incidents = append(out.Incidents, JourneyIncident{At: l.Time.Format("2006-01-02T15:04:05.000000000Z07:00"), Kind: l.Kind, State: l.State, Text: l.Text})
		}
	} else if !os.IsNotExist(err) {
		missing("incidents")
	}

	cfg, cfgErr := s.ws.LoadProject(project)
	if cfgErr != nil {
		missing("project.yaml")
	}
	if s.deps.ChangedFiles != nil && len(meta) > 0 {
		if files, err := s.deps.ChangedFiles(ctx, project, crew, branch.Exists, meta["merge_commit"]); err == nil {
			out.ChangedFiles = files
		} else {
			missing("changed files: " + err.Error())
		}
	} else {
		missing("changed files")
	}
	if cfgErr == nil && len(cfg.Workflow) > 0 {
		out.Workflow = checkWorkflow(cfg.Workflow, performance, handback, out.ChangedFiles)
	}
	return out
}

var journeyMetaKeys = []string{"pr_url", "pr_state", "pr_sync", "merge_commit", "lane", "size", "teardown", "cleanup",
	"launched_at", "started_at", "stopped_at", "state", "worktree", "repo"}

func journeyMeta(meta map[string]string) map[string]string {
	out := map[string]string{}
	for _, key := range journeyMetaKeys {
		if v := meta[key]; v != "" {
			out[key] = v
		}
	}
	return out
}

// mateTurnsFor is the Mate's exchanges about one crew: those whose
// activities name it (spawned, closed, merged, answered) and those its own
// digests woke (the prompt opens with the crew's name). The Mate's
// exchanges are computed once per event id and shared across task pages,
// because building them reads the Mate's whole recording.
func (s *Server) mateTurnsFor(ctx context.Context, project, crew string, eventID int64) ([]JourneyMateTurn, error) {
	exchanges, err := s.mateExchangesCached(ctx, project, eventID)
	if err != nil {
		return nil, err
	}
	out := []JourneyMateTurn{}
	for _, x := range exchanges {
		if !exchangeTouches(x, crew) {
			continue
		}
		activities := x.Activities
		if activities == nil {
			activities = []MateActivity{}
		}
		out = append(out, JourneyMateTurn{
			ID: x.ID, Source: x.Source, Prompt: x.Prompt, PromptAt: x.PromptAt, Response: x.Response,
			StartedAt: x.StartedAt, EndedAt: x.EndedAt, DurationMs: x.DurationMs,
			ModelCalls: x.ModelCalls, ToolCalls: x.ToolCalls, Model: x.Model, Tokens: x.Tokens, Activities: activities,
		})
	}
	return out, nil
}

func exchangeTouches(x MateExchange, crew string) bool {
	for _, a := range x.Activities {
		if a.Crew == crew {
			return true
		}
	}
	return strings.HasPrefix(x.Prompt, crew+" ") || strings.HasPrefix(x.Prompt, crew+":")
}

type mateExchangesMemo struct {
	mu      sync.Mutex
	project string
	eventID int64
	value   []MateExchange
	err     error
}

func (s *Server) mateExchangesCached(ctx context.Context, project string, eventID int64) ([]MateExchange, error) {
	m := &s.mateMemo
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.project == project && m.eventID == eventID && eventID != 0 {
		return m.value, m.err
	}
	scenes, err := s.sceneRows(ctx, project)
	if err != nil {
		return nil, err
	}
	mate, err := s.mateBlock(ctx, project, scenes)
	if err != nil {
		return nil, err
	}
	value, err := s.mateExchanges(ctx, project, mate.Running)
	m.project, m.eventID, m.value, m.err = project, eventID, value, err
	return value, err
}

// parseHandback reads the hand-back the crew wrote (the brief's "Definition
// of done" shape): tracker lines at the top, a criteria table whose header
// starts with "criterion", `## Deviations …` and `## Still open` lists, and
// a "Learning:" line. Anything it does not recognise stays in Markdown.
func parseHandback(text string) JourneyHandback {
	hb := JourneyHandback{Markdown: text, Closes: []string{}, Criteria: []HandbackCriterion{}, Deviations: []string{}, StillOpen: []string{}}
	section := ""
	inTable := false
	for _, raw := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "## ") {
			section = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(line, "## ")))
			inTable = false
			continue
		}
		if strings.HasPrefix(line, "|") {
			cells := tableCells(line)
			if len(cells) >= 2 && strings.EqualFold(cells[0], "criterion") {
				inTable = true
				continue
			}
			if inTable && len(cells) >= 2 && strings.Trim(cells[0], "- ") == "" {
				continue // the |---|---| rule
			}
			if inTable && len(cells) >= 2 {
				c := HandbackCriterion{Name: cells[0], Result: cells[1], Pass: strings.EqualFold(strings.TrimSpace(cells[1]), "pass")}
				if len(cells) > 2 {
					c.Command = cells[2]
				}
				if len(cells) > 3 {
					c.Output = cells[3]
				}
				hb.Criteria = append(hb.Criteria, c)
			}
			continue
		}
		inTable = false
		if m := closesLine.FindStringSubmatch(line); m != nil && section == "" {
			hb.Closes = append(hb.Closes, line)
			continue
		}
		if i := strings.Index(line, "Learning:"); i >= 0 && hb.Learning == "" {
			hb.Learning = strings.TrimSpace(line[i+len("Learning:"):])
		}
		item, isItem := strings.CutPrefix(line, "- ")
		if !isItem {
			item, isItem = strings.CutPrefix(line, "* ")
		}
		if !isItem {
			continue
		}
		switch {
		case strings.HasPrefix(section, "deviation"):
			hb.Deviations = append(hb.Deviations, item)
		case strings.HasPrefix(section, "still open"):
			hb.StillOpen = append(hb.StillOpen, item)
		}
	}
	return hb
}

var closesLine = regexp.MustCompile(`^(?i)(closes|part of|fixes|resolves)\s+\S`)

func tableCells(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")
	parts := strings.Split(line, "|")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.TrimSpace(p))
	}
	return out
}

// checkWorkflow ticks each declared step from the recording: `command`
// against the commands the crew ran, `read` against the paths it read (a
// file tool's target or a shell command naming the path), `skill` against
// the skills it loaded, `handback` against the hand-back's lines. A step
// with `files` applies only when the crew's changed files match it: n/a
// when they do not, unknown when they could not be read and the step was
// not taken anyway. A bad pattern is unknown and says so.
func checkWorkflow(steps []store.WorkflowStep, p diagnostics.Performance, handback string, changed []string) []WorkflowCheck {
	out := make([]WorkflowCheck, 0, len(steps))
	execs := make([]diagnostics.Execution, 0, len(p.Executions))
	for _, e := range p.Executions {
		if e.IsWrapper || e.Poll {
			continue
		}
		execs = append(execs, e)
	}
	sort.SliceStable(execs, func(i, j int) bool { return execs[i].StartedAt < execs[j].StartedAt })
	for _, step := range steps {
		check := WorkflowCheck{Step: step.Step, State: WorkflowMissed}
		patterns := []struct{ field, pattern string }{{"command", step.Command}, {"read", step.Read}, {"skill", step.Skill}, {"handback", step.Handback}, {"files", step.Files}}
		bad := ""
		compiled := map[string]*regexp.Regexp{}
		for _, pt := range patterns {
			if pt.pattern == "" {
				continue
			}
			re, err := regexp.Compile(pt.pattern)
			if err != nil {
				bad = pt.field + ": " + err.Error()
				break
			}
			compiled[pt.field] = re
		}
		if bad != "" {
			check.State, check.Evidence = WorkflowUnknown, "bad pattern · "+bad
			out = append(out, check)
			continue
		}
		if re := compiled["command"]; re != nil {
			for _, e := range execs {
				if loc := re.FindStringIndex(e.Command); loc != nil {
					check.State, check.Evidence, check.At = WorkflowDone, excerptAround(e.Command, loc[0]), e.StartedAt
					break
				}
			}
		}
		if re := compiled["read"]; re != nil && check.State != WorkflowDone {
			for _, e := range execs {
				if e.Target != "" && re.MatchString(e.Target) {
					check.State, check.Evidence, check.At = WorkflowDone, e.Target, e.StartedAt
					break
				}
				if loc := re.FindStringIndex(e.Command); loc != nil {
					check.State, check.Evidence, check.At = WorkflowDone, excerptAround(e.Command, loc[0]), e.StartedAt
					break
				}
			}
		}
		if re := compiled["skill"]; re != nil && check.State != WorkflowDone {
			for _, skill := range p.Skills {
				if re.MatchString(skill.Name) {
					check.State, check.Evidence = WorkflowDone, "skill "+skill.Name
					if len(skill.Loads) > 0 {
						check.At = skill.Loads[0].At
					}
					break
				}
			}
		}
		if re := compiled["handback"]; re != nil && check.State != WorkflowDone && handback != "" {
			for _, line := range strings.Split(handback, "\n") {
				if re.MatchString(line) {
					check.State, check.Evidence = WorkflowDone, excerptLine(strings.TrimSpace(line))
					break
				}
			}
		}
		if re := compiled["files"]; re != nil {
			switch {
			case changed == nil && check.State != WorkflowDone:
				check.State, check.Evidence = WorkflowUnknown, "the files the crew changed could not be read, so whether this step applies is unknown"
			case changed != nil:
				applies := false
				for _, f := range changed {
					if re.MatchString(f) {
						applies = true
						break
					}
				}
				if !applies && check.State != WorkflowDone {
					check.State, check.Evidence = WorkflowNA, "no changed file matches "+step.Files
				}
			}
		}
		out = append(out, check)
	}
	return out
}

// excerptAround is the piece of a command around the match: a long shell
// line is read where it matched, not from its `cd` at the start.
func excerptAround(s string, at int) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	if len(s) <= 160 {
		return s
	}
	if at > len(s) {
		at = len(s)
	}
	start := at - 40
	if start < 0 {
		start = 0
	}
	for start > 0 && start < len(s) && !strings.ContainsRune(" ;|&", rune(s[start-1])) {
		start--
	}
	end := start + 157
	if end >= len(s) {
		end = len(s)
	}
	out := s[start:end]
	if start > 0 {
		out = "…" + out
	}
	if end < len(s) {
		out += "…"
	}
	return out
}

func excerptLine(s string) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	if len(s) > 160 {
		return s[:157] + "…"
	}
	return s
}
