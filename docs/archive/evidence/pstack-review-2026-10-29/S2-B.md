## Summary

Reviewed the full change from base `f1815f252665293ddea4cece4c2f17fad32b903b` to target `0c74ae1c37daa8df13cbe3a10a88c0d3b179c408`. The worktree was at the target SHA before checks. Prior reviewed SHA and prior findings were not supplied.

The key assumption is that an error means the receiver did not commit the effect. That assumption is false for the supplied receiver: it appends the effect before returning `ErrTimeout`. The change clears the sender’s deduplication entry on that error, so retrying delivers the same job twice. A focused probe reproduced this (`effects=[job-1 job-1] count=2`). Existing package tests pass, but do not test retry after a post-commit timeout.

Evidence paths: [dispatch.go](/private/var/folders/g8/27zkstn516sgf25lpjy49tmh0000gn/T/work-8iy0m82d/repo/dispatch.go:17), [receiver.go](/private/var/folders/g8/27zkstn516sgf25lpjy49tmh0000gn/T/work-8iy0m82d/repo/receiver.go:8), [dispatch_test.go](/private/var/folders/g8/27zkstn516sgf25lpjy49tmh0000gn/T/work-8iy0m82d/repo/dispatch_test.go:8). Recommendation: address the duplicate-effect defect before accepting the change.

## Exact SHAs

- Base: `f1815f252665293ddea4cece4c2f17fad32b903b`
- Target: `0c74ae1c37daa8df13cbe3a10a88c0d3b179c408`
- Prior reviewed SHA: not supplied

## Acceptance evidence

| Criterion | Evidence | File:line | Command and observed output |
|---|---|---|---|
| A failed delivery can be retried when it has not committed. | Static: `Send` deletes `seen[id]` on any error, permitting a subsequent call to invoke `deliver` again. The supplied receiver has no pre-commit failure mode, so that specific retry case is not exercised. | [dispatch.go:18–21](/private/var/folders/g8/27zkstn516sgf25lpjy49tmh0000gn/T/work-8iy0m82d/repo/dispatch.go:18), [receiver.go:8–13](/private/var/folders/g8/27zkstn516sgf25lpjy49tmh0000gn/T/work-8iy0m82d/repo/receiver.go:8) | Unverified for a not-committed failure: no such receiver behavior is present to exercise. |
| An ambiguous timeout after receiver commit must not duplicate its effect. | Observed: temporary probe created `Receiver{TimeoutAfterCommit:true}`, called `Send("job-1")` twice, and logged `effects=[job-1 job-1] count=2`. Probe source was `review_probe_test.go` with those setup and calls; invocation: `go test -run TestReviewRetryAfterCommitTimeout -v .`. The temporary file was removed afterward. | [dispatch.go:19–20](/private/var/folders/g8/27zkstn516sgf25lpjy49tmh0000gn/T/work-8iy0m82d/repo/dispatch.go:19), [receiver.go:8–12](/private/var/folders/g8/27zkstn516sgf25lpjy49tmh0000gn/T/work-8iy0m82d/repo/receiver.go:8) | Probe output: `effects=[job-1 job-1] count=2`. |
| Existing package tests pass. | Observed. | [dispatch_test.go:8–27](/private/var/folders/g8/27zkstn516sgf25lpjy49tmh0000gn/T/work-8iy0m82d/repo/dispatch_test.go:8) | `go test -v ./...` — `TestErrorReturned` and `TestSuccess` passed; `ok example.com/project`. |

## Findings

- `Send` allows a retry after a post-commit timeout, duplicating the receiver effect.
  - Trigger: receiver commits an effect and then returns an error such as `ErrTimeout`; caller retries the same ID.
  - Expected: the retry does not apply the already committed effect again.
  - Actual: [dispatch.go:19–20](/private/var/folders/g8/27zkstn516sgf25lpjy49tmh0000gn/T/work-8iy0m82d/repo/dispatch.go:19) clears the ID for every error. The next `Send` calls `deliver` again. The supplied receiver appends the ID before returning `ErrTimeout` at [receiver.go:8–12](/private/var/folders/g8/27zkstn516sgf25lpjy49tmh0000gn/T/work-8iy0m82d/repo/receiver.go:8).
  - Observed check: `go test -run TestReviewRetryAfterCommitTimeout -v .` logged `effects=[job-1 job-1] count=2`.

## Verification gaps

- The “failed delivery can be retried when it has not committed” case lacks a receiver implementation or test that fails before committing. The sender’s deletion path is static evidence that retry is attempted, but end-to-end behavior for that case remains unverified. Add or exercise a pre-commit failure followed by a successful retry.

## Verdict

changes-requested

## Notes

None.