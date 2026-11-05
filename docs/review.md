# Independent code review

`mate review <project> <crew> --id <reviewer>` starts one independent scout Crew.
It reviews the committed change at the recorded target SHA, using the task brief
as the contract and the hand-back as evidence to verify. The dispatch profile and
budget still come from the normal review settings.

The reviewer identifies the assumptions that matter to the change, traces their
owner and directly affected callers, and tries the smallest check that could
falsify them. Examples include a serialization round-trip, an ambiguous retry, or
a boundary input through the actual caller. Module boundaries matter when they
explain a concrete defect; an unsolicited refactor stays nonblocking.

Evidence is distinguished as observed, static, inherited with a source/revision,
or unverified. A decisive static path can establish a defect without a live
service. A skipped test cannot establish the behaviour it skipped.

| Outcome | Meaning |
| --- | --- |
| `changes-requested` | A demonstrated material defect or an open prior finding. |
| `incomplete` | No defect is established, but material acceptance or correctness evidence is missing. |
| `pass` | In-scope criteria are supported, prior findings are closed, and both Findings and Verification gaps say `none`. |

Material defects go under `## Findings`, one top-level item each, with trigger,
impact, file:line and expected/actual evidence. Missing proof goes under
`## Verification gaps`, with the next check needed. Nonblocking observations go
under `## Notes`; they never create a correction round or block a merge.

After a fix, `mate review <project> <crew> --id <next> --since <previous>` reviews
only the delta from the prior reviewed SHA. It closes each numbered prior finding
exactly once and checks regressions caused by that delta. An unchanged caller may
be inspected when directly affected; this does not authorize an unrelated audit.
Prior material verification gaps stay unresolved until supported. Gates are
repeated only when the delta invalidates their earlier evidence. If only the
environment/evidence changed at the same commit, request a fresh review without
`--since`, which requires a new commit.

`mate review <project> <crew> --check <reviewer>` and `mate merge --review` retain
the existing revision, brief, hand-back and prior-report freshness checks. New
review records carry `review_contract=2`. Their passing reports must contain
exactly one Verdict, Findings and Verification gaps section. A scoped pass also
needs exactly one closed mark for every prior finding, with no unknown numbers.
Contradictory, missing or duplicate sections cannot authorize a merge. This is a
structural check; the reviewer remains responsible for the truth of its evidence.
Historical records without the contract marker retain their old rules, including
the legacy `--followups` path. Unsupported contract versions are refused.

The implementation adapts pstack's assumptions, ownership and falsification
methods while keeping mate's existing task, Crew and merge ownership. Research:
[philosophy](archive/research/pstack-design-philosophy-2026-10-29.md). Reproducible paired
pilot: [revieweval](../scripts/revieweval/README.md). It compares reviewer behaviour
separately from report-schema enforcement and does not substitute for a Herdr
lifecycle test.
