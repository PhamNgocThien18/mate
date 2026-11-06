package core

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/nguyenngocanh94/mate/internal/coreapi"
	"github.com/nguyenngocanh94/mate/internal/gitx"
	"github.com/nguyenngocanh94/mate/internal/matehost"
	"github.com/nguyenngocanh94/mate/internal/observability"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// The Project's actions (CON-231): add one to the workspace, remove one,
// and the three requests to its headless Mate's REPL and work board. They
// ran in cmd/mate's consoleAction until CON-215's B4.

// addProject is the new-project form, an onboard of the workspace: the
// same registration `mate project add <name> [<repo>]` performs, with the
// repo path resolved against the workspace root rather than a shell's
// working directory, since a UI has no cwd the reader chose. An empty repo
// registers a Project with no repo yet (docs/mvp.md M9).
//
// It registers through a freshly opened Workspace rather than the shared
// one: the engine re-reads workspace.yaml into that one (reload.go) from
// another goroutine, and the next re-read picks the new Project up from
// disk anyway.
func (e *Engine) addProject(ctx context.Context, req coreapi.ActionRequest) (string, error) {
	name, repo := strings.TrimSpace(req.Input), strings.TrimSpace(req.Repo)
	if name == "" {
		return "", observability.NewError(observability.CodeUsage, "project name is required")
	}
	fresh, err := store.Open(e.o.Workspace.Root())
	if err != nil {
		return "", err
	}
	if repo == "" {
		if _, err := spawn.AddProject(fresh, name, spawn.AddProjectOptions{}); err != nil {
			return "", err
		}
		return fmt.Sprintf("Project %s added with no repo yet; add one with `mate project repo add %s <path>`", name, name), nil
	}
	absRepo := repo
	if !filepath.IsAbs(absRepo) {
		absRepo = filepath.Join(fresh.Root(), repo)
	}
	repoCfg, err := spawn.RepoConfigFor(ctx, gitx.New(), absRepo, repo, "", "")
	if err != nil {
		return "", err
	}
	saved, err := spawn.AddProject(fresh, name, spawn.AddProjectOptions{Repos: []store.RepoConfig{repoCfg}})
	if err != nil {
		return "", err
	}
	added := saved.Repos[0]
	return fmt.Sprintf("Project %s added: repo %s, default branch %s", name, added.Path, added.DefaultBranch), nil
}

// removeProject is the Project row's `Remove project…` entry (mvp.md task
// 75). It is `mate project remove`: the crews and the Mate are stopped,
// the Mate after the captain's stow, and only then does the Project leave
// workspace.yaml. Like addProject it works on a store it opens itself,
// because RemoveProject saves the whole config and the engine's own copy
// may be older than the file.
func (e *Engine) removeProject(ctx context.Context, req coreapi.ActionRequest) (string, error) {
	if req.Target == "" || req.TargetKind != "project" {
		return "", observability.NewError(observability.CodeUsage,
			fmt.Sprintf("remove project needs a Project; the request named %s %q", req.TargetKind, req.Target))
	}
	fresh, err := store.Open(e.o.Workspace.Root())
	if err != nil {
		return "", err
	}
	deps := e.o.Spawn
	var stderr strings.Builder
	res, err := spawn.RemoveProject(ctx, fresh, deps, req.Target, spawn.RemoveProjectStow(fresh, deps, spawn.CallerUser, &stderr, nil))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Project %s removed: %d agent(s) stopped; its files stay on disk, `mate project add %s` brings it back",
		req.Target, len(res.Stopped), req.Target), nil
}

// openNews is Enter on a news in the box (CON-86): it asks the Project's
// headless Mate REPL to open the news, which the console stages beside
// it. It writes the request and nothing else.
func (e *Engine) openNews(_ context.Context, req coreapi.ActionRequest) (string, error) {
	ws := e.o.Workspace
	id, err := strconv.Atoi(req.Input)
	if err != nil || id <= 0 || req.Target == "" {
		return "", observability.NewError(observability.CodeUsage,
			fmt.Sprintf("open news needs a Project and a news id; the request named %q #%q", req.Target, req.Input))
	}
	if err := ws.RequireProject(req.Target); err != nil {
		return "", err
	}
	if err := matehost.RequestNewsFocus(ws.MateDir(req.Target), id, e.now()); err != nil {
		return "", err
	}
	return fmt.Sprintf("news #%d opened in the Mate", id), nil
}

// openWork is `w` on a Mate or Project row (CON-198): the Mate's REPL
// shows its work board as a page. A Mate that does not run headless has
// no REPL to ask, and the console shows its own sheet.
func (e *Engine) openWork(_ context.Context, req coreapi.ActionRequest) (string, error) {
	ws := e.o.Workspace
	if req.Target == "" {
		return "", observability.NewError(observability.CodeUsage, "open work needs a Project")
	}
	if err := ws.RequireProject(req.Target); err != nil {
		return "", err
	}
	if !ws.MateHeadless(req.Target) {
		return "", coreapi.ErrNoMateREPL
	}
	if err := matehost.RequestWorkFocus(ws.MateDir(req.Target), e.now()); err != nil {
		return "", err
	}
	return "work board opened in the Mate", nil
}

// okayWork is the captain's tick on a done row of the work board
// (CON-158): Space on it in the console's work sheet, or a page's okay,
// which the dashboard sends as okay_work (CON-206), where no headless
// Mate's host takes it for its undo (okayOnHost, CON-222). The tick is
// final; the row leaves the board and the Mate is never waiting on it.
func (e *Engine) okayWork(_ context.Context, req coreapi.ActionRequest) (string, error) {
	ws := e.o.Workspace
	id, err := strconv.Atoi(req.Input)
	if err != nil || id <= 0 || req.Target == "" {
		return "", observability.NewError(observability.CodeUsage,
			fmt.Sprintf("okay work needs a Project and a row id; the request named %q #%q", req.Target, req.Input))
	}
	if err := ws.RequireProject(req.Target); err != nil {
		return "", err
	}
	if err := matehost.OkayWork(matehost.WorkPath(ws.MateDir(req.Target)), id, e.now()); err != nil {
		return "", err
	}
	return fmt.Sprintf("okayed work #%d of %s; it is off the board", id, req.Target), nil
}
