package timeline

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nguyenngocanh94/mate/internal/crewstate"
	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/gitx"
	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// HealthReading is one observation of one agent's pane. It is the only input
// to this package that is not a file: nothing on disk records that a composer
// was busy at 10:32:41, and a busy stretch nobody explains is exactly the
// hole M5 exists to close.
//
// The composer word is crewstate.Composer, the spelling the console and
// `mate state` already share, so this package needs neither internal/send
// nor the runtime port it pulls in.
type HealthReading struct {
	Project      string
	Crew         string // empty for the Mate
	AgentPresent bool
	Composer     crewstate.Composer
	At           time.Time
}

// SessionRefFunc reports the harness session id the runtime records for one
// crew's agent - Herdr's `agent_session.value`, which for Codex is the
// rollout's session uuid. It is a seam for the same reason watch.HandleFunc
// is one: this package must not be able to reach Herdr, let alone start
// anything. An error or an empty string means "the runtime did not say", and
// the locator falls through to its next rule.
type SessionRefFunc func(ctx context.Context, project, crew string) (string, error)

// Deps are the ingest's collaborators. The zero value reads the real clock
// and the real git and resolves no session refs, but reads no transcript:
// that takes Harnesses.
type Deps struct {
	Now        func() time.Time
	SessionRef SessionRefFunc
	// Health returns the observer's latest readings. The ingest turns a
	// change in a reading into `health.changed`, and a busy stretch with no
	// tool call inside it into a `thinking` action.
	Health func() []HealthReading
	// Harnesses are the harnesses whose transcripts the ingest reads. An
	// agent whose harness is not registered here, or declares no transcript
	// it can read, is recorded unresolved and never guessed at.
	Harnesses harness.Registry
	// TranscriptRoots override, by harness, where its transcripts are
	// searched; tests point them at a fixture directory. A harness left
	// out searches its own default.
	TranscriptRoots map[harness.Kind]string
	Git             gitx.Git
	// ThinkingGap is the shortest uncovered stretch inside a turn that
	// becomes a `thinking` action. Zero means DefaultThinkingGap.
	ThinkingGap time.Duration
	// ProjectTimeout bounds what one project's pass of Ingest waits on
	// outside the process - git and Herdr - not the pass itself. Zero means
	// DefaultProjectTimeout.
	ProjectTimeout time.Duration
}

// DefaultProjectTimeout bounds the git and Herdr calls of one project's pass
// of a poll's ingest. The pass runs git per crew and asks Herdr for session
// refs, at the end of every observer round: a git or a Herdr that never
// answers must cost the round this long, not the round itself. Reindex is
// not bounded - it is one transaction over every project, run by a person
// who can interrupt it.
//
// Reading the files and writing the database are not bounded (CON-139): a
// pass that is rolled back for running out of time leaves the next pass the
// same work plus whatever arrived meanwhile, so once a project's backlog took
// longer than the bound no pass ever committed again. Measured 2026-10-30:
// mihuv2 needed 49 s to catch up and froze for hours at 15 s. A git or Herdr
// call that runs out the bound says nothing, which every caller already
// treats as "ask again next pass", and the pass still commits.
const DefaultProjectTimeout = 15 * time.Second

// DefaultThinkingGap is the shortest silence inside a turn the ingest is
// willing to leave unexplained. It is short because the question M5 asks is
// "what was this agent doing", and five seconds of a model reasoning between
// two tool calls is an answer, not noise.
const DefaultThinkingGap = 5 * time.Second

func (d Deps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

func (d Deps) git() gitx.Git {
	if d.Git.Runner != nil {
		return d.Git
	}
	return gitx.New()
}

func (d Deps) projectTimeout() time.Duration {
	if d.ProjectTimeout > 0 {
		return d.ProjectTimeout
	}
	return DefaultProjectTimeout
}

func (d Deps) thinkingGap() time.Duration {
	if d.ThinkingGap > 0 {
		return d.ThinkingGap
	}
	return DefaultThinkingGap
}

func (d Deps) health() []HealthReading {
	if d.Health == nil {
		return nil
	}
	return d.Health()
}

// Ingester reads a workspace's files into a timeline database. One Ingester
// belongs to one writer: Ingest is called from a single goroutine (the
// observer's poll loop), and internal/db has already refused a second
// process.
type Ingester struct {
	ws          *store.Workspace
	d           *db.DB
	deps        Deps
	transcripts map[string]transcriptCache
	settledGit  *gitFacts

	// mu guards stale, which readers outside the poll goroutine ask for.
	mu    sync.Mutex
	stale map[string]Stale
}

// Stale is a project whose ingest has failed every pass since Since: its
// timeline is missing whatever happened after that, while the files that
// the console reads go on moving. Measured 2026-10-30, a timeline frozen
// for hours said nothing anywhere and the Office drew finished crews as
// working (CON-139).
type Stale struct {
	Project string
	// Since is the wall-clock time of the first pass of the run that failed.
	Since time.Time
	// Error is the latest pass's error, on one line.
	Error string
}

// Stale reports the projects whose latest pass failed, by name. It is safe
// to call from any goroutine.
func (i *Ingester) Stale() []Stale {
	i.mu.Lock()
	defer i.mu.Unlock()
	out := make([]Stale, 0, len(i.stale))
	for _, st := range i.stale {
		out = append(out, st)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Project < out[b].Project })
	return out
}

func (i *Ingester) notePass(project string, err error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if err == nil {
		delete(i.stale, project)
		return
	}
	if i.stale == nil {
		i.stale = map[string]Stale{}
	}
	st, ok := i.stale[project]
	if !ok {
		st = Stale{Project: project, Since: time.Now()}
	}
	st.Error, _, _ = strings.Cut(err.Error(), "\n")
	i.stale[project] = st
}

// New builds an ingest over an opened workspace and a writable database.
func New(ws *store.Workspace, d *db.DB, deps Deps) *Ingester {
	return &Ingester{ws: ws, d: d, deps: deps}
}

// Ingest reads everything new since the last pass and records it. It is what
// the observer calls at the end of every poll.
//
// A project that fails is reported and the others still run: a half-written
// `.meta` or a transcript that vanished under the parser must not stop the
// rest of the workspace being recorded. The error is the join of what failed.
func (i *Ingester) Ingest(ctx context.Context) error {
	return i.ingestAll(ctx, nil)
}

// ingestAll runs one pass over every project. With a transaction it runs
// inside that one, which is how a rebuild is atomic; without one each project
// commits on its own, which is what a poll wants - a project whose `.meta` is
// half-written must not hold back the rest of the workspace.
func (i *Ingester) ingestAll(ctx context.Context, shared *sql.Tx) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !i.d.Writable() {
		return errors.New("timeline: this database handle is read-only")
	}
	if err := i.ws.LoadConfig(); err != nil {
		return err
	}
	if err := i.ingestPricing(ctx, shared); err != nil {
		return fmt.Errorf("timeline: pricing: %w", err)
	}
	var errs []error
	for _, ref := range i.ws.Projects() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if shared != nil {
			if err := i.ingestProject(ctx, ref.Name, shared, time.Time{}); err != nil {
				return fmt.Errorf("timeline: %s: %w", ref.Name, err)
			}
			continue
		}
		err := i.ingestBounded(ctx, ref.Name)
		if ctx.Err() == nil {
			// A pass cut short by the caller going away says nothing
			// about the project.
			i.notePass(ref.Name, err)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("timeline: %s: %w", ref.Name, err))
		}
	}
	return errors.Join(errs...)
}

// ingestBounded is one project's pass of a poll, its git and Herdr calls
// within ProjectTimeout (DefaultProjectTimeout says why only those).
func (i *Ingester) ingestBounded(ctx context.Context, project string) error {
	// The wall clock, not deps.Now: that one dates the records.
	return i.ingestProject(ctx, project, nil, time.Now().Add(i.deps.projectTimeout()))
}

// Reindex drops every derived table and rebuilds the whole timeline from the
// files and the transcripts. It is `mate reindex`, and it is the executable
// statement of decision 6: nothing here is a record, so throwing all of it
// away and reading the files again has to produce the same story.
func (i *Ingester) Reindex(ctx context.Context) error {
	if !i.d.Writable() {
		return errors.New("timeline: this database handle is read-only")
	}
	tx, err := i.d.SQL().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := db.ResetDerivedTx(ctx, tx); err != nil {
		return err
	}
	if err := i.ingestAll(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

// ingestProject is one project's pass, in one transaction. The order of the
// phases is the order their facts depend on each other: actors and tasks name
// everybody, the transcripts date the crews' own shell commands (which is how
// a status line - a line with no timestamp of its own - gets one), the logs
// read from their cursors, and the causality pass runs last because it needs
// every event of the pass to already have an id.
//
// external is the deadline of the pass's git and Herdr calls; zero is none.
func (i *Ingester) ingestProject(ctx context.Context, project string, shared *sql.Tx, external time.Time) (err error) {
	tx := shared
	if tx == nil {
		tx, err = i.d.SQL().BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() {
			if err != nil {
				_ = tx.Rollback()
			}
		}()
	}

	now := i.deps.now()
	w := newWriter(tx, now)
	b := newBatch()
	p := &pass{ing: i, project: project, now: now, b: b, w: w, tx: tx, external: external}

	if err = p.ingestMeta(ctx); err != nil {
		return err
	}
	if err = p.ingestProfiles(ctx); err != nil {
		return err
	}
	if err = p.ingestTranscripts(ctx); err != nil {
		return err
	}
	if err = p.ingestStatus(ctx); err != nil {
		return err
	}
	if err = p.ingestSent(ctx); err != nil {
		return err
	}
	if err = p.ingestIncidents(ctx); err != nil {
		return err
	}
	if err = p.ingestGit(ctx); err != nil {
		return err
	}
	if err = p.ingestHealth(ctx); err != nil {
		return err
	}
	if err = w.flush(ctx, b); err != nil {
		return err
	}
	if w.insertedEvents > 0 {
		if err = p.fillThinking(ctx); err != nil {
			return err
		}
		if err = p.linkCauses(ctx); err != nil {
			return err
		}
		if err = p.updateCounters(ctx); err != nil {
			return err
		}
	}
	if err = p.projectScene(ctx); err != nil {
		return err
	}
	if shared != nil {
		// The caller owns the transaction and commits every project at once.
		return nil
	}
	return tx.Commit()
}

// pass is the mutable state of one project's ingest.
type pass struct {
	ing     *Ingester
	project string
	now     time.Time
	b       *batch
	w       *writer
	tx      *sql.Tx

	// external is the deadline of the pass's calls outside the process (git,
	// Herdr); zero is none. See DefaultProjectTimeout.
	external time.Time

	// crews is every crew of the project, from `crews/<id>.meta`.
	crews []crewRecord
	// mate is the project's Mate, from `mate/mate.meta`.
	mate mateRecord
	// statusClock dates the status lines: a crew's own shell commands,
	// keyed by actor, with the command text as written in the transcript.
	statusClock map[string][]datedCommand
	// telemetryProfile is the model and effort the harness's telemetry
	// state names for an actor's session, read before its turns are
	// recorded; usageTurns falls back to it, then to the actor's `.meta`.
	telemetryProfile map[string]actorProfile
	// statusTimes are the status lines this pass dated, keyed by actor, so
	// a `sent.log` line read in the same pass can tell whether the crew
	// spoke between two of the Mate's lines (reanswer).
	statusTimes map[string][]time.Time
	// lastAnswered is the newest answer this pass recorded per crew, for
	// the same question.
	lastAnswered map[string]*pendingQuestion
	// commitSightings are the commits an actor's transcript shows it making,
	// keyed by actor. They matter because a branch is short-lived: `mate
	// merge` deletes it, and a crew that commits seconds before the merge can
	// leave no window in which any poll could have read `git log`.
	commitSightings map[string][]commitSighting
}

type datedCommand struct {
	command string
	at      time.Time
	// path and offset are the transcript byte the command was read from, so
	// a status line it dates can sort right after it.
	path   string
	offset int64
}

// commitSighting is a commit an agent's transcript shows it making: the sha
// git printed back at it, and when.
type commitSighting struct {
	sha string
	at  time.Time
}

// readCursor is where the last pass stopped in one append-only file.
func (p *pass) readCursor(ctx context.Context, path string) (int64, error) {
	var offset int64
	err := p.tx.QueryRowContext(ctx, `SELECT byte_offset FROM cursor WHERE source_path = ?`, path).Scan(&offset)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return offset, nil
}

// actorProfile is a model and a reasoning effort, as a harness or a
// launch recorded them.
type actorProfile struct {
	Model, Effort string
}

// metaProfile is the launch profile `.meta` keeps for an actor (`model=`,
// `effort=`, written by crew spawn and mate start), the fallback for a
// turn whose transcript names no model or no effort.
func (p *pass) metaProfile(actorID string) actorProfile {
	if p.mate.Present && p.mate.ActorID == actorID {
		return actorProfile{Model: p.mate.Meta[MetaModel], Effort: p.mate.Meta[MetaEffort]}
	}
	for _, c := range p.crews {
		if c.ActorID == actorID {
			return actorProfile{Model: c.Meta[MetaModel], Effort: c.Meta[MetaEffort]}
		}
	}
	return actorProfile{}
}

// turnProfile is the model and effort a turn is recorded with: the
// transcript's own model first, then what the harness's telemetry state
// says, then the launch profile. Effort has no transcript source on
// Claude, so there it is always the launch profile (CON-75).
func (p *pass) turnProfile(actorID, transcriptModel string) actorProfile {
	out := actorProfile{Model: transcriptModel}
	tel := p.telemetryProfile[actorID]
	meta := p.metaProfile(actorID)
	if out.Model == "" {
		out.Model = firstNonEmpty(tel.Model, meta.Model)
	}
	out.Effort = firstNonEmpty(tel.Effort, meta.Effort)
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
