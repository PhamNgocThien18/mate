## Summary

The full committed change is one edit to `Dispatcher.Send`. Existing package tests pass at the target SHA, but an independent retry probe shows that retrying a timeout after the receiver has committed appends the same effect twice. That fails the at-most-once acceptance criterion.

Evidence: `go test -v ./...` passed; the temporary retry probe logged `effects after ambiguous timeout and retry: [job-1 job-1]`. Both were run in the specified checkout. The probe file was removed; the checkout is clean.

## Verdict

changes-requested

## Acceptance evidence

| Acceptance criterion | Evidence and result |
|---|---|
| A failed delivery can be retried when it has not committed. | **Gap.** `dispatch.go:19-20` clears `seen` for every error, but the existing receiver only models a timeout after commit. No test or implementation evidence verifies retry after a pre-commit failure. |
| An ambiguous timeout after receiver commit must not duplicate its effect. | **Failed.** In `receiver.go:8-11`, `Receive` appends the effect before returning `ErrTimeout`. A temporary retry probe called `Send("job-1")` twice and logged `[job-1 job-1]`; the marker is cleared in `dispatch.go:20`. |
| Existing package tests pass. | **Passed.** Ran `go test -v ./...` at the target SHA; output: `TestErrorReturned` and `TestSuccess` passed; `ok example.com/project`. |

## Exact SHAs and scope

- Base: `902dd5dd2fa96b22afc4f05564fbbd7335cb498f`
- Target: `5d44b346ccf11872ef42d684aa4c758f77d8dcb5`
- Inspected full diff: `git diff 902dd5dd2fa96b22afc4f05564fbbd7335cb498f...5d44b346ccf11872ef42d684aa4c758f77d8dcb5`

## Findings

- `dispatch.go:19-20` clears the deduplication marker on every delivery error, so a retry after an ambiguous timeout can repeat an effect already committed by the receiver.
  - `receiver.go:8-11` demonstrates the committed-timeout case: it appends to `Effects` before returning `ErrTimeout`.
  - Independent retry probe observed `[job-1 job-1]` after two sends.
  - This violates the at-most-once acceptance criterion.

## Notes

- The hand-back reports only the existing test run. Those tests do not retry after a timeout or assert receiver effects in that case.