# Claude MCP probe dưới cờ launch Mate — 2026-10-12

Task 1a của `docs/archive/plans/plugins-and-office-2026-10-12.md` (mục 3, Claude). Đo,
không suy từ help: một MCP server stdio có đúng một tool `read_probe`
(`scripts/mcpfixture`), chạy `claude -p` headless dưới đúng cờ Mate trong
`internal/harness/claude/claude.go` (RoleMate), cwd là temp dir rỗng.

- Claude Code: `2.1.287 (Claude Code)`, macOS, tài khoản captain (login keychain).
- Repo: branch `feat/plugin-pilot`, base `52e4073`.
- Lệnh: `MATE_LIVE=1 go test ./internal/harness/claude -run TestLiveClaudeMCPProbe -v -count=1`
- Kết quả: `--- PASS: TestLiveClaudeMCPProbe (33.09s)`, cả 4 subtest PASS, `exit=0`.
- Config MCP (path redacted): `{"mcpServers":{"probe-inst":{"command":"<tmp>/mcpfixture","args":[],"env":{"MCP_FIXTURE_LOG":"<tmp>/<run>/fixture.log"}}}}`
- `<tmp>` = `t.TempDir()` của test. Không có token, secret hay nội dung config dir
  trong file này.

## Kết luận

| Câu hỏi của plan | Kết quả đo |
| --- | --- |
| `--tools Bash,...,Skill` có chặn MCP tool không? | **Không.** Biến thể A (cờ Mate nguyên trạng) liệt kê `mcp__probe-inst__read_probe` ở init, model gọi nó ngay lượt đầu, fixture nhận `tools/call` id `abc123`, kết quả `probe:abc123:ok`. `--tools` chỉ lọc built-in. |
| `--strict-mcp-config` + `--mcp-config` có load server không? | **Có.** Mọi lượt: `mcp_servers: [{"name":"probe-inst","source":"dynamic","status":"connected"}]`; fixture nhận `server/discover`, `initialize`, `notifications/initialized`, `tools/list`, `tools/call`. |
| Global server có lọt không? | **Không lọt.** `claude mcp list` trên máy liệt kê 2 server global (`plugin:playwright:playwright`, `plugin:text-to-cad:cad`). Trong cả 5 lượt probe, init chỉ có `mcp__probe-inst__read_probe` và server `probe-inst`. |
| `--resume` có giữ MCP không? | **Có**, dưới cờ C: lượt `--session-id <uuid>` gọi tool (`abc123`), lượt `--resume <uuid>` với cùng `--mcp-config` khởi động lại server (fixture nhận `initialize` mới) và gọi tool với `resume456` → `probe:resume456:ok`. |
| Hooks/skills | **Chưa đo** trong probe này: cwd rỗng, không có `.claude/` project nên không có hook/skill để quan sát. `Skill` vẫn có trong built-in tools ở A/C. |

**PR 4 cần làm với `--tools`:** không cần bỏ allowlist và không bắt buộc thêm
tên MCP vào `--tools`; chỉ thêm `--mcp-config <file>` là MCP tool tới được Mate
(biến thể A). Khuyến nghị giữ nguyên `--tools Bash,Read,Write,Edit,Glob,Grep,Skill`
để policy built-in không đổi. Thêm tên `mcp__<server>__<tool>` vào `--tools`
(biến thể C) không có hại và cho kết quả giống A; chỉ dùng nếu PR 4 muốn ghi
rõ tool MCP trong argv, không phải để mở khoá. Bỏ `--tools` (biến thể B) làm lộ
~26 built-in (Task, WebFetch, Workflow, ...) và khiến MCP tool bị defer sau
`ToolSearch` (model phải gọi `ToolSearch` trước), nên không nên chọn.

Kết luận này chỉ phủ `claude -p` headless với stdio server. Pane interactive
thật (Herdr) và HTTP/OAuth server (Linear) vẫn là gate riêng của plan.

## Từng biến thể

Mọi lượt chung các cờ: `--dangerously-skip-permissions --setting-sources project
--strict-mcp-config --autocompact 300000 --mcp-config <tmp>/<run>/mcp.json
--output-format json --verbose`. Prompt 1 (cả 4 biến thể đều thành công ở prompt
1, không cần prompt dự phòng):

```
Call the tool mcp__probe-inst__read_probe with id "abc123" and reply with exactly the text it returns, verbatim, nothing else.
```

`--verbose` được thêm vào `--output-format json` mà brief nêu: không có nó stdout
chỉ là result event; có nó stdout là mảng mọi message, gồm event `system/init`
với danh sách tool và `mcp_servers` — cần để đo discovery và leak trực tiếp thay
vì suy từ câu trả lời của model. Result event vẫn có trong mảng.

### A — cờ Mate nguyên trạng

```
claude -p '<prompt 1>' --dangerously-skip-permissions --setting-sources project --strict-mcp-config --tools Bash,Read,Write,Edit,Glob,Grep,Skill --autocompact 300000 --mcp-config <tmp>/A_mate_flags_as_is-prompt1/mcp.json --output-format json --verbose
```

- exit code `0`; `is_error:false`, `subtype:success`, `num_turns:2`, `permission_denials:[]`
- init built-in tools: `Bash Edit Glob Grep Read Skill Write`
- init MCP tools: `mcp__probe-inst__read_probe`
- assistant tool_use: `mcp__probe-inst__read_probe {"id":"abc123"}`
- tool_result: `[{"text":"probe:abc123:ok","type":"text"}]`
- result: `"probe:abc123:ok"`
- fixture: `server/discover, initialize, notifications/initialized, tools/list, tools/call`
- fixture tools/call: `{"method":"tools/call","params":{"name":"read_probe","arguments":{"id":"abc123"},"_meta":{"claudecode/toolUseId":"toolu_012tKKzuRzHrC26ZD8uUjHcJ","progressToken":2}},"jsonrpc":"2.0","id":2}`
- **Phán quyết: tool được gọi.**

### B — bỏ `--tools`

```
claude -p '<prompt 1>' --dangerously-skip-permissions --setting-sources project --strict-mcp-config --autocompact 300000 --mcp-config <tmp>/B_no_tools_flag-prompt1/mcp.json --output-format json --verbose
```

- exit code `0`; `is_error:false`, `num_turns:3`
- init built-in tools: `Task Bash CronCreate CronDelete CronList DesignSync Edit EnterWorktree ExitWorktree ListAgents Monitor NotebookEdit PushNotification Read RemoteTrigger ReportFindings ScheduleWakeup SendMessage ShareOnboardingGuide Skill TaskStop ToolSearch WebFetch WebSearch Workflow Write`
- init MCP tools: `mcp__probe-inst__read_probe`
- assistant tool_use 1: `ToolSearch {"max_results":1,"query":"select:mcp__probe-inst__read_probe"}` → `[{"tool_name":"mcp__probe-inst__read_probe","type":"tool_reference"}]`
- assistant tool_use 2: `mcp__probe-inst__read_probe {"id":"abc123"}` → `probe:abc123:ok`
- result: `"probe:abc123:ok"`
- fixture: `server/discover, initialize, notifications/initialized, tools/list, tools/call`
- fixture tools/call: `{"method":"tools/call","params":{"name":"read_probe","arguments":{"id":"abc123"},"_meta":{"claudecode/toolUseId":"toolu_017kH93Jxb9JR8mAxHS6WF94","progressToken":2}},"jsonrpc":"2.0","id":2}`
- **Phán quyết: tool được gọi** (qua `ToolSearch` trước).

### C — `--tools` cộng tên MCP tool

```
claude -p '<prompt 1>' --dangerously-skip-permissions --setting-sources project --strict-mcp-config --tools Bash,Read,Write,Edit,Glob,Grep,Skill,mcp__probe-inst__read_probe --autocompact 300000 --mcp-config <tmp>/C_tools_plus_mcp_name-prompt1/mcp.json --output-format json --verbose
```

- exit code `0`; `is_error:false`, `num_turns:2`
- init built-in tools: `Bash Edit Glob Grep Read Skill Write` (giống A)
- init MCP tools: `mcp__probe-inst__read_probe`
- assistant tool_use: `mcp__probe-inst__read_probe {"id":"abc123"}` → `probe:abc123:ok`
- result: `"probe:abc123:ok"`
- fixture: `server/discover, initialize, notifications/initialized, tools/list, tools/call`
- fixture tools/call: `{"method":"tools/call","params":{"name":"read_probe","arguments":{"id":"abc123"},"_meta":{"claudecode/toolUseId":"toolu_013xhiZCk1C9iSnJi7Asd5tW","progressToken":2}},"jsonrpc":"2.0","id":2}`
- **Phán quyết: tool được gọi.**

### D — `--session-id` rồi `--resume`, cờ C

```
claude -p '<prompt 1>' --session-id fc5f0046-2a3a-4f9c-9257-4636c1604500 --dangerously-skip-permissions --setting-sources project --strict-mcp-config --tools Bash,Read,Write,Edit,Glob,Grep,Skill,mcp__probe-inst__read_probe --autocompact 300000 --mcp-config <tmp>/D-fresh/mcp.json --output-format json --verbose
claude -p 'Call the tool mcp__probe-inst__read_probe again, this time with id "resume456", and reply with exactly the text it returns.' --resume fc5f0046-2a3a-4f9c-9257-4636c1604500 --dangerously-skip-permissions --setting-sources project --strict-mcp-config --tools Bash,Read,Write,Edit,Glob,Grep,Skill,mcp__probe-inst__read_probe --autocompact 300000 --mcp-config <tmp>/D-resume/mcp.json --output-format json --verbose
```

- fresh: exit `0`, tool_use `{"id":"abc123"}` → `probe:abc123:ok`, fixture nhận `tools/call`.
- resume: exit `0`, cùng `session_id`, init lại có `probe-inst` connected và
  `mcp__probe-inst__read_probe`; tool_use `{"id":"resume456"}` →
  `probe:resume456:ok`; fixture (log riêng của lượt resume) nhận `initialize` và
  `tools/call` id `resume456`.
- **Phán quyết: tool được gọi ở cả hai lượt.** Hai lượt cùng một cwd vì CLI tìm
  session theo project dir của cwd. D chỉ chạy dưới cờ C, không lặp dưới cờ A.

## Phát hiện phụ (đo tay, ngoài test)

1. **Client MCP của 2.1.287 gửi `server/discover` trước `initialize`.** Nó mang
   `_meta."io.modelcontextprotocol/protocolVersion": "2026-07-28"`. Fixture trả
   -32601 và client quay về `initialize` với `protocolVersion: "2025-11-25"`.
   Server plugin nào trả lỗi khác cho method lạ cần được kiểm riêng.
2. **`--setting-sources project` làm mất `apiKeyHelper` và `env` của user
   settings.** Trên máy này `~/.claude/settings.json` có `apiKeyHelper` và
   `env.ANTHROPIC_BASE_URL`. Khi chạy từ trong một phiên Claude Code, process con
   thừa hưởng `ANTHROPIC_BASE_URL` (proxy) nhưng không có `apiKeyHelper`, nên
   login keychain bị gửi tới proxy và nhận `401 {"error":"Invalid API key"}`.
   CLI retry (11 lần, backoff) và treo quá 120s. Debug log: `API error (attempt
   5/11): 401`. Không phụ thuộc MCP: `claude -p "Reply ok." --setting-sources
   project` một mình cũng treo; bỏ `ANTHROPIC_BASE_URL` khỏi env thì xong trong
   ~4s. Test vì vậy chạy claude với env đã bỏ biến của phiên Claude cha
   (`ANTHROPIC_BASE_URL`, `ANTHROPIC_DEFAULT_*`, `CLAUDECODE`, `CLAUDE_CODE_*`,
   `CLAUDE_PID`, `CLAUDE_EFFORT`, `AI_AGENT`); `CLAUDE_CONFIG_DIR` giữ nguyên.
   Hệ quả cho Mate: một Mate launch mà env có `ANTHROPIC_BASE_URL` trỏ tới proxy
   cần key từ user settings sẽ treo ở lượt đầu. Chưa đo trong pane Herdr thật.
3. Lượt thử tay `--setting-sources project` **không** `--strict-mcp-config` có
   kết nối claude.ai connector ("claude.ai Claude Docs") qua mcp-proxy, tức đó
   là nguồn MCP global mà `--strict-mcp-config` đang chặn. Với strict, không lượt
   probe nào thấy connector.

## PR 4 verification

Task 4 nối `--mcp-config` vào launch Mate thật (`Claude{}.Prepare` ghi
`<mate>/mcp-plugins.json`, `Build` thêm `--mcp-config <file>` cuối argv, fresh
lẫn `--resume`; `--tools` và `--strict-mcp-config` giữ nguyên theo kết luận ở
trên). Đo lại trên chính argv do `Build` sinh.

- Claude Code `2.1.287 (Claude Code)`, macOS, 2026-10-12, branch `feat/plugin-pilot`.
- Lệnh: `MATE_LIVE=1 go test ./internal/harness/claude -run TestLiveClaudeMateLaunchMCP -v -count=1`
- Kết quả: `--- PASS: TestLiveClaudeMateLaunchMCP (16.38s)`, `exit=0`.
- Server: một `harness.MCPServer` stdio `probe-inst`, `Command: ["./mcpfixture"]`,
  `Cwd: <tmp>/pkg` (thư mục chứa fixture); cwd của Mate là `<tmp>/mate`, không
  có fixture, nên server chỉ lên được nếu chạy đúng trong `Cwd` của nó.
- File do `Prepare` sinh (path redacted):
  `{"mcpServers":{"probe-inst":{"command":"/bin/sh","args":["-c","cd \"$1\" && shift && exec \"$@\"","mate-mcp-cwd","<tmp>/pkg","./mcpfixture"]}}}`
  (in thụt lề trong file thật). Không env, không token.

| Lượt | argv (sau `claude -p '<prompt>'`, cộng `--output-format json --verbose`) | init | tool_use → result | fixture |
| --- | --- | --- | --- | --- |
| fresh | `--dangerously-skip-permissions --session-id <uuid> --settings <tmp>/mate/.claude/settings.json --setting-sources project --strict-mcp-config --tools Bash,Read,Write,Edit,Glob,Grep,Skill --autocompact 300000 --model opus --effort medium --mcp-config <tmp>/mate/mcp-plugins.json` | MCP tools `["mcp__probe-inst__read_probe"]`, `mcp_servers [{"name":"probe-inst","source":"dynamic","status":"connected"}]` | `mcp__probe-inst__read_probe {"id":"abc123"}` → `probe:abc123:ok` | `server/discover, initialize, notifications/initialized, tools/list, tools/call` |
| resume | giống trên, `--resume <cùng uuid>` thay cho `--session-id` | như trên | `{"id":"resume456"}` → `probe:resume456:ok` | như trên |

Không server global nào lọt (init chỉ có `probe-inst`). Phán quyết: **tool
được gọi ở cả hai lượt dưới argv thật của Mate.**

### `cwd` của server stdio: Claude 2.1.287 bỏ qua

Đo tay trước khi viết `MCPConfig` (cùng env sạch như test, `claude -p "Reply ok."
--dangerously-skip-permissions --setting-sources project --strict-mcp-config
--tools Bash --mcp-config <file> --output-format json --verbose`, cwd của phiên
là `<tmp>/cwd`):

| Config | Kết quả |
| --- | --- |
| `{"command":"./mcpfixture","args":[],"cwd":"<tmp>/pkg"}` | `status: failed`, không tool MCP: lệnh tương đối không được tìm trong `cwd`. |
| `{"command":"<tmp>/pkg/mcpfixture","args":[],"cwd":"<tmp>/pkg"}` | `connected`, có `mcp__abs__read_probe`: khoá `cwd` lạ không làm hỏng config, chỉ bị bỏ. |
| `{"command":"/bin/sh","args":["-c","pwd -P > <tmp>/pwd.txt; exec <tmp>/pkg/mcpfixture"],"cwd":"<tmp>/pkg"}` | `connected`; `pwd.txt` = `<tmp>/cwd`, tức cwd của phiên, không phải `cwd` đã khai. |

Khớp với schema trong binary 2.1.287: stdio nhận `type?`, `command`, `args`,
`env`, `timeout`, `alwaysLoad`; transport spawn với `cwd` = project dir của
phiên. Vì vậy `MCPConfig` không ghi khoá `cwd`; server có `Cwd` được chạy qua
`/bin/sh -c 'cd "$1" && shift && exec "$@"' mate-mcp-cwd <cwd> <command...>`
(thư mục và lệnh là tham số vị trí, không nằm trong script). Lượt live ở trên
chứng minh cách này chạy (`./mcpfixture` tương đối lên được).

Phát hiện phụ: env của process `claude` tới được server stdio (fixture nhận
`MCP_FIXTURE_LOG` từ env của `claude`, không từ config).
