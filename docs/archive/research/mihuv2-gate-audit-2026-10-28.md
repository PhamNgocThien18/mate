# mihuv2 gate audit — 2026-10-28

Scope: read-only audit of mihuv2's local gate and its fit for HU-110. No gate, tests or policies changed; no builds run. Sources are the checked-out repository at `06dcb522233c5a7d03757f9f52e271467a5166f8` and HU-110 target `e40b2c5a23e8102e30e010e832c793b56b6fdcae`.

## Finding

The full gate is defensible as a final regression barrier because the project deliberately has no CI. Applying that entire barrier repeatedly to an unchanged SHA, and making every narrow correction pay for every unrelated module, is excessive. The sharper defect is mismatched coverage: the gate checks backend and unrelated packages but does **not** run `AppCompositionTests`, the suite relevant to HU-110.

These are engineering judgments from the source map below, not measured speedup claims. Runtime durations and latency attribution should come from the task's logs, not script length.

## Actual command graph

`make pre-merge E=integration` executes:

1. Resolve branch/SHA, fetch `origin/main` (offline failure is a warning), verify base exists.
2. Require a clean tree, including untracked files.
3. `make check-full`:
   - `make check`: lint all handwritten Swift, then `scripts/verify.sh`.
   - Run the Mihu scheme's snapshot test bundle on one iPhone 16 simulator.
4. `make check-zone E=integration BASE=origin/main` against the committed diff.
5. Print success for the SHA captured at command entry.

Sources: [Makefile:14](/Users/erics/workspace/mihuv2/mihuv2/Makefile:14), [Makefile:23](/Users/erics/workspace/mihuv2/mihuv2/Makefile:23), [scripts/pre-merge.sh:20](/Users/erics/workspace/mihuv2/mihuv2/scripts/pre-merge.sh:20), [scripts/pre-merge.sh:65](/Users/erics/workspace/mihuv2/mihuv2/scripts/pre-merge.sh:65).

**`E=integration` controls file ownership, not test selection.** `pre-merge` does not pass E to `check-full`; only the final `check-zone` consumes it. `OWNERSHIP` permits AppComposition edits for integration and docs/features edits for all zones. Sources: [scripts/pre-merge.sh:66](/Users/erics/workspace/mihuv2/mihuv2/scripts/pre-merge.sh:66), [OWNERSHIP:22](/Users/erics/workspace/mihuv2/mihuv2/OWNERSHIP:22), [scripts/check-zone.sh:18](/Users/erics/workspace/mihuv2/mihuv2/scripts/check-zone.sh:18).

## Every check inside `verify.sh`

| Step | Concrete work | Fit for HU-110 |
| --- | --- | --- |
| Backend setup | In this worktree, install Composer dependencies if vendor is missing; create `.env` and key if missing | Unrelated to a router/view-only diff; costly on fresh reviewer worktrees |
| Backend lint | Docker Pint over backend | No backend files changed |
| Backend tests | Docker `composer test` → Laravel config clear + PHPUnit | No backend or contract files changed |
| Xcode project drift | Regenerate committed `.xcodeproj`, compare against git | Useful if project/config/files change; no project.yml diff in HU-110 |
| API drift | Compare both backend contract files to iOS copies; delete/regenerate APIClient; compare generated trees | No contracts/generator/APIClient diff in HU-110 |
| macOS package tests | Every Core and Features package, plus APIClient, except simulator-only NoteRender/Board/NoteEditor | Broad shared-module regression checks |
| NoteRender tests | Separate `xcodebuild test` on simulator | No renderer changes in HU-110 |
| Board tests | Separate `xcodebuild test` on simulator | Board is adjacent behavior, but the task changes AppComposition wiring, not Board code |
| NoteEditor tests | Separate `xcodebuild test` on simulator | Adjacent input flow, not edited in HU-110 |
| App build | Build Mihu for generic iOS simulator | Directly valuable: proves changed composition compiles with app dependencies |
| Snapshots (outside verify) | `xcodebuild test -project … -scheme Mihu` on iPhone 16 | Useful UI regression evidence, but not the cover-dismiss integration test |

Sources: [scripts/verify.sh:11](/Users/erics/workspace/mihuv2/mihuv2/scripts/verify.sh:11), [backend/composer.json:49](/Users/erics/workspace/mihuv2/mihuv2/backend/composer.json:49), [scripts/verify.sh:21](/Users/erics/workspace/mihuv2/mihuv2/scripts/verify.sh:21), [scripts/verify.sh:26](/Users/erics/workspace/mihuv2/mihuv2/scripts/verify.sh:26), [scripts/verify.sh:43](/Users/erics/workspace/mihuv2/mihuv2/scripts/verify.sh:43), [scripts/verify.sh:57](/Users/erics/workspace/mihuv2/mihuv2/scripts/verify.sh:57), [scripts/verify.sh:70](/Users/erics/workspace/mihuv2/mihuv2/scripts/verify.sh:70), [scripts/verify.sh:83](/Users/erics/workspace/mihuv2/mihuv2/scripts/verify.sh:83), [scripts/verify.sh:96](/Users/erics/workspace/mihuv2/mihuv2/scripts/verify.sh:96), [scripts/snapshots.sh:12](/Users/erics/workspace/mihuv2/mihuv2/scripts/snapshots.sh:12).

HU-110's final semantic diff is four files: `docs/features/app-router.md`, `AppRootView.swift`, `AppRouter.swift`, `PastedAppearanceTests.swift`. Verified with `git diff --name-only 630a847...e40b2c5`.

## Coverage and execution weaknesses

### 1. Relevant AppComposition tests are absent from full gate

The macOS test loop enumerates only `Core/*`, `Features/*` and APIClient. The simulator tests explicitly name only NoteRender, Board and NoteEditor. The Mihu scheme names only MihuSnapshotTests. AppComposition's manifest itself documents that `verify.sh` does not run its tests yet.

Therefore a green `pre-merge` does not demonstrate that PastedAppearanceTests passed; those require a separate command. This remains true even though AppComposition is compiled as part of Mihu.

Sources: [scripts/verify.sh:44](/Users/erics/workspace/mihuv2/mihuv2/scripts/verify.sh:44), [ios/project.yml:66](/Users/erics/workspace/mihuv2/mihuv2/ios/project.yml:66), [ios/Packages/AppComposition/Package.swift:38](/Users/erics/workspace/mihuv2/mihuv2/ios/Packages/AppComposition/Package.swift:38).

### 2. Broad gate ignores change scope

No changed-file selection, dependency reachability, successful-SHA receipt reuse or distinction between initial and corrective verification exists in the gate runner. Backend tests, contract regeneration, every package and snapshots run on every invocation. This is sensible as a conservative full barrier, excessive as a repeated inner loop for four AppComposition/docs files.

Sources: [scripts/pre-merge.sh:65](/Users/erics/workspace/mihuv2/mihuv2/scripts/pre-merge.sh:65), [scripts/verify.sh:15](/Users/erics/workspace/mihuv2/mihuv2/scripts/verify.sh:15), [scripts/verify.sh:44](/Users/erics/workspace/mihuv2/mihuv2/scripts/verify.sh:44), [Makefile:23](/Users/erics/workspace/mihuv2/mihuv2/Makefile:23).

### 3. Five Xcode invocations and broad feature helper

A complete full gate invokes Xcode for NoteRender tests, Board tests, NoteEditor tests, app build, and snapshots. These are distinct targets/operations, so this is not proof of five complete recompilations: Xcode may reuse products. It is still five invocations and multiple test runner launches. Package generation adds a `swift run` of the pinned OpenAPI generator. A fresh worktree has its own DerivedData, simulator and backend vendor, so reviewers do not inherit all prior setup/build products.

`check-feature F=Board` is narrower for its tests/build, but finishes by running **all** Mihu snapshots, not snapshots selected for F.

Sources: [scripts/verify.sh:64](/Users/erics/workspace/mihuv2/mihuv2/scripts/verify.sh:64), [scripts/verify.sh:77](/Users/erics/workspace/mihuv2/mihuv2/scripts/verify.sh:77), [scripts/verify.sh:90](/Users/erics/workspace/mihuv2/mihuv2/scripts/verify.sh:90), [scripts/verify.sh:104](/Users/erics/workspace/mihuv2/mihuv2/scripts/verify.sh:104), [scripts/snapshots.sh:12](/Users/erics/workspace/mihuv2/mihuv2/scripts/snapshots.sh:12), [ios/Packages/APIClient/scripts/generate.sh:10](/Users/erics/workspace/mihuv2/mihuv2/ios/Packages/APIClient/scripts/generate.sh:10), [scripts/check-feature.sh:35](/Users/erics/workspace/mihuv2/mihuv2/scripts/check-feature.sh:35), [scripts/simulator.sh:6](/Users/erics/workspace/mihuv2/mihuv2/scripts/simulator.sh:6), [Makefile:2](/Users/erics/workspace/mihuv2/mihuv2/Makefile:2).

### 4. Cheap ownership failure runs after the expensive suite

`check-zone` reads the committed changed filenames and applies OWNERSHIP rules, but runs only after check-full succeeds. Reordering this cheap check before the full suite preserves coverage and catches out-of-zone work earlier.

Sources: [scripts/check-zone.sh:18](/Users/erics/workspace/mihuv2/mihuv2/scripts/check-zone.sh:18), [scripts/pre-merge.sh:65](/Users/erics/workspace/mihuv2/mihuv2/scripts/pre-merge.sh:65).

### 5. Gate checks cleanliness/SHA only at entry

The wrapper captures SHA at entry, checks clean tree, runs subprocesses, and prints success using the original SHA; it does not recheck HEAD or clean tree after the suite. `verify.sh` regenerates tracked project/API files in place; drift normally fails its explicit comparisons, but the wrapper has no general protection against concurrent branch switching or other modifications during a long gate. An evidence receipt should bind completion to the same SHA and gate version and final tree state.

Sources: [scripts/pre-merge.sh:20](/Users/erics/workspace/mihuv2/mihuv2/scripts/pre-merge.sh:20), [scripts/pre-merge.sh:58](/Users/erics/workspace/mihuv2/mihuv2/scripts/pre-merge.sh:58), [scripts/pre-merge.sh:75](/Users/erics/workspace/mihuv2/mihuv2/scripts/pre-merge.sh:75), [scripts/verify.sh:22](/Users/erics/workspace/mihuv2/mihuv2/scripts/verify.sh:22), [ios/Packages/APIClient/scripts/generate.sh:10](/Users/erics/workspace/mihuv2/mihuv2/ios/Packages/APIClient/scripts/generate.sh:10).

### 6. Prompt-level serialization is not enforced by these scripts

Simulators and DerivedData are isolated per checkout. No global Xcode scheduling/lock appears in the audited gate/simulator scripts. Separate worktrees can therefore launch their heavy gates at the same time. This source observation does not establish whether an external Mate lane limits concurrency; runtime inventory is needed for that.

Sources: [Makefile:2](/Users/erics/workspace/mihuv2/mihuv2/Makefile:2), [scripts/simulator.sh:3](/Users/erics/workspace/mihuv2/mihuv2/scripts/simulator.sh:3), [scripts/pre-merge.sh:66](/Users/erics/workspace/mihuv2/mihuv2/scripts/pre-merge.sh:66).

### 7. Snapshot gate mixes regression assertions and review artifacts

`MihuSnapshotTests.testRenderGalleries` renders every gallery, compares against reference PNGs and writes images/reports, but explicitly does not fail on visual differences. `MihuComponentSnapshotTests` does fail against baselines and checks Dynamic Type, so it would be wrong to call all snapshots useless. Separating the review-artifact renderer from mandatory regression assertions would avoid regenerating review aids on every narrow correction while keeping the assertions.

Sources: [MihuSnapshotTests.swift:7](/Users/erics/workspace/mihuv2/mihuv2/ios/MihuSnapshotTests/MihuSnapshotTests.swift:7), [MihuSnapshotTests.swift:25](/Users/erics/workspace/mihuv2/mihuv2/ios/MihuSnapshotTests/MihuSnapshotTests.swift:25), [MihuComponentSnapshotTests.swift:8](/Users/erics/workspace/mihuv2/mihuv2/ios/MihuSnapshotTests/MihuComponentSnapshotTests.swift:8).

The separate `DesignSystemTests` SPM target is also outside the macOS manifest loop and the Mihu scheme's listed test targets; snapshot tests importing DesignSystem are not the same as executing that package's test target. Sources: [verify.sh:44](/Users/erics/workspace/mihuv2/mihuv2/scripts/verify.sh:44), [project.yml:66](/Users/erics/workspace/mihuv2/mihuv2/ios/project.yml:66).

## HU-110 runtime evidence

The parent audit supplied these local logs, already checked against task status and transcript:

| Attempt | Checked SHA | Full-gate elapsed | Outcome |
| --- | --- | ---: | --- |
| Implementer initial | `9dc4518` | 713s | pass |
| Reviewer r1 | `9dc4518` | 1218s | pass |
| Implementer after fix | `e40b2c5` | 449s | failed Board tests |
| Implementer rerun | `e40b2c5` | 621s | pass |
| Reviewer r2 | `630a847` | 1208s | pass on base, not reviewed target |

Aggregate is **4209s = 70m9s of gate execution**, with overlap; this is not 70 minutes added serially to task duration. No per-stage timing exists in these summaries, so backend or snapshot savings cannot be estimated separately from them. In particular, the r2 gate does not verify its target `e40b2c5`: it ran after switching the worktree back to base.

Sources: [initial log](/tmp/hu-110-paste-wire-premerge.log), [r1 log](/tmp/hu-110-r1-gate-rerun.log), [failed correction log](/tmp/hu-110-paste-wire-gate.log), [successful correction log](/tmp/hu-110-paste-wire-gate2.log), [r2 log](/tmp/hu-110-r2-gate.log:2).

## What to keep and what to change

Recommended policy, not implemented:

1. **Keep a full final regression barrier.** With no CI it is the only repository-wide executable barrier today. The current policy requires it before merge; this audit does not silently relax that policy.
2. **Run fast validation first:** correct target SHA, clean tree, `diff --check`, ownership, relevant formatting. These are cheap and should fail before building.
3. **Add the missing AppComposition suite.** For HU-110, router tests and any real cover-dismiss test are direct evidence. A broad green suite cannot substitute for them.
4. **Use scoped checks during development/review corrections.** For HU-110: changed Swift lint; focused AppComposition tests at the exact SHA; Mihu integration compile; snapshots/manual evidence when rendering or timing is affected. If shared libraries/contracts/build configuration change, expand to their dependents. Unknown reachability falls back to full.
5. **One reusable full-gate result per reviewed SHA and gate fingerprint.** Reviewer independently verifies the relevant behavior and checks the receipt/log, instead of automatically repeating every backend/codegen/snapshot test. Invalidate the receipt on code/base/gate/toolchain changes according to a defined policy. Never reuse a result from a different SHA.
6. **Rerun the failing targeted test first, then the final barrier when required.** Treat repeated unrelated flaky Board failures as reliability work; do not weaken a failing assertion or silently mark its result passed.
7. **Separate execution from checking:** generator drift should ideally run into a temporary output and compare, so interrupted verification cannot leave tracked generated code half-rewritten.

The policies requiring full local verification are intentional: the repo says there is no CI and that all merges require pre-merge green. Changing validation or the definition of done requires a human decision record under the repo's existing hard-gate policy. The user's present request is an audit; no change or approval flow is needed to provide these findings.

Sources: [AGENTS.md:135](/Users/erics/workspace/mihuv2/mihuv2/AGENTS.md:135) (see “Verification” and “How work merges” in the same file), [HARNESS.md:80](/Users/erics/workspace/mihuv2/mihuv2/HARNESS.md:80), [scripts/pre-merge.sh:2](/Users/erics/workspace/mihuv2/mihuv2/scripts/pre-merge.sh:2).

## Historical comparison and limits

`git diff e40b2c5 -- Makefile scripts/verify.sh scripts/pre-merge.sh scripts/snapshots.sh scripts/check-feature.sh ios/project.yml` returned no differences. The gate layout above therefore also describes HU-110's target commit, not merely today's code.

The repo already records first-worktree setup cost and repeated missing test routing as open friction. These are corroboration of the mechanism, not current benchmark numbers: [docs/harness/friction.md:71](/Users/erics/workspace/mihuv2/mihuv2/docs/harness/friction.md:71), [docs/harness/friction.md:77](/Users/erics/workspace/mihuv2/mihuv2/docs/harness/friction.md:77), [docs/harness/friction.md:83](/Users/erics/workspace/mihuv2/mihuv2/docs/harness/friction.md:83).

No performance benchmark, full gate execution, source edit or gate weakening was performed in this audit.
