# mate agent notes

- `docs/mvp.md` is history: the original design decisions and the M0–M21 task log. Don't read it up front.
  When code cites it (`mvp.md section 4b`, `M18`), grep for that section. Don't add new entries to it.
- Issues and tasks live in Linear: team `EricsNguyen` (key `CON`), project `mate`. Use the `linear` MCP server from `.mcp.json`. Teams `HVN` and `MIHU` belong to projects mate manages, not to this repo.
- Module `github.com/nguyenngocanh94/mate`. Binary `cmd/mate`.
- `make check` runs gofmt, `go vet`, and `go test -json ./...` through `scripts/gotestreport`.
  Any skipped test not declared in `scripts/gotestreport/expected-skips.txt` fails the build.
  Live Herdr/harness tests are named `TestLive*` and skip unless `MATE_LIVE=1`.
  A green suite that skipped live tests is not evidence those features work.
- This repo was matev2 until 2026-09-24; v1 lives only in the archived GitHub repo `nguyenngocanh94/mate-v1`. Dated evidence and research under `docs/` keep the old `matev2`/`.matev2` names. firstmate reference: upstream https://github.com/kunchenguid/firstmate (the local clone at `/Volumes/Work/Workspace/firstmate` was 665 commits behind on 2026-09-24; read upstream, never pull into the user's clone).
- `docs/evidence`, `docs/plans` and `docs/research` hold only dated docs that code still cites. Once nothing cites one, move it to `docs/archive/` with `git mv`. Don't link new docs or code to `docs/archive/`.
- Env var prefix is `MATE_`, state dir is `.mate/`. Never `MATEV2_` or `.matev2/` outside dated docs.
- Commit messages: terse, imperative. Never add an agent name as co-author.
