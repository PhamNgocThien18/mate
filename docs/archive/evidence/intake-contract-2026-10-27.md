# Intake contract — 2026-10-27

The change moves product intake to Mate and gives a ship a settled contract
for the repository workflow. It does not add a semantic validator: readiness
and decision authority remain Mate's judgement. `brief check` checks shape.

## Deterministic coverage

Validation: `make check`, `make build` and `git diff --check` passed.
The full suite reported 80 expected skips; live model/runtime behaviour
remains unproven. Launch characterization snapshots were updated for the
new prompt bytes and fingerprints.

- `internal/brief/revise_test.go`: preserves captain history and worker rules;
  rejects changed history, a stale draft missing later words, invalid shape,
  a rendered prompt and a change of task kind; handles scout deliverables and
  fenced headings.
- `cmd/mate/brief_revision_test.go`: saves the specification and new captain
  words before one notification; keeps Mate decisions out of captain words;
  refuses invalid/stopped updates without mutation; preserves saved updates
  on send refusal and prints a retry pointer; supplies the revised contract
  to a resting Crew's fresh session.
- Existing asset goldens cover normal/headless Mate, skills, scout and all
  ship sizes/delivery modes. These prove rendering, not model compliance.

## Live scenarios to run

These scenarios have **not** been run with a live model for this change.
Use the same model, effort, repository revision and available evidence when
comparing the old and new prompts; preserve transcripts and usage buckets.

| Input | Expected behaviour | Failure |
| --- | --- | --- |
| An agreed About-page version feature; component path unknown | Ship immediately; Unknown names the implementation lookup | Scout or captain question just to locate a file |
| A button request with two possible destination pages and no decision | Mate asks the one product question and holds the ship | Ship spawns with the unresolved choice |
| Requested loading state; evidence says the user path is synchronous | Settle scope using evidence, or a bounded feasibility scout if evidence is insufficient | Invent a new loading flow, or promise unreachable acceptance |
| PROJECT.md names a design artifact and an earlier failed environment check | Brief carries the usable path, source and relevant limitation | Crew rediscovers the artifact or repeats a known failed attempt without new evidence |
| Repo workflow starts with intake, but delegated brief settles scope | Crew uses the brief for intake and begins scoped inspect; keeps required verification/learning/handoff | Reopens settled product questions or skips required delivery checks |
| Captain narrows a running task | One append with revision updates Build/Acceptance/Open decisions before notification | Contradictory specification, lost captain words or two notifications |
| Crew uncovers a contradiction after dispatch | Report evidence, affected requirement and smallest alternatives; await the authorised decision | Treat “intake complete” as permission to change scope |
| Mate resolves an in-scope implementation constraint | Revise the canonical task with provenance if requirements change; otherwise send an operational hint | Put Mate's words into Captain's words |

For inspect, compare model-call count, bytes read, repeated reads, and input,
cache-read, cache-write and output tokens separately. Count questions whose
answers were already available and requirement contradictions at handoff.
Lower token use is useful only with unchanged acceptance quality. No token
saving or live workflow compliance is claimed from the unit suite.

## Applying the change

Build/use the updated Mate binary. Mate manuals and skills regenerate at
Mate start; newly spawned Crews receive the updated worker template. Existing
Crew briefs remain their recorded launch instructions until explicitly
updated; restarting Mate does not rewrite them. This change does not install
or upgrade a target repository's harness workflow.
