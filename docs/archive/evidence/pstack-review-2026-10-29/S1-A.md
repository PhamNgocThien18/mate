## Summary

The full committed change is a one-line change to `Save`. It changes expiry serialization from Unix milliseconds to Unix seconds, but `Load` still interprets the stored number as milliseconds. As a result, a saved expiry is restored near the start of 1970; the value and exact expiry do not survive a round trip, and the entry is unavailable at current dates.

I ran `go test -v ./...`; it passed, but the only test checks that a file is created and looks up the original in-memory map. It does not test `Load`. The existing package tests therefore do not support the acceptance claim.

Evidence: `cache.go`, `restore.go`, `cache_test.go`, and the hand-back at `.mate/projects/shop/crews/change/handback.md`.

**Base:** `38b3a7567e3476c048a9353224117f6ce9201e41`  
**Target:** `bf836a9053dfde30cc4c64abd810a9ca240af9af`

## Verdict

changes-requested

## Acceptance evidence

| Acceptance criterion | Evidence |
|---|---|
| A cache entry keeps its value and exact expiry through Save then Load, and remains available before that expiry. | **Not met.** `cache.go:21` writes `Expires.Unix()` (seconds), while `restore.go:20` reads it with `time.UnixMilli()` (milliseconds). The restored expiry is therefore far earlier than the original. `cache_test.go:10-23` does not call `Load`; it checks file creation and looks up the original map. |
| Existing package tests pass. | **Pass.** I ran `go test -v ./...` at the target SHA. Output: `=== RUN TestSave`, `--- PASS: TestSave`, `PASS`, `ok example.com/project 0.093s`. The hand-back reports a pass too, but its displayed timing differs; it does not demonstrate round-trip coverage. |

## Findings

- `cache.go:21` — Save and Load use incompatible expiry units, breaking persistence.
  - `Save` now serializes Unix seconds, while `Load` continues to interpret the integer as Unix milliseconds in `restore.go:20`.
  - A loaded entry’s expiry is near 1970, so it is expired at current dates. The entry does not retain its exact expiry or remain available before it.
  - The passing `TestSave` in `cache_test.go:10-23` does not exercise this path.

## Notes

None.