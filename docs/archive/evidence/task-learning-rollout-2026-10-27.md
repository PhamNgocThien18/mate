# Task learning rollout — 2026-10-27

Deployment below is superseded by [the console deployment correction](console-deployment-correction-2026-10-27.md); the first build used an older branch.

Mate now checks learning at delivery/closure and external merge catch-up,
deduplicates by evidence revision, and carries relevant prediction pointers
into later briefs. Crew handoffs name a Learning result. The new
`task-learning` skill limits records to 40 lines and three themes; absent
metrics do not trigger transcript audits or measurement-only scouts.

`mate usage <project> <crew> --why` prints existing recorded timing lanes
and category elapsed time alongside token buckets. Lanes may overlap;
unallocated time is not model latency. No new collection was introduced.

Validation: `make check`, `make build` and `git diff --check` passed.
The suite reported 80 expected live skips, which do not prove model
compliance. Asset goldens and launch characterization cover rendering;
the usage test checks timing output and overlap/unknown caveats.

Installed `/opt/homebrew/bin/mate` from this dirty working tree (including
pre-existing user changes), build `d5e2b04-dirty`, built at
2026-10-27T06:38:27Z. SHA-256:
`c83c925dd42378c1000660909a17721e82190e913609c531cde8ac6f447ee1bb`.
Previous binary retained at `/tmp/mate-before-learning-20261027`.

The plantrip, mihuv2 and signal Mate hosts restarted and resumed their
existing sessions. All three generated manuals and skill directories
contain the new procedures. Signal and Mihu stowed; Plantrip's stow was
refused because its composer held unsent text, and its session was retained.
Existing Crew launches/briefs were not rewritten. The Ghostty console has
not restarted: the computer-use tool refused access to that app. Reopen
the console with the installed binary to replace that old process.

A read-only invocation against PLA-104 confirmed installed CLI output:
175 model calls; 14.9M total tokens including 14.1M cache-read;
Research/inspect 81 calls and 6.4M tokens. These are historical baseline
measurements, not evidence of an efficiency improvement.

Harness upstream PR #3 merged as v0.11.0 (`3e68c80`):
https://github.com/ericsnguyen9941/harness/pull/3.
Typecheck, lint, 29 tests (259 assertions), and kit harness-check passed.
Seven workspace repository upgrades are committed on `chore/harness-0-11`.
market_monitor and signal-infra were applied locally; the remaining five
await product gates or the captain’s exception for this docs-only rollout.
Their rollout inventory, verification limitations and current application
status live in `/Users/erics/workspace/.mate/harness-upgrades/2026-10-27/`.

Next evidence: a comparable task must exercise settled intake, bounded
inspect, closure learning and prediction reuse without reduced acceptance
or review quality. Restarting hosts and changing prompts do not prove
those model behaviours or any token/time saving.
