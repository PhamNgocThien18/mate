## Summary

The README correction closes the prior finding: it now documents `go run . --format json`, matching the flag parsed by the program. The JSON output path is supported by the implementation and prior runtime evidence. This delta changes documentation only; the prior review’s runtime evidence is inherited.

**Evidence paths:** [README.md](/private/var/folders/g8/27zkstn516sgf25lpjy49tmh0000gn/T/work-qzxe8i16/repo/README.md:3), [main.go](/private/var/folders/g8/27zkstn516sgf25lpjy49tmh0000gn/T/work-qzxe8i16/repo/main.go:9)

## Exact SHAs

- Base: `e6e00f29895049d866a427bfad17b1f4f69559a4`
- Prior reviewed SHA: `c32787fa4dc13a8b81d3dd7bad7f021f9b06e422`
- Target: `e8cf1ce883889ef9561cecb4708d33ebef2b17db`
- Checkout HEAD observed at target SHA.

## Prior findings

- F1 closed — [README.md:3](/private/var/folders/g8/27zkstn516sgf25lpjy49tmh0000gn/T/work-qzxe8i16/repo/README.md:3). Observed: the README now documents `go run . --format json`. Static: [main.go:9–15](/private/var/folders/g8/27zkstn516sgf25lpjy49tmh0000gn/T/work-qzxe8i16/repo/main.go:9) parses `--format` and emits JSON for the `json` value. Inherited runtime evidence from r1 at `c32787fa4dc13a8b81d3dd7bad7f021f9b06e422`: `go run . --format json` produced `{"status":"ok"}`.

## Acceptance evidence

| Criterion | Evidence | File:line | Command and output |
|---|---|---|---|
| The documented command produces JSON. | Static contract match; inherited runtime confirmation. | [README.md:3](/private/var/folders/g8/27zkstn516sgf25lpjy49tmh0000gn/T/work-qzxe8i16/repo/README.md:3), [main.go:9–15](/private/var/folders/g8/27zkstn516sgf25lpjy49tmh0000gn/T/work-qzxe8i16/repo/main.go:9) | Inherited from r1 at `c32787fa4dc13a8b81d3dd7bad7f021f9b06e422`: `go run . --format json` → `{"status":"ok"}`. |
| Runtime command and output behaviour remain unchanged; use prior review runtime evidence for this docs-only correction. | Observed delta is README-only; runtime evidence inherited from r1. | [README.md:3](/private/var/folders/g8/27zkstn516sgf25lpjy49tmh0000gn/T/work-qzxe8i16/repo/README.md:3) | `git diff c32787fa4dc13a8b81d3dd7bad7f021f9b06e422...e8cf1ce883889ef9561cecb4708d33ebef2b17db` → README.md, 1 insertion and 1 deletion. Inherited from r1: `go test -v ./...` passed; `go run . --format json` produced `{"status":"ok"}`. |

## Findings

none

## Verification gaps

none

## Verdict

pass

## Notes

none