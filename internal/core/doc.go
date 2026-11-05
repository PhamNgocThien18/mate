// Package core runs the background loops of an open workspace (CON-203):
// the observer of mvp.md section 4b with the timeline it records, the
// outbox sender and the auto daemon of mvp.md section 5, and recovery
// (docs/mvp.md M17).
//
// Engine holds their lifecycle: the order they start in, the reverse order
// they stop in, the timeline database it closes last, and the state a
// snapshot reads off them. The parts themselves are built by the caller and
// handed in through Options, the way spawn.Deps and dashboard.Deps take
// theirs: cmd/mate builds them over the live Herdr adapter, and a test
// hands in fakes.
//
// A process with no console runs this package (CON-211), so it reaches
// neither a command nor a UI: no cmd/... and no internal/ui/... package,
// directly or through another package of this module.
// boundary_test.go enforces that rather than trusting this paragraph.
package core
