## Summary

The checkout was at target SHA `55e2bc22823a191886e390ec3189d0a937a633f9` before checks. The change saves expiry timestamps in seconds, while `Load` still interprets them as milliseconds. A round-trip probe showed that an expiry in 2026 loaded as a date in 1970 and the entry was unavailable before its intended expiry. The existing package test passes but does not call `Load`. Recommendation: correct the timestamp-unit mismatch and add round-trip coverage.

Evidence paths: `cache.go`, `restore.go`, `cache_test.go`.

## SHAs

- Base: `4985daeebad994b5ae21be9d49c67ad7e2c15397`
- Target: `55e2bc22823a191886e390ec3189d0a937a633f9`
- Prior reviewed SHA: none recorded

## Acceptance evidence

| Criterion | Evidence |
|---|---|
| An entry keeps its value and exact expiry through Save then Load, and remains available before that expiry. | **Observed failure.** `cache.go:21` serializes with `Unix()`; `restore.go:20` decodes with `time.UnixMilli()`. Temporary probe `TestReviewerRoundTrip`, run with `go test -run TestReviewerRoundTrip -v .`, logged expiry `2026-10-29T12:34:56.789Z`, loaded expiry `1970-01-22T02:07:57.296+08:00`, and `lookup-before-expiry=false`; test failed with `expiry changed`. Probe source was a temporary `cache_review_test.go`, removed after the run. |
| Existing package tests pass. | **Observed pass.** `go test -v ./...` at target SHA printed `PASS` and `ok example.com/project 0.095s`. |

## Findings

- `Save` and `Load` use incompatible timestamp units, breaking persistence and early cache availability. Trigger: save an entry with a normal modern expiry, then load it. `Save` writes Unix seconds (`cache.go:21`), but `Load` reads that integer as Unix milliseconds (`restore.go:20`); `Lookup` consequently considers the loaded entry expired (`restore.go:24-29`). Expected: loaded value and exact expiry match the saved entry, and lookup succeeds before that expiry. Actual: the expiry becomes a 1970 date and lookup fails. Observed with `go test -run TestReviewerRoundTrip -v .` as reported above.

## Verification gaps

none

## Verdict

changes-requested

## Notes

none