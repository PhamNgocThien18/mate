// Package core runs the background loops of an open workspace (CON-203):
// the observer of mvp.md section 4b with the timeline it records, the
// outbox sender and the auto daemon of mvp.md section 5, recovery
// (docs/mvp.md M17), the dashboard that serves Mate Office and Agent City,
// and the captain's notifications. It also computes the one snapshot every
// UI reads (Engine.Snapshot, CON-208), and keeps the workspace it shares
// with the dashboard in step with workspace.yaml.
//
// Engine holds their lifecycle: the order they start in, the one bound they
// stop within (StopWait, CON-210), the timeline database it closes last, and
// the state a snapshot reads off them. The parts themselves are built by the caller and
// handed in through Options, the way spawn.Deps and dashboard.Deps take
// theirs: cmd/mate builds them over the live Herdr adapter, and a test
// hands in fakes.
//
// A process with no console runs this package (CON-211), so it reaches
// neither a command nor a UI: no cmd/... and no internal/ui/... package,
// directly or through another package of this module.
// boundary_test.go enforces that rather than trusting this paragraph.
package core
