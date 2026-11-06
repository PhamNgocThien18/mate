// Package herdr is the live runtime.Adapter: it runs the herdr executable
// through a process.Runner against a named Herdr session. The port, the
// handles, the error taxonomy and the fake adapter stay in internal/runtime.
// In non-test code only internal/spawn, which builds the adapter, imports
// this package (boundary_test.go).
package herdr
