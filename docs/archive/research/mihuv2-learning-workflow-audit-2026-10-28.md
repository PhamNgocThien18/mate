# mihuv2 learning workflow audit — 2026-10-28

Read-only audit of runtime artifacts for HU-108–HU-114 and repository sources. No runtime or mihuv2 files changed; no builds, messages or tracker writes. This note records observed adoption, not a controlled before/after efficiency experiment.

## Finding

The workflow has changed in observable ways: handoffs now usually contain bounded learning, Mate has task-level learning records, known API/verification traps are copied into later briefs, and HU-114 explicitly avoids HU-110's real-dismissal acceptance ambiguity. Adoption is incomplete: one handoff omits the Learning result, some records remain provisional after task closure, most later records omit bounded usage measurement, repeated full gates and review evidence errors remain. There is no demonstrated token/time improvement.

## Coverage in the inspected task sample

| Task | Integrator learning record | Crew handback Learning line | Review sequence recorded |
| --- | --- | --- | --- |
| HU-108 SizePicker | yes, captured; explicitly provisional while crew open | yes, captured | r1 pass |
| HU-109 harness PR | yes, none | yes, none | r1 pass |
| HU-110 paste wiring | yes, captured; still provisional, r1/r2 only | yes, captured | r1 changes; r2 changes; r3 pass |
| HU-111 editor limits | yes, captured | yes, captured | r1 incomplete; r2 pass after Mate ruling |
| HU-112 Board pick | yes, captured | yes, captured | r1 pass |
| HU-113 PDF placeholders | yes, captured | **no** | r1 incomplete; r2 pass |
| HU-114 pick wiring | yes, captured | yes, captured | r1 pass |

Thus **7/7** have one task-named integrator record, **6/7** have a formal Learning line in handback. This proves artifacts exist, not when every check executed or that all duplicate notifications were handled correctly. HU-108 was already using learning while the upgrade had not reached origin/main, so it is not a clean pre-upgrade control.

Sources: task `.meta` review links and `mate/learnings/<crew>.md`; representative [HU-108 record](/Users/erics/workspace/.mate/projects/mihuv2/mate/learnings/hu-108-sizepicker.md:2), [HU-109 record](/Users/erics/workspace/.mate/projects/mihuv2/mate/learnings/hu-109-harness-pr.md:4), [HU-113 handback](/Users/erics/workspace/.mate/projects/mihuv2/crews/hu-113-pdf-placeholders/handback.md:3), [HU-113 integrator catch-up record](/Users/erics/workspace/.mate/projects/mihuv2/mate/learnings/hu-113-pdf-placeholders.md:4). HU-113's later integrator record supplies a learning result despite the implementer's missing formal line; the record has no creation timestamp proving exactly when catch-up happened.

## Concrete evidence of reuse

### HU-110 → HU-114: choose a testable acceptance boundary

HU-110 learning says a real SwiftUI dismissal cannot be proved by manually invoking the router; a later integration brief must explicitly require a UI-test target or accept router-level verification. Mate stores this in memory Lessons. HU-114's brief then states **there is no UI-test target or simulator tap tool; router/model-level tests suffice** and its acceptance explicitly says **end to end at router/model level**. Its first reviewer reports pass with no material findings. This is a real change in contract wording and an observed absence of the same review dispute, though not proof that overall review quality or elapsed time improved.

Sources: [HU-110 learning](/Users/erics/workspace/.mate/projects/mihuv2/mate/learnings/hu-110-paste-wire.md:7), [Mate lesson](/Users/erics/workspace/.mate/projects/mihuv2/mate/memory.md:19), [HU-114 known limitation](/Users/erics/workspace/.mate/projects/mihuv2/crews/hu-114-pick-wire/brief.md:23), [HU-114 acceptance](/Users/erics/workspace/.mate/projects/mihuv2/crews/hu-114-pick-wire/brief.md:42), [HU-114 review result](/Users/erics/workspace/.mate/projects/mihuv2/crews/hu-114-r1/report.md:22).

### HU-111/HU-112 → HU-114: handoff API knowledge becomes dispatch context

The editor handback lists exact room inputs and `onPickToRemove`; the Board handback lists `BoardRoom`, `pickToRemove` and undo semantics. HU-114's brief copies these interfaces, sources and call-site requirements into What we already know, rather than leaving the next worker to rediscover them. This is the strongest concrete evidence of knowledge routing into the next task.

Sources: [HU-111 handback](/Users/erics/workspace/.mate/projects/mihuv2/crews/hu-111-editor-limits/handback.md:4), [HU-112 handback](/Users/erics/workspace/.mate/projects/mihuv2/crews/hu-112-board-pick/handback.md:4), [HU-114 brief](/Users/erics/workspace/.mate/projects/mihuv2/crews/hu-114-pick-wire/brief.md:20).

### Simulator limitations → explicit demo launch route

HU-110 recorded a temporary launch-argument probe to capture a flow without a tap tool. HU-114 Build explicitly permits/requires a DEBUG launch argument or test route for screenshots, and the deliverable records `-pickDemo pick|resolved`, in-memory state and timing. The learning record nominates this for later integration UI briefs. This is a concrete implementation/verification technique transferred; whether HU-53b reuses it has not yet been checked here.

Sources: [HU-110 probe](/Users/erics/workspace/.mate/projects/mihuv2/crews/hu-110-paste-wire/handback.md:8), [HU-114 screenshot instruction](/Users/erics/workspace/.mate/projects/mihuv2/crews/hu-114-pick-wire/brief.md:36), [HU-114 implementation handoff](/Users/erics/workspace/.mate/projects/mihuv2/crews/hu-114-pick-wire/handback.md:7), [HU-114 learning](/Users/erics/workspace/.mate/projects/mihuv2/mate/learnings/hu-114-pick-wire.md:5).

### Review instructions become more specific, but incompletely

Mate's sent.log shows targeted instructions: HU-110 r3 at 22:07 says not to rerun a passed gate absent a diff-based reason; HU-111 r2 at 23:12 says to reuse full-gate evidence and run a focused NoteEditor test with nested-suite syntax. Memory now records queuing gate instructions and exceptions at review start. These are actual messages to reviewers, not only template text. Later reviewers still ran full gates, so the narrower policy is not consistent.

Sources: [HU-110 r3 message](/Users/erics/workspace/.mate/projects/mihuv2/sent.log:1481), [HU-111 r2 message](/Users/erics/workspace/.mate/projects/mihuv2/sent.log:1520), [review lesson](/Users/erics/workspace/.mate/projects/mihuv2/mate/memory.md:21).

## What is not working yet

### 1. Task records are not consistently refreshed to closure evidence

HU-110 record calls itself an open-task snapshot observed at 21:15 and names only r1/r2. The task later finished after r3, and its meta records three reviewers. HU-108 record likewise remains explicitly provisional. These records are useful, but they do not demonstrate the expected final-revision catch-up. Filesystem modification time alone cannot prove ordering, so this finding rests on record content versus final metadata, not mtimes.

Sources: [HU-110 stale snapshot](/Users/erics/workspace/.mate/projects/mihuv2/mate/learnings/hu-110-paste-wire.md:2), [HU-110 final metadata](/Users/erics/workspace/.mate/projects/mihuv2/crews/hu-110-paste-wire.meta), [HU-108 provisional record](/Users/erics/workspace/.mate/projects/mihuv2/mate/learnings/hu-108-sizepicker.md:2).

### 2. Measurement coverage is weak and comparisons are not matched

HU-109 says metrics not pulled; HU-112/113/114 say usage not pulled; HU-111 has a rough budget observation rather than final task-total usage. HU-108 and HU-110 include partial/provisional crew-only figures, excluding reviewer usage. The notes therefore cannot establish a before/after reduction. Unknown cost is correctly labelled, but a bounded result record is not equivalent to a measured improvement. The runtime usage tool was available; the records do not establish why it was omitted.

Sources: [HU-109 metrics](/Users/erics/workspace/.mate/projects/mihuv2/mate/learnings/hu-109-harness-pr.md:3), [HU-111 metrics](/Users/erics/workspace/.mate/projects/mihuv2/mate/learnings/hu-111-editor-limits.md:3), [HU-112 metrics](/Users/erics/workspace/.mate/projects/mihuv2/mate/learnings/hu-112-board-pick.md:3), [HU-113 metrics](/Users/erics/workspace/.mate/projects/mihuv2/mate/learnings/hu-113-pdf-placeholders.md:3), [HU-114 metrics](/Users/erics/workspace/.mate/projects/mihuv2/mate/learnings/hu-114-pick-wire.md:3).

### 3. Repeated full-gate friction is recorded but has not become an effective process change

HU-108 already recorded that a reviewer repeated a 777s gate despite a pass at the same SHA, nominating targeted review verification. HU-110 still repeated it; HU-112 reviewer ran 1903s, and HU-114 reviewer ran 1256s after the crew had passed at the same target (crew 1957s). The records show awareness and some narrower instructions, not a general reduction in redundant gates. See the separate gate audit for exact command coverage and wrong-SHA HU-110 r2 evidence.

Sources: [HU-108 gate lesson](/Users/erics/workspace/.mate/projects/mihuv2/mate/learnings/hu-108-sizepicker.md:8), [HU-112 reviewer gate](/Users/erics/workspace/.mate/projects/mihuv2/crews/hu-112-r1/report.md:18), [HU-114 reviewer gate](/Users/erics/workspace/.mate/projects/mihuv2/crews/hu-114-r1/report.md:13), [HU-114 crew gate](/Users/erics/workspace/.mate/projects/mihuv2/crews/hu-114-pick-wire/handback.md:18), [gate audit](/Users/erics/app/mate/docs/archive/research/mihuv2-gate-audit-2026-10-28.md).

### 4. Learning has not prevented false test-coverage claims

HU-114 reviewer says the AppComposition PickToRemove tests were “Ran by the gate (swift test/iOS build)” while also saying it did not run the targeted simulator test command. The gate's manifest loop omits AppComposition, simulator steps name only NoteRender/Board/NoteEditor, and Mihu scheme testTargets names only MihuSnapshotTests. Therefore that statement is incorrect. The implementer's handback separately attributes 23 tests, so this does not establish that the code was untested; it establishes that the independent reviewer overstated what its full gate verified.

Sources: [incorrect reviewer claim](/Users/erics/workspace/.mate/projects/mihuv2/crews/hu-114-r1/report.md:9), [gate package enumeration](/Users/erics/workspace/mihuv2/mihuv2/scripts/verify.sh:44), [Mihu testTargets](/Users/erics/workspace/mihuv2/mihuv2/ios/project.yml:67), [HU-114 implementer evidence](/Users/erics/workspace/.mate/projects/mihuv2/crews/hu-114-pick-wire/handback.md:13).

### 5. Friction is often retained as traps/backlog rather than closed predictions

The notes have useful next-check pointers, but no normalized improved/unchanged/inconclusive outcome for the inspected efficiency lessons. HU-114 still hit its own swift-format failures, fixed in commit f6a7076, then documented the trap and reported “Friction: none logged.” That is a practical handoff lesson, but not a completed harness feedback loop in the repo friction log. The repo friction log's latest commits remain the upgrade/HU-108 entry, not these later repeated gate/test observations. Captured should be read as stored/routed, not proven effective.

Sources: [format trap](/Users/erics/workspace/.mate/projects/mihuv2/crews/hu-114-pick-wire/handback.md:9), [first gate failed format](/Users/erics/workspace/.mate/projects/mihuv2/crews/hu-114-pick-wire/handback.md:18), [no friction logged](/Users/erics/workspace/.mate/projects/mihuv2/crews/hu-114-pick-wire/handback.md:37), [friction log](/Users/erics/workspace/mihuv2/mihuv2/docs/harness/friction.md). Verified `git show --stat f6a7076` changes four Swift source/test files; `git log -3 -- docs/harness/friction.md` ends at upgrade/HU-108.

## Interpretation and limits

There is evidence of **adoption**, **information routing**, and **a better-specified verification boundary in a later task**. There is not yet evidence of systematic gate optimization, complete closure measurement, or statistically/comparably improved efficiency. One-pass reviews in HU-112/HU-114 are observations across different scopes/models/timings; they are not sufficient to attribute improvement to the upgrade.

The upgrade also explicitly preserves existing gates, and a learning workflow is instructions plus bounded records, not a watcher that changes gates or promotes rules automatically. This audit does not treat unchanged gate scripts as an implementation defect in the learning upgrade itself; it treats recurring unclosed cost as an unresolved outcome.
