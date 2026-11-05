# Baseline audit trước pilot plugin — 2026-10-12

Đây là snapshot nhận diện working tree khi sửa plan sau review, không phải
commit/checkpoint đã landed và không phải live MCP evidence.

- HEAD: `87253691645a94674830e91f15ccc44fcc0f7a5c`.
- HEAD vẫn có compiled `internal/tool/catalog` với Fresh và Beads.
- Runtime diff đang chờ: editor command config, rồi removal Beads/registry và
  chuyển UI/query từ tool bindings sang command bindings.
- SHA-256 của `git diff --binary HEAD -- assets cmd internal`:
  `c095378687d1f988e011054e584f11ccfe68058289d119abab953c5d05eea9ad`.

Lệnh trên không chứa untracked files. Các runtime file mới thuộc thay đổi này:

| File | SHA-256 |
| --- | --- |
| `cmd/mate/console_commands.go` | `744a984daddf2985b0906485fdffdbecf81ea09c86855dde70f26a080d434e19` |
| `cmd/mate/console_commands_test.go` | `c13926756f38253e30aa18c484855defb1b5f43969652b145c561612edb01b8f` |
| `cmd/mate/external_tasks_test.go` | `e786dbf513285f44b8542aad1d376386db7c21f5be207d0ac1222d5a5bf018f5` |
| `internal/consolecmd/config.go` | `58ec9f235e607706c0ba7f2a74db9225644c5bc8f883dcbe768af8d943c06f17` |
| `internal/consolecmd/config_test.go` | `073459521e8b2842ba4bfe93f19ace2849f3ed5a42ca5bf6a16ef73fa136c8e3` |
| `internal/consolecmd/process.go` | `bccbd15b47ea825c5c781e47e079392bb780c160033afb48eecba4ef9405ed2c` |

Untracked `.claude/`, `videos/` và research không thuộc pilot không nằm trong
snapshot runtime này. Docs có diff/untracked riêng, gồm plan và tài liệu setup;
chúng không bị coi là đã commit chỉ vì plan nhắc đến.

Trong lần sửa plan chỉ đọc CLI help/version, không mở agent, login hay gọi API:

- Claude Code `2.1.287`: `--strict-mcp-config` chỉ dùng servers từ
  `--mcp-config`; `--tools` được mô tả là built-in tool set. Cần probe tool thực
  dưới tổ hợp flags trước khi kết luận ảnh hưởng tới MCP.
- codex-cli `0.162.1`: có dotted `-c` và `--strict-config`; chưa xác minh
  `mcp_servers.<instance>.url`, auth hoặc read tool trong launch Mate.

Trước implementation plugin cần chốt editor/removal qua các PR riêng và ghi
commit SHA mới làm baseline. Hash này giúp phát hiện runtime diff đã đổi; không
đảm bảo có thể phục hồi snapshot sau đó và không thay thế commit có thể checkout.
