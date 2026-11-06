package main

import (
	"testing"

	"github.com/nguyenngocanh94/mate/internal/core"
	"github.com/nguyenngocanh94/mate/internal/coreapi"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// The engines the tests build. Only `mate core` builds one for real
// (cmd/mate/core.go); no console file does, its tests included
// (TestRunConsoleIsAClientOfTheCore).

// testEngine is core.New(o), stopped when the test ends; Stop is safe to
// call again before then.
func testEngine(t testing.TB, o core.Options) *core.Engine {
	t.Helper()
	engine := core.New(o)
	t.Cleanup(engine.Stop)
	return engine
}

// engineAction is how a test presses an action the way every UI does:
// through an engine's Act (CON-231), the engine not started - Act needs
// none of its loops - over w and deps, with no Jev. The engine runs every
// action itself (CON-232).
func engineAction(t testing.TB, w *store.Workspace, deps spawn.Deps) coreapi.ActionFunc {
	t.Helper()
	return engineNoticeAction(t, w, deps, nil)
}

// engineNoticeAction is engineAction with jev as the engine's Jev
// (core.Options.Notice), which explain_notice asks.
func engineNoticeAction(t testing.TB, w *store.Workspace, deps spawn.Deps, jev core.NoticeClassifier) coreapi.ActionFunc {
	t.Helper()
	return testEngine(t, core.Options{Workspace: w, Spawn: deps, Notice: jev}).Act
}
