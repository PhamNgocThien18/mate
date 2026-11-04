package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ErrProjectExists is returned by AddProject for a name already registered.
var ErrProjectExists = errors.New("store: project already registered")

// ErrNoProject is returned for a project that is not registered.
var ErrNoProject = errors.New("store: no such project")

// ProjectConfig is `projects/<name>/project.yaml`.
// MateConfig pins the coordinator profile independently of the captain's CLI.
type MateConfig struct {
	Model  string `yaml:"model,omitempty"`
	Effort string `yaml:"effort,omitempty"`
	// RefreshContext defaults to 150000; -1 disables automatic refresh.
	RefreshContext int64 `yaml:"refresh_context,omitempty"`
	// Mode is the Mate's mode when a start names none and the last run
	// recorded none: `headless` (CON-78, the default for a harness with
	// headless turns) or `pane`, the harness's own TUI.
	Mode string `yaml:"mode,omitempty"`
	// Headless is the first spelling of Mode: headless.
	Headless bool `yaml:"headless,omitempty"`
}

type ProjectConfig struct {
	Mate MateConfig `yaml:"mate,omitempty"`
	// Repos are the git repositories the project owns, zero or more
	// (docs/mvp.md M9). A crew works in exactly one of them.
	Repos []RepoConfig `yaml:"repos"`
	// Budget is the optional token/cost ceiling mvp.md M5 task 27 checks at
	// the end of every observer poll. A nil Budget (the field absent from
	// project.yaml) means no limit is configured, not a limit of zero:
	// BudgetConfig's own fields carry that same "unset means no limit"
	// convention one level down.
	Budget *BudgetConfig `yaml:"budget,omitempty"`
	// Review is whether each task's work is independently reviewed before
	// the Mate lands it (CON-101): ReviewOn or ReviewOff. Empty means the
	// captain has not decided, and the Mate asks before it lands one.
	Review string `yaml:"review,omitempty"`
	// NextWork is what the Mate does when a task lands and nothing the
	// captain asked for is left (CON-120): NextWorkPick, the default when
	// empty, takes the tracker's next ready task without asking;
	// NextWorkAsk asks the captain which work comes next.
	NextWork string `yaml:"next_work,omitempty"`
	// Workflow is the project's own process as the admin task page checks
	// it (docs/design/task-page.md, CON-148): the steps the harness asks a
	// crew to take, each with the evidence that proves it was taken. Empty
	// means the project declares none and the page draws no checklist.
	Workflow []WorkflowStep `yaml:"workflow,omitempty"`
}

// WorkflowStep is one step of ProjectConfig.Workflow. Every pattern is a
// Go regular expression; a step is done when any one of them matches:
// Command against a shell command the crew ran, Read against a path it
// read (a file tool's target, or a command naming the path), Skill against
// a skill it loaded, Handback against a line of its hand-back. Files, when
// set, says which changed files make the step apply at all: a crew whose
// diff matches none of them has the step as "n/a", not "missed". Since,
// when set, is the day the repo started asking for the step (2026-10-28,
// or an RFC 3339 time): a task spawned before it has the step as "n/a".
// Roles names the crews the step is asked of (WorkflowRoleShip,
// WorkflowRoleScout, WorkflowRoleReview); empty means ship crews only,
// because a workflow is the way a change is shipped. A crew of another
// role has the step as "n/a" (CON-201).
type WorkflowStep struct {
	Step     string   `yaml:"step"`
	Command  string   `yaml:"command,omitempty"`
	Read     string   `yaml:"read,omitempty"`
	Skill    string   `yaml:"skill,omitempty"`
	Handback string   `yaml:"handback,omitempty"`
	Files    string   `yaml:"files,omitempty"`
	Since    string   `yaml:"since,omitempty"`
	Roles    []string `yaml:"roles,omitempty"`
}

// The values of WorkflowStep.Roles, the dashboard's task roles.
const (
	WorkflowRoleShip   = "ship"
	WorkflowRoleScout  = "scout"
	WorkflowRoleReview = "review"
)

// AskedOf reports whether the step is asked of a crew of role.
func (s WorkflowStep) AskedOf(role string) bool {
	if len(s.Roles) == 0 {
		return role == WorkflowRoleShip
	}
	for _, r := range s.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// The values of ProjectConfig.Review.
const (
	ReviewOn  = "on"
	ReviewOff = "off"
)

// ValidReview reports whether s is a value project.yaml's review may hold.
func ValidReview(s string) bool { return s == ReviewOn || s == ReviewOff }

// The values of ProjectConfig.NextWork.
const (
	NextWorkPick = "pick"
	NextWorkAsk  = "ask"
)

// ValidNextWork reports whether s is a value project.yaml's next_work may hold.
func ValidNextWork(s string) bool { return s == NextWorkPick || s == NextWorkAsk }

// NextWorkMode is NextWork with its default filled in: an empty or unknown
// value picks, so a project the captain never configured keeps moving.
func (c ProjectConfig) NextWorkMode() string {
	if c.NextWork == NextWorkAsk {
		return NextWorkAsk
	}
	return NextWorkPick
}

// MetaBudget is the crew `.meta` key holding the crew's own token budget
// (CON-76): the dispatch rule's budget `mate crew spawn --budget` recorded,
// else a copy of the project's crew_tokens at spawn. The observer's budget
// check reads it before the project's block. It lives here, beside
// MetaRepo, because both the writer (internal/spawn) and the reader
// (internal/timeline) already import this package.
const MetaBudget = "budget"

// The crew `.meta` keys of CON-94's budget enforcement. budget_line is when
// the daemon typed the "budget reached" line into the crew's pane, so it is
// typed once per ceiling; budget_stop is what the daemon did at the hard
// ceiling, so it acts once. budget_extensions counts `mate crew budget
// --extend`, budget_reason is the last extension's reason, and
// budget_original the ceiling the crew was spawned with. An extension
// deletes budget_line and budget_stop, which re-arms both against the new
// ceiling.
const (
	MetaBudgetLine       = "budget_line"
	MetaBudgetStop       = "budget_stop"
	MetaBudgetExtensions = "budget_extensions"
	MetaBudgetReason     = "budget_reason"
	MetaBudgetOriginal   = "budget_original"
)

// MetaMateMode is the mate.meta key recording how the Mate runs, and
// MateModeHeadless its value for a headless host (CON-78). They live here
// because the outbox, stow and the console ask it as well as spawn.
const (
	MetaMateMode     = "mode"
	MateModeHeadless = "headless"
)

// MateHeadless reports whether the Mate of project runs headless now: its
// meta names a live agent and records the headless mode.
func (w *Workspace) MateHeadless(project string) bool {
	meta, err := w.ReadMateMeta(project)
	return err == nil && meta[MetaMateMode] == MateModeHeadless && strings.TrimSpace(meta["agent"]) != ""
}

// MetaFollowups is the crew `.meta` key `mate merge --followups` writes
// (CON-74): where the findings a passing review left standing were
// recorded before the merge - a `backlog:<text>` line under the project's
// `## Queued`, or a tracker issue id such as `HVN-12`. It is the record a
// reader of the closed crew (`mate usage`, the Office) can show for "what
// happened to the reviewer's findings".
const MetaFollowups = "followups"

// BudgetConfig is `project.yaml`'s `budget:` block. Every field is optional
// and a zero value means "no limit on this dimension" - hand-editing the
// file to add one line is the whole interface for the MVP; `project add
// --budget-usd` is the one flag that also writes it, for the caller who
// wants a project-wide ceiling at creation time.
//
// Crossing a limit opens an incident (`budget`, mvp.md section 4b) on the
// crew (for the two crew-scoped fields) or on the project's Mate (for
// ProjectUSD); per the 2026-09-20 decision it never makes a crew `blocked`
// and it is never resolved, because going over budget is a fact about the
// task's past spend, not a condition that clears.
type BudgetConfig struct {
	// CrewTokens is the total token ceiling (all four buckets summed) for
	// one crew's whole task.
	CrewTokens int64 `yaml:"crew_tokens,omitempty"`
	// CrewUSD is the cost ceiling for one crew's whole task, using
	// `pricing.yaml`. A crew whose model has no price never crosses this,
	// because its cost is unknown rather than zero.
	CrewUSD float64 `yaml:"crew_usd,omitempty"`
	// ProjectUSD is the cost ceiling across every crew this project has
	// ever spawned, checked against the Mate rather than any one crew.
	ProjectUSD float64 `yaml:"project_usd,omitempty"`
}

// AddProject registers a project: it normalises its repos (possibly none),
// refuses a repo another project owns, creates `projects/<name>/` with the
// `mate/` and `crews/` directories, writes project.yaml and appends the
// project to workspace.yaml. Before any of that is written it makes the
// project's own directory, ProjectHome, if it is not there yet: the user's
// directory, where its repos live, kept as it is when it exists.
func (w *Workspace) AddProject(name string, cfg ProjectConfig) error {
	if err := ValidateProjectName(name); err != nil {
		return err
	}
	// cfgMu is held from the check that the name is free to the save that
	// registers it, so no LoadConfig or other change of this handle lands
	// in between (CON-204).
	w.cfgMu.Lock()
	defer w.cfgMu.Unlock()
	if _, ok := w.Project(name); ok {
		return fmt.Errorf("%w: %s", ErrProjectExists, name)
	}
	normalised, err := w.normaliseProject(name, cfg)
	if err != nil {
		return err
	}
	if err := w.checkReposUnclaimed(name, normalised.Repos); err != nil {
		return err
	}
	for _, dir := range []string{w.ProjectHome(name), w.ProjectDir(name), w.MateDir(name), w.CrewsDir(name)} {
		if err := w.mkdirAll(dir); err != nil {
			return err
		}
	}
	if err := w.SaveProject(name, normalised); err != nil {
		return err
	}
	if err := w.seedOnce(w.ProjectCrewDoc(name), projectCrewDocSeed(name)); err != nil {
		return err
	}
	next := w.config().clone()
	next.Projects = append(next.Projects, ProjectRef{Name: name})
	return w.saveConfigLocked(next)
}

// RemoveProject drops a project from workspace.yaml. The state directory under
// `projects/` is left on disk: like `crews/<id>/`, it is history and only the
// user deletes it.
func (w *Workspace) RemoveProject(name string) error {
	return w.changeConfig(func(cfg *WorkspaceConfig) error {
		for i, p := range cfg.Projects {
			if p.Name == name {
				cfg.Projects = append(cfg.Projects[:i:i], cfg.Projects[i+1:]...)
				return nil
			}
		}
		return fmt.Errorf("%w: %s", ErrNoProject, name)
	})
}

// LoadProject reads `projects/<name>/project.yaml`.
func (w *Workspace) LoadProject(name string) (ProjectConfig, error) {
	if err := ValidateProjectName(name); err != nil {
		return ProjectConfig{}, err
	}
	path := w.ProjectFile(name)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ProjectConfig{}, fmt.Errorf("%w: %s", ErrNoProject, name)
		}
		return ProjectConfig{}, err
	}
	var file projectFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return ProjectConfig{}, fmt.Errorf("store: %s: %w", path, err)
	}
	cfg := file.ProjectConfig
	if file.LegacyRepo != "" && len(cfg.Repos) == 0 {
		cfg.Repos = []RepoConfig{{
			Name: DefaultRepoName(file.LegacyRepo), Path: file.LegacyRepo, DefaultBranch: file.LegacyDefaultBranch,
		}}
	}
	for i := range cfg.Repos {
		if cfg.Repos[i].DefaultBranch == "" {
			cfg.Repos[i].DefaultBranch = DefaultBranch
		}
	}
	return cfg, nil
}

// projectFile is project.yaml as read: the current shape plus the fields
// older files carry and SaveProject never writes again - the one-repo fields
// every project.yaml had before M9, which LoadProject turns into a one-entry
// Repos, and the `mode` and `yolo` that M19 removed (the Mate picks each
// crew's delivery and merges reviewed work itself), read and ignored.
type projectFile struct {
	ProjectConfig       `yaml:",inline"`
	LegacyRepo          string `yaml:"repo"`
	LegacyDefaultBranch string `yaml:"default_branch"`
	LegacyMode          string `yaml:"mode"`
	LegacyYolo          bool   `yaml:"yolo"`
}

// CrewIDs are the ids with a `.meta` under the project's `crews/`, sorted. A
// file whose name is not a valid crew id is ignored rather than reported:
// `crews/` is a directory the user can also put things in.
func (w *Workspace) CrewIDs(project string) ([]string, error) {
	if err := ValidateProjectName(project); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(w.CrewsDir(project))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".meta")
		if !ok || ValidateCrewID(id) != nil {
			continue
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

// SaveProject writes `projects/<name>/project.yaml` atomically.
func (w *Workspace) SaveProject(name string, cfg ProjectConfig) error {
	if err := ValidateProjectName(name); err != nil {
		return err
	}
	normalised, err := w.normaliseProject(name, cfg)
	if err != nil {
		return err
	}
	data, err := yaml.Marshal(normalised)
	if err != nil {
		return err
	}
	return w.writeFile(w.ProjectFile(name), data, 0o644)
}

// normaliseProject fills the defaults and checks the repos per
// normaliseRepos.
func (w *Workspace) normaliseProject(project string, cfg ProjectConfig) (ProjectConfig, error) {
	repos, err := w.normaliseRepos(project, cfg.Repos)
	if err != nil {
		return ProjectConfig{}, err
	}
	cfg.Repos = repos
	return cfg, nil
}

// RelRepo turns a repository path - absolute, or relative to the workspace
// root - into the path stored in the configuration: relative to the root,
// slash separated, and proven to be inside the workspace. The repository need
// not be a direct child of the root, but it must not be the root itself and
// must not live under `.mate/`.
func (w *Workspace) RelRepo(repo string) (string, error) {
	if repo == "" {
		return "", fmt.Errorf("store: repo path is empty")
	}
	abs := repo
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(w.root, abs)
	}
	resolved, err := w.resolve(abs)
	if err != nil {
		return "", err
	}
	if resolved == w.root {
		return "", fmt.Errorf("store: repo %s is the workspace root", repo)
	}
	if within(w.StateDir(), resolved) {
		return "", fmt.Errorf("store: repo %s is inside %s", repo, StateDirName)
	}
	rel, err := filepath.Rel(w.root, resolved)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(rel), nil
}

// Auto reports whether the project is in auto mode: always, unless the
// captain holds it in manual with the console's `m` key (Held). Auto is the
// default (CON-115, docs/mvp.md M19): a new project's Mate hears its crews
// from the first hand-back, with no key to press first. `mate/.auto` is
// still written by SetMode, for readers that date the switch by its mtime,
// but it no longer decides; a project with no flag at all used to sit in
// manual with no way out but the `m` key (signal, 2026-10-20).
func (w *Workspace) Auto(project string) bool {
	if err := ValidateProjectName(project); err != nil {
		return false
	}
	return !w.Held(project)
}

// Held reports whether the captain holds the project in manual mode, which
// is the presence of `mate/.manual`.
func (w *Workspace) Held(project string) bool {
	if err := ValidateProjectName(project); err != nil {
		return false
	}
	_, err := os.Stat(w.ManualHold(project))
	return err == nil
}

// SetMode is the captain's own choice of mode, the console's `m` key: auto
// turns `.auto` on and releases the hold; manual turns it off and holds it
// off, so the captain's choice is recorded. The auto daemon releases the hold
// through here too, once the captain has been away for QuietAfter. The hold
// is released before auto goes on and set before it goes off, so no reader
// between the two writes sees a state the captain did not choose.
func (w *Workspace) SetMode(project string, auto bool) error {
	if err := ValidateProjectName(project); err != nil {
		return err
	}
	if auto {
		if err := w.removeFlag(w.ManualHold(project)); err != nil {
			return err
		}
		return w.SetAuto(project, true)
	}
	if err := w.writeFile(w.ManualHold(project), nil, 0o644); err != nil {
		return err
	}
	return w.SetAuto(project, false)
}

func (w *Workspace) removeFlag(path string) error {
	if _, err := w.resolve(path); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// SetAuto creates or removes `mate/.auto`. It records the mode, it does not
// decide it: only the hold does (Auto). Choosing a mode is SetMode.
func (w *Workspace) SetAuto(project string, on bool) error {
	if err := ValidateProjectName(project); err != nil {
		return err
	}
	path := w.AutoFlag(project)
	if !on {
		return w.removeFlag(path)
	}
	return w.writeFile(path, nil, 0o644)
}
