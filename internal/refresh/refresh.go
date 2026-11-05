// Package refresh changes a Mate's session when its context has grown, at a
// checkpoint the Mate wrote itself: the Mate is asked to stow what exists
// only in its conversation and to answer the request with `mate
// checkpoint`, and only once that receipt verifies is the old session
// archived and a fresh one started, which recalls what was stowed.
//
// The caller hands in, through Deps, what it owns: where the request lives
// and how the Mate's receipt is verified (`mate checkpoint`), and the outbox
// sender over the live runtime.
package refresh

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/matehost"
	"github.com/nguyenngocanh94/mate/internal/memory"
	"github.com/nguyenngocanh94/mate/internal/outbox"
	"github.com/nguyenngocanh94/mate/internal/send"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/timeline"
)

// defaultContext is the context, in tokens, at which an automatic refresh
// is due when project.yaml leaves mate.refresh_context at zero.
const defaultContext int64 = 150000

// retryAfter is how long an automatic refresh waits, after asking a Mate
// to stow, before it asks again.
const retryAfter = 30 * time.Minute

// Deps are what a refresh needs from its caller. Every field is required.
type Deps struct {
	// Spawn reads, reloads, stops and starts the Mate. Its Now is the
	// refresh's clock, and its Binary the `mate` the stowing Mate runs
	// (the running executable when empty).
	Spawn spawn.Deps
	// Outbox asks the Mate to stow.
	Outbox *outbox.Sender
	// RequestPath is the store meta file a refresh writes its request to.
	// Its nonce and session keys are what `mate checkpoint <project>
	// <nonce>` reads and must answer (cmd/mate/checkpoint.go); its
	// attempted_at is when the request was made, which spaces automatic
	// attempts by retryAfter.
	RequestPath func(project string) (string, error)
	// VerifyCheckpoint returns nil only when the Mate's receipt answers
	// nonce in session and nothing the receipt covers changed since.
	VerifyCheckpoint func(project, nonce, session string) error
}

// Refresher refreshes the Mates of one workspace.
type Refresher struct {
	ws   *store.Workspace
	deps Deps
}

// New builds a refresher over ws.
func New(ws *store.Workspace, deps Deps) *Refresher {
	return &Refresher{ws: ws, deps: deps}
}

// Run changes sessions only at a verified checkpoint. Queued work remains in
// the durable outbox. Ordinary restart retains its resume semantics.
//
// automatic is the console's own refresh: it runs only for a quiet Mate
// whose current session reached the project's context limit, and it puts
// itself off without an error where the captain's `mate mate refresh` is
// told why. Run reports whether the Mate's session changed.
func (r *Refresher) Run(ctx context.Context, project string, automatic bool) (bool, error) {
	a, due, err := r.begin(ctx, project, automatic)
	if err != nil || !due {
		return false, err
	}
	release, locked, err := r.ws.TryMateMaintenance(project, true)
	if err != nil || !locked {
		return false, err
	}
	defer release()
	if ready, err := a.ready(ctx); err != nil || !ready {
		return false, err
	}
	if err := a.stow(ctx); err != nil {
		return false, err
	}
	current, err := a.confirm(ctx)
	if err != nil {
		return false, err
	}
	return a.swap(ctx, current)
}

// attempt is one Run: what it read before asking for a stow, and what the
// Mate's receipt must answer.
type attempt struct {
	ws        *store.Workspace
	deps      Deps
	project   string
	automatic bool
	now       time.Time
	// meta is mate.meta as the attempt found it, and session its session.
	meta    map[string]string
	session string
	// requestPath is where the request is written, nonce what the receipt
	// must name, and offset where sent.log ended before the stow.
	requestPath string
	nonce       string
	offset      int64
}

// begin reads what the attempt turns on and reports whether a refresh is
// due: the Mate has a session and, for an automatic refresh, may be
// refreshed automatically, is quiet, and has reached the context limit.
func (r *Refresher) begin(ctx context.Context, project string, automatic bool) (*attempt, bool, error) {
	a := &attempt{ws: r.ws, deps: r.deps, project: project, automatic: automatic, now: time.Now()}
	if r.deps.Spawn.Now != nil {
		a.now = r.deps.Spawn.Now()
	}
	cfg, err := r.ws.LoadProject(project)
	if err != nil {
		return nil, false, err
	}
	if a.meta, err = r.ws.ReadMateMeta(project); err != nil {
		return nil, false, err
	}
	a.session = a.meta[spawn.MetaSessionID]
	if automatic && a.manualOnly(cfg) {
		return nil, false, nil
	}
	if a.session == "" {
		return nil, false, nil
	}
	if !automatic {
		return a, true, nil
	}
	due, err := a.automaticDue(ctx, cfg)
	return a, due, err
}

// manualOnly reports whether only the captain may refresh this Mate: its
// project turned automatic refresh off, the captain has it in hand, or its
// harness gives nothing to judge it quiet by. Automatic refresh judges the
// Mate quiet by the answers its Stop hook logs to sent.log (mateQuiet). A
// harness whose turn-end evidence is not that log - Codex's is its rollout
// - or that declares none gives it nothing to judge by, so it is never
// refreshed automatically; the captain's `mate mate refresh` still can,
// though one that declares none cannot show the stow turn ended either, so
// that refresh is put off when the stow times out.
func (a *attempt) manualOnly(cfg store.ProjectConfig) bool {
	return cfg.Mate.RefreshContext < 0 || !logsAnswers(a.deps.Spawn.Harnesses, a.meta[spawn.MetaHarness]) || !a.ws.Auto(a.project) || a.ws.Held(a.project)
}

// automaticDue reports whether an automatic refresh is due: the Mate is
// quiet, and its current session carries at least the project's
// refresh_context tokens, as the timeline last measured them. A timeline
// that cannot be opened puts the refresh off.
func (a *attempt) automaticDue(ctx context.Context, cfg store.ProjectConfig) (bool, error) {
	quiet, err := mateQuiet(a.ws, a.project, a.now)
	if err != nil || !quiet {
		return false, err
	}
	handle, err := db.OpenRead(a.ws)
	if err != nil {
		return false, nil
	}
	usage, known, err := handle.LatestContext(ctx, timeline.MateActorID(a.project), a.session)
	handle.Close()
	if err != nil {
		return false, err
	}
	limit := cfg.Mate.RefreshContext
	if limit == 0 {
		limit = defaultContext
	}
	return known && usage.Tokens >= limit, nil
}

// ready checks, under the maintenance lock, what must hold before the Mate
// is asked to stow: nothing is queued for it, an automatic refresh did not
// ask within retryAfter, and its composer is empty.
func (a *attempt) ready(ctx context.Context) (bool, error) {
	if err := queueEmpty(a.ws, a.project); err != nil {
		return a.putOff(err)
	}
	var err error
	if a.requestPath, err = a.deps.RequestPath(a.project); err != nil {
		return false, err
	}
	prior, err := store.ReadMeta(a.requestPath)
	if err != nil {
		return false, err
	}
	if a.automatic && a.askedRecently(prior) {
		return false, nil
	}
	// Busy/pending is a deferral, not a failed stow: do not spend the retry
	// cooldown before an LLM request was even possible. Stow rechecks this.
	composer, err := mateComposer(ctx, a.ws, a.deps.Spawn, a.project)
	if err != nil {
		return a.putOff(err)
	}
	if composer.State != send.StateEmpty {
		return a.putOff(fmt.Errorf("refresh postponed: Mate composer is %s", send.ComposerLabel(composer.State)))
	}
	return true, nil
}

// putOff is a refresh put off for err before anything was asked of the
// Mate: the captain's is told why, an automatic one tries on a later tick.
func (a *attempt) putOff(err error) (bool, error) {
	if a.automatic {
		return false, nil
	}
	return false, err
}

// askedRecently reports whether the prior request asked the Mate to stow
// less than retryAfter ago.
func (a *attempt) askedRecently(prior map[string]string) bool {
	at, err := time.Parse(time.RFC3339Nano, prior["attempted_at"])
	return err == nil && a.now.Sub(at) < retryAfter
}

// stow writes a fresh request, checks a fresh Mate could start, and asks the
// Mate to stow and answer the request with `mate checkpoint`.
func (a *attempt) stow(ctx context.Context) error {
	a.nonce = rand.Text()
	req := map[string]string{"nonce": a.nonce, "session": a.session, "attempted_at": a.now.UTC().Format(time.RFC3339Nano)}
	if err := store.WriteMeta(a.requestPath, req); err != nil {
		return err
	}
	// The fresh start must be able to come up before the Mate is asked to
	// stow, let alone stopped: plugins that do not resolve for this project,
	// or MCP servers its harness cannot be given, would leave it with no
	// Mate at all. Asked after the request is written, so an automatic
	// refresh waits out retryAfter rather than retrying every tick.
	if err := a.preflight(); err != nil {
		return err
	}
	// Where sent.log ends now, so the check after the stow sees only what
	// was said during it. Writers append whole lines under the lock, so the
	// size is the offset a full read would end at.
	var err error
	if a.offset, err = a.ws.SentLogSize(a.project); err != nil {
		return err
	}
	line, err := a.stowLine()
	if err != nil {
		return err
	}
	stowed, err := a.deps.Outbox.Stow(ctx, a.project, outbox.StowOptions{RequireEmpty: true, RequireCompletion: true, Text: line})
	if err != nil {
		return err
	}
	if !stowed.Stowed {
		return fmt.Errorf("refresh postponed: %s", stowed.Outcome())
	}
	return nil
}

// stowLine is the stow request: memory.StowLine, and the receipt the Mate
// runs before it ends its turn, `mate checkpoint <project> <nonce>`.
func (a *attempt) stowLine() (string, error) {
	binary := a.deps.Spawn.Binary
	if binary == "" {
		var err error
		if binary, err = os.Executable(); err != nil {
			return "", err
		}
	}
	return memory.StowLine + "; before ending, preserve unresolved questions verbatim, promises, decisions, active crews and next actions, then run " + shellWord(binary) + " checkpoint " + shellWord(a.project) + " " + a.nonce, nil
}

// preflight reports whether a fresh Mate could start on the harness the
// Mate runs: its plugins resolve, and their MCP servers can be given it.
func (a *attempt) preflight() error {
	if err := spawn.PreflightMatePlugins(a.ws, a.deps.Spawn, a.project, harness.Kind(a.meta[spawn.MetaHarness])); err != nil {
		return fmt.Errorf("refresh postponed: a fresh Mate could not start: %w", err)
	}
	return nil
}

// confirm checks, after the stow, that the session may still change: the
// receipt verifies, the captain said nothing during the stow, nothing was
// queued meanwhile, the session is the one asked to stow, an automatic
// refresh is still the console's to make, and a fresh Mate could still
// start. It returns mate.meta as it is now.
func (a *attempt) confirm(ctx context.Context) (map[string]string, error) {
	if err := a.deps.VerifyCheckpoint(a.project, a.nonce, a.session); err != nil {
		return nil, err
	}
	if err := a.captainSilent(); err != nil {
		return nil, err
	}
	if err := queueEmpty(a.ws, a.project); err != nil {
		return nil, err
	}
	current, err := a.ws.ReadMateMeta(a.project)
	if err != nil {
		return nil, err
	}
	if current[spawn.MetaSessionID] != a.session {
		return nil, fmt.Errorf("refresh postponed: Mate session changed")
	}
	if a.automatic && (!a.ws.Auto(a.project) || a.ws.Held(a.project)) {
		return nil, fmt.Errorf("refresh postponed: captain took manual control")
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	// Again, since the stow took a turn in which plugins.yaml could change.
	if err := a.preflight(); err != nil {
		return nil, err
	}
	return current, nil
}

// captainSilent fails when the captain spoke to the Mate during the stow.
// A captain prompt during stow takes precedence, even if it has already
// been answered. Do not discard a conversation that changed since stow.
func (a *attempt) captainSilent() error {
	entries, _, err := a.ws.ReadSent(a.project, a.offset)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Source == store.SourceUser && e.Target == store.TargetMate {
			return fmt.Errorf("refresh postponed: captain spoke during stow")
		}
	}
	return nil
}

// swap archives the stowed session and starts the fresh one: in the Mate's
// own pane when it can change session in place, else by a stop and a
// fresh start.
func (a *attempt) swap(ctx context.Context, current map[string]string) (bool, error) {
	if err := a.ws.ArchiveMateSession(a.project, current); err != nil {
		return false, err
	}
	// A headless Mate changes session in its own pane: stopping it closed
	// the pane, and every attach to it - the console's stage among them -
	// ended with "terminal ... not found" (CON-133).
	switch _, err := spawn.ReloadMate(ctx, a.ws, a.deps.Spawn, a.project); {
	case err == nil:
		// The host switched between turns, so no turn writes the old
		// transcript any more.
		if err := a.ws.FreezeMateSession(a.project, current); err != nil {
			return false, fmt.Errorf("Mate refreshed with checkpoint saved; archive failed: %w", err)
		}
		return true, a.record()
	case errors.As(err, new(*matehost.BusyError)):
		return false, fmt.Errorf("refresh postponed: %w", err)
	case !errors.Is(err, spawn.ErrNoReload):
		return false, err
	}
	return a.restart(ctx, current)
}

// restart stops the Mate and starts it fresh, for a Mate that cannot change
// session in place.
func (a *attempt) restart(ctx context.Context, current map[string]string) (bool, error) {
	stopped, err := spawn.StopMate(ctx, a.ws, a.deps.Spawn, a.project)
	if err != nil {
		return false, err
	}
	// Only a confirmed stop freezes the transcript. A Mate whose Herdr
	// session was not reported running was cleared on inference alone, so
	// its archive stays as written above, on the live transcript and not
	// finalized: the timeline keeps its last message group pending rather
	// than flushing a turn a writer may still extend.
	if stopped.Confirmed {
		if err := a.ws.FreezeMateSession(a.project, current); err != nil {
			return false, fmt.Errorf("Mate stopped with checkpoint saved; archive failed: %w", err)
		}
	}
	_, err = spawn.StartMate(ctx, a.ws, a.deps.Spawn, spawn.StartRequest{Project: a.project, Harness: harness.Kind(a.meta[spawn.MetaHarness]), Fresh: true})
	if err != nil {
		return false, fmt.Errorf("checkpoint saved; fresh start failed: %w", err)
	}
	return true, a.record()
}

// record writes to sent.log that the session changed, and which it was.
func (a *attempt) record() error {
	return a.ws.AppendSent(a.project, store.SentEntry{Source: store.SourceApp, Target: store.TargetMate, Text: "context refreshed from checkpoint; previous session " + a.session})
}
