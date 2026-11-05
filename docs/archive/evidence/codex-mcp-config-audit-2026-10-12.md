# Codex MCP config injection audit (2026-10-12)

Task 1b of `docs/archive/plans/plugins-and-office-2026-10-12.md`. Offline parser probe
of whether codex-cli accepts `-c mcp_servers.<name>.*` per launch under
`--strict-config`. Companion to `claude-mcp-probe-2026-10-12.md`. No Go changed.

- Version: `codex --version` -> `codex-cli 0.162.1`
- Every command ran with `CODEX_HOME` set to a fresh `mktemp -d` (the codexlab
  approach: an isolated home so nothing reads the operator's config or login),
  `OPENAI_API_KEY`/`CODEX_API_KEY` unset, cwd `/tmp`, stdin `/dev/null`, a
  15 s `alarm` as hard timeout. No login, no TUI.

## Method note (what does not measure strict parsing)

- `--help` never evaluates config: `codex --strict-config -c totally_unknown=1 exec --help`
  prints help and exits 0. So `... --strict-config exec --help` (the brief's first
  suggestion) proves nothing about key acceptance.
- `--strict-config` is rejected outright by non-agent subcommands:
  `Error: \`--strict-config\` is not supported for \`codex mcp\`` (same text for
  `codex features`, `codex login`, `codex debug`). So `codex mcp list` cannot
  verify strict acceptance.
- What does evaluate it: `codex --strict-config <overrides> exec --skip-git-repo-check x`.
  Control: `-c totally_unknown=1` -> rc=1,
  `Error loading config.toml: unknown configuration field \`totally_unknown\` in -c/--config override`.

Caveat on side effects: these `exec` probes got past config load and made one
unauthenticated request to the OpenAI endpoint, which answered
`401 Unauthorized` (`wss://api.openai.com/v1/responses`). No credential existed
to send; no model response was obtained. This went beyond the brief's "no API
call" and is recorded for honesty.

## Results (codex --strict-config ... exec --skip-git-repo-check x)

| `-c` overrides | Result |
|---|---|
| `mcp_servers.probe.url=http://127.0.0.1:1/mcp` | Accepted. Codex started and tried the server: `ERROR rmcp::transport::worker: ... http/request failed: error sending request for url (http://127.0.0.1:1/mcp)`. Process then hit the 15 s alarm (rc=142) on the 401 retry loop. |
| `mcp_servers.probe.command=/bin/true` | Accepted (started; 401 from OpenAI, alarm rc=142). |
| `url` + `bearer_token_env_var=PROBE_TOK` + `startup_timeout_sec=5` + `enabled=true` | Accepted (same). |
| `url` + `command=/bin/true` | rc=1 `url is not supported for stdio in \`mcp_servers.probe\`` (transport mixing refused). |
| `mcp_servers.probe.bogus=1` | rc=1 `invalid transport in \`mcp_servers.probe\`` (no url/command). |
| `url` + `mcp_servers.probe.bogus=1` | rc=1 `unknown configuration field \`mcp_servers.probe.bogus\` in -c/--config override`: strict mode checks keys inside the dotted table. |
| `check_for_update_on_startup=false` + `project_doc_max_bytes=65536` + `mcp_servers.probe.url=...` (the two overrides Mate already passes) | Accepted, same as the url-only case. |

Without `--strict-config`, `-c mcp_servers.probe.bogus=1` fails the same
`invalid transport` way: a dotted table is validated either way.

Offline `codex -c mcp_servers.probe.url=... mcp list` / `mcp get probe` (not
strict, config view only) also lists the entry:
`probe  http://127.0.0.1:1/mcp  - enabled Unknown` with `transport: streamable_http`;
for `command=/bin/true`: `Auth Unsupported`.

## Findings per item

1. Parser accepts dotted `mcp_servers.<name>.url` / `.command` / `.bearer_token_env_var`
   / `.startup_timeout_sec` / `.enabled` under `--strict-config`, and still refuses
   unknown sub-keys. Measured: YES.
2. How Mate passes overrides today (`internal/harness/codex/codex.go`, ~line 367):
   args are `--dangerously-bypass-approvals-and-sandbox -c check_for_update_on_startup=false
   -c project_doc_max_bytes=<N>`, then `-m <model>` and
   `-c model_reasoning_effort="<effort>"` when supported; resume appends the id after
   all flags. Strict config is not currently passed by the launcher (grep finds
   `--strict-config` only in comments describing the measurements). Overrides are
   per-launch args, never written into `config.toml`. `mcp_servers.<instance>.*`
   would be additional `-c` pairs appended in the same slice; the launcher has no
   input for them today (`LaunchSpec` carries no MCP field).
3. Tool naming: `ClassifyCodexTool` (`transcript.go` ~line 713) treats any name with
   prefix `mcp__` as `CommandMCP`, i.e. the same `mcp__<server>__<tool>` convention
   Claude uses. That is only the classifier's expectation; no Codex run here
   reached a real MCP tool call, so the actual emitted name is NOT measured.
4. Auth: NOT measured. Only a bearer env var key was parsed; no server was
   reachable, no OAuth (`codex mcp login`) flow was run.
5. Resume: NOT measured. `codex resume` with these overrides was not run beyond
   `--help`, and Mate's resume path (id last, same flags) is untested with MCP.
6. A real tool read through a Codex pane: NOT measured.

## Conclusion

Codex: unsupported for plugin config in the pilot. The parser prerequisite is met
(dotted `mcp_servers` keys pass strict-config), but nothing past parsing is proven.

Conditions to enable:
- Launcher gains an explicit `mcp_servers` input that emits the `-c` pairs and
  is covered by an argv test.
- Live gate in a real Mate Codex pane under the exact launch flags: server
  connects, a tool is listed and a read tool returns a result (recording,
  version, redacted argv).
- Observed tool name recorded and matched against `ClassifyCodexTool`.
- Auth measured for the chosen transport (bearer env var passing through the
  pane environment, or OAuth with an isolated `CODEX_HOME`).
- Resume proven with the same overrides.
