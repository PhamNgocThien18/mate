# Console deployment correction — 2026-10-27

The intake/learning rollout incorrectly built `integ/con-102-103` at
`d5e2b04`, which lacked 58 commits present in the previously installed
binary (`go version -m` identifies `60bc453fdfa3`). The actual workspace
uses `dashboard.addr` and `dashboard.allow_remote`, introduced by CON-116.
The older binary rejected these valid fields before opening the console.

Reproduction: `/opt/homebrew/bin/mate /Users/erics/workspace` exited 1 with
`line 18: field dashboard not found in type consolecmd.Config`.
No workspace configuration edit is needed or was made.

Correction: branch `fix/intake-learning-release`, worktree
`/Users/erics/app/mate-learning-release`, starts at the exact previously
installed revision and applies the intake/learning changes there. Merge
resolution preserves later scope, teardown, news and delivery behaviour.
Unrelated in-progress notification edits remain in the original checkout;
they are not part of this corrective release.

Validation: existing TestLoadDashboard and dashboard/brief/usage tests,
regenerated prompt and launch goldens, `make check` and `make build` passed.
The suite reported 81 declared live skips; model compliance remains unproven.
A PTY smoke run opened the real workspace with its unchanged console.yaml,
rendered all three projects, and served HTTP 200 on port 7777. The frame
showed both localhost and LAN URLs. The smoke console exited cleanly.

Deployment learning: before installing a local build, compare its source
revision with the installed binary's build provenance, preserve deployed
features, and smoke-test the actual workspace configuration. A passing
suite on an older branch cannot prove upgrade compatibility. Keep rollback
artifacts and do not treat the original dirty checkout as the release tree.
