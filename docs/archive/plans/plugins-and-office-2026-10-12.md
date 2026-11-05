# Plugin ngoài: lát mỏng cho pilot Linear

> Workspace scope correction (2026-10-12): new installations put `config` and
> `key` directly under the instance and apply to every current/future Mate
> project. The project bindings below describe the original pilot design;
> existing files remain readable for compatibility. See [current setup](../../plugins.md).


Ngày: 2026-10-12. Trạng thái: **đề xuất đã thu hẹp sau review**, chưa triển khai.
Thay thế draft cùng ngày về Plugin Host/shared OAuth. Mục tiêu trước mắt là
pilot Linear hai tuần trên **Mate Claude**, rồi dùng evidence để quyết bước sau.

## 1. Baseline và lịch sử quyết định

Baseline committed là `87253691645a94674830e91f15ccc44fcc0f7a5c`.
Commit này **vẫn chứa** `internal/tool` và catalog Fresh/Beads. Working tree đang
có hai thay đổi chưa commit: editor thành console command config và bỏ tích hợp
Beads/registry. Plan này cũng chưa commit. Câu “registry đã bỏ” ở draft trước
chỉ đúng với working tree, không đúng với HEAD; không dùng nó như baseline đã
landed. [Snapshot runtime và danh sách file](../evidence/plugin-pilot-baseline-2026-10-12.md).

Trước implementation cần một checkpoint review riêng cho hai thay đổi đang
chờ, với commit SHA và checks. Các PR plugin base trên checkpoint đó, không
chồng implementation lên diff removal chưa chốt. Snapshot hash giúp nhận diện
bản đang review, không thay thế commit có thể checkout.

### Vì sao đổi từ registry 2026-10-08?

[Registry cũ](../../plans/workspace-layout-and-tools-2026-10-08.md) giải đúng yêu cầu lúc đó:
đưa Fresh/Beads vào profile, thêm công cụ bằng package trong repo + một dòng
catalog, giữ data ngoài Mate và bỏ task schema trong core. Nó gom coupling
rải rác và có contract tests; không có evidence rằng abstraction đó bị lỗi.

Yêu cầu sau đó đã đổi: user tự chọn app bằng config/prompt, package **ngoài cây
source**, không rebuild binary và không bắt Mate tự quản lý tracker. Compiled
catalog không đáp ứng yêu cầu mới. Các call site vẫn có quyền tự init data,
đọc tracker trong recall, cài workflow skill và cấp phím/view mặc định. Những
hành vi ấy không còn phù hợp sau quyết định bỏ Beads và cho user sở hữu workflow.
Đây là lý do thay thiết kế, không phải vì đổi tên registry thành “Plugin Host”.

Draft Host lại lặp nhiều capability tương tự Skill/Command/Viewer/Recall cũ và
thêm OAuth, gateway, daemon, lease, view schema trước khi có pilot. Đổi cách
đóng gói không tự loại coupling. Bản thu hẹp chỉ có **config local → harness
MCP config + skill + URL mở app**. Không có Data/Recall lifecycle, task methods,
compiled catalog hoặc plugin code được gọi bởi core để chọn/cập nhật việc.

## 2. Phạm vi v1

- Một local package với manifest nhỏ và skill tuỳ chọn. Một config instance /
  project dùng chung để dựng launch, Console link và Office link.
- Harness trực tiếp kết nối MCP và sở hữu OAuth/token/lifecycle. Pilot Claude
  login trong `/mcp` ngay tại pane Mate, với config dir và server name thực tế.
- Console có shortcut mở URL; Office chỉ đọc cấu hình/trạng thái cấu hình và
  có nút Open. Quản lý bằng CLI hoặc edit file, không màn hình Install/Connect.
- Không daemon, socket/lease, shared token store, MCP client/proxy trong Mate,
  plugin receipts, package cache/digest/lock/update hay view renderer v1.
- Không hứa login dùng chung Claude/Codex/Office. Office không gọi MCP; Codex
  cần auth riêng nếu sau này được hỗ trợ. Đây là tradeoff chấp nhận cho pilot.
- `.mate/console.yaml` tiếp tục chạy editor như hiện tại. V1 không chuyển mọi
  editor command thành plugin hay dựng action framework để chạy mọi loại tool.

Core cần sửa một lần cho config/launcher/link interface. Sau đó thêm package
cùng interface không đổi source/build Mate. Plugin tác giả cung cấp workflow;
core không biết Linear team, issue status hoặc claim policy.

## 3. Gate đầu tiên: chứng minh launcher, trước resolver

Hai điểm này nằm trong **bước 1**, không để đến một phase gateway:

### Claude

Launcher hiện có `--setting-sources project --strict-mcp-config`, không có
`--mcp-config`, và có `--tools Bash,Read,Write,Edit,Glob,Grep,Skill`. Phải kiểm
tra toàn bộ tổ hợp: config được load, MCP tool discover được, model gọi được
read tool, global servers không lọt vào, hooks/skills và resume vẫn hoạt động.

Review nêu `--tools` chặn MCP. Help local **Claude Code 2.1.287** ngày 2026-10-12
mô tả flag này là “available tools from the built-in set”; help chưa chứng minh
MCP bị chặn hoặc được cho phép. Không khẳng định đã giải chỉ bằng thêm
`--mcp-config`, cũng không tự bỏ allowlist. Probe bằng một MCP fixture stdio có
read tool duy nhất dưới đúng cờ launch Mate, ghi tool call/result làm evidence;
sau đó kiểm HTTP OAuth với Linear tại pane thật. Giữ policy built-in hiện có;
chỉ chỉnh đường cho MCP nếu probe chứng minh cần, bằng cơ chế harness hỗ trợ.

Tiếp nối 2026-10-13: adapter Codex và pi được triển khai theo
[plan MCP](codex-pi-mcp-2026-10-13.md); kết quả đo mới ở
[evidence](../../evidence/codex-pi-mcp-2026-10-13.md). Phần dưới ghi baseline pilot.

### Codex

Launcher chưa nhận `mcp_servers`. Help local **codex-cli 0.162.1** hỗ trợ dotted
`-c` và `--strict-config`, nhưng chưa xác minh kết hợp
`-c mcp_servers.<instance>.url=...`, auth, strict-config và resume trong Mate.
Ghi ngay trong gate đầu là **unverified/unsupported cho config plugin**; không
quảng cáo Codex parity. Claude pilot không chờ Codex. Nếu muốn hỗ trợ Codex,
probe parser + read tool + auth riêng dưới đúng launch flags trước khi bật.
Parser dotted `mcp_servers` dưới `--strict-config` đã đo OK, phần còn lại chưa:
[evidence](../evidence/codex-mcp-config-audit-2026-10-12.md).

Version/help chỉ là evidence cú pháp; không có live MCP proof trong lần sửa
plan này. Gate phải có recording/version/argv redacted và tool result, không
chỉ unit test argv hoặc output `/mcp` cho biết server tồn tại.

## 4. Manifest local và project config nhỏ

Ví dụ **schema đề xuất, CLI chưa tồn tại**:

```text
/path/to/linear-plugin/
├── mate-plugin.yaml
└── skills/workflow/SKILL.md

<workspace>/.mate/plugins.yaml
```

```yaml
# /path/to/linear-plugin/mate-plugin.yaml
apiVersion: mate.plugin/v1
name: Linear
mcp:
  type: http
  url: https://mcp.linear.app/mcp
skills:
  - skills/workflow/SKILL.md
open:
  title: Open Linear
  url: "{config.project_url}"
```

```yaml
# <workspace>/.mate/plugins.yaml
apiVersion: mate.plugins/v1
instances:
  linear-work:
    path: /path/to/linear-plugin
    projects:
      shop:
        config:
          project_url: https://linear.app/example/project/shop/overview
        key: t
```

Chỉ bind vai Mate; không inject vào Crew. Project không có binding thì không
nhận server/skill/link. Instance id dùng identifier ổn định và unique; package
path relative được resolve từ workspace root, skill path từ package root và
không vượt ra ngoài package. Unknown fields, duplicate ids/keys, missing paths
và URL không hợp lệ là lỗi cấu hình có vị trí rõ. URL v1 chỉ HTTP(S).

Config của app là opaque values. Team/project identifiers và workflow nằm
trong skill/config user, không có schema Task hay auto-select project đầu tiên.
Chưa xây JSON Schema form generator hoặc picker gọi tool trong Mate.

### Tool name và skill phải cùng một quy tắc

Tên MCP server trong generated config là **instance id**, ví dụ `linear-work`.
Claude dùng tên `mcp__linear-work__<tool-name-published-by-server>`. Không có
namespace/alias riêng từ Mate hoặc gateway. Skill tĩnh phải dùng tên tool thật
đã verify ở pilot; prefix instance được thay bằng biến `{instance_id}` khi
materialize skill. V1 chỉ literal substitution cho biến đã khai báo, không
expression/eval. Không đoán tên tool hoặc biến chưa biết rồi coi setup thành công.

Plugin author ghi version/ngày tool names được verify. Provider đổi tên làm
skill lệch thì sửa package bên ngoài; không thêm mapping Linear trong core.
`doctor` chỉ kiểm config/dependency/harness support, không tự discovery/login;
`/mcp` và live read là evidence tool availability. Codex naming có thể khác,
phải verify và dùng skill tương ứng trước khi declare support.

## 5. Chuẩn bị launch và login do harness sở hữu

Resolver là một pure module: đọc package + binding của project, trả MCP server
config, skill assets và URL link. Không interface Snapshot/Invoke/Watch.
Launcher adapter chuyển output sang format/flags của harness. Chuẩn bị file
qua luồng Prepare/start hiện có; không thêm background writer vào state Mate.

Generated config/skills ở vùng riêng của launch, có ownership rõ, namespace
skill theo instance. Không ghi đè user skills/global settings hoặc token. Nếu
file destination có nội dung user không thuộc generator thì báo conflict.
Claude nhận explicit `--mcp-config` và giữ `--strict-mcp-config`; skill/manual
load theo cơ chế harness hiện có. Auth là của Claude, không của resolver.

Đổi config cần restart agent để nhận tool/skill mới; không auto-interrupt.
Doctor không gọi write tool để check. Disable binding loại nó khỏi launch kế
và links; không revoke account hay xoá tracker data. Chưa xây hot-reload, status
store hay management API. Mở lại Console/Office để đọc config mới.

Flow mục tiêu: `mate plugin add <local-path> --as linear-work --project shop
--url <project-url>` ghi config tường minh, giữ các instance khác; start/restart
Mate rồi vào `/mcp` login và chạy read mẫu. Lệnh add là tiện ích nhỏ sau resolver,
không package manager. User vẫn có thể edit YAML trực tiếp.

## 6. Console và Office trong spec hiện hành

Console ghép shortcut URL của project vào config commands hiện có. Collision
với console.yaml hoặc reserved key thì báo lỗi, không ghi đè/suy ưu tiên ngầm.
Bấm shortcut dùng opener của máy Console; không tạo terminal tab chỉ để mở URL.

Office thêm một mục nhỏ trong project drawer: tên app, trạng thái config và
anchor Open (`target=_blank`, `rel=noopener`). Không thêm màn quản lý vào
`office.js`. Browser của captain mở URL, không phải server chạy `open`.

Có thể thêm `GET /api/plugins?project=<p>` trả metadata local đã validate.
Route đọc file config, không ghi database/state, không gọi Linear/MCP, không
exec. Không thêm POST, control token, form mutation hay cross-origin request
trong Office. [Dashboard §10–11](../../dashboard.md#10-the-ui) giữ GET-only,
read-only, same-origin. Spec chỉ bổ sung endpoint metadata và link trong PR nhỏ
cùng implementation; việc cho Office ghi sau này cần spec change riêng trước.

Status v1 là `configured`, `configuration error` hoặc `harness unsupported`.
Auth/tool availability là **unknown to Office**, kèm hướng dẫn kiểm `/mcp` trong
Claude. Không ghi Ready/Connected chỉ vì YAML hợp lệ. Không infer trạng thái
kết nối từ timeline event, không làm request cache phụ thuộc duy nhất vào
`last_event_id`: metadata route đọc config khi request, không timeline cache.
Lỗi một package/config hiện ở mục app đó, không làm scene/timeline Office mất
dữ liệu. Office không ghi kết quả auth hoặc status mới vào state Mate.

## 7. Các PR nhỏ và gate

Mỗi hàng là một PR review trong một buổi. Nếu vượt giới hạn thì tách tiếp;
không ghép cả host + OAuth + renderer vào một hàng.

| PR | Scope | Evidence / gate |
| --- | --- | --- |
| 0a | Chốt editor command diff đang chờ | Config/argv/GUI launcher checks và console help; SHA riêng |
| 0b | Chốt removal Beads/registry đang chờ | Không gọi bd/bv, không mất data/skill user; make check/build; SHA baseline mới |
| 1a | Characterize Claude MCP dưới launch flags thật | Fixture read tool + tool result; xác định tác động --tools, strict config, hooks/resume; evidence ghi trong repo |
| 1b | Audit Codex config injection | Parser/strict-config probe; ghi support chưa verified, không thêm parity vào pilot; nếu thử tool thì gate riêng trước enable |
| 2 | Resolver local + schema nhỏ | Invalid config, project isolation, stable server name/path; không app literal trong core |
| 3 | Materialize MCP config và skills | Ownership/conflict/user preservation; no token; disabled instance không inject |
| 4 | Claude launch wiring | Explicit MCP config, policy tools đã verify; unit/golden + read fixture; không global inheritance |
| 5 | Console URL shortcut | Resolve đúng project/key; collision; legacy editor behavior không đổi |
| 6 | Office GET metadata + Open | Cập nhật dashboard docs; GET-only/read-only; unknown auth; anchor đúng URL; không server exec |
| 7 | CLI local add/doctor | Config write rõ, preserve binding; doctor không gọi provider; help/setup instructions |
| 8 | Linear package pilot + evidence | External manifest/skill tool names đã verify; read và một write task do captain chọn; package không compile vào Mate |

PR 1a/1b là bước khả thi đầu tiên trước PR 2. Fail gate thì thu hẹp/đổi launcher
trước, không tiếp tục xây platform để che điểm chưa biết. Gate Claude đạt là đủ
để mở pilot; Codex unsupported phải hiện rõ. Các PR checks cần thiết chạy
`make check`; skipped live tests không chứng minh launch/tool đã hoạt động.

## 8. Pilot hai tuần và quyết định sau đó

Một project, 10–20 issue thật. Captain chọn/ưu tiên; Mate là dispatcher duy nhất,
Crew báo kết quả. ID issue có trong brief/PR; task external không đồng nhất với
Crew state. Done theo acceptance/merge policy. Không native webhook dispatch,
Office tool calls hoặc automatic tracker recall trong pilot.

Đo setup time, các lần auth/connection hỏng, task stale/trùng, request → merge
và việc captain có theo dõi backlog dễ hơn không. Ghi cụ thể lần nào phải mở
Linear vì Office link chưa đủ; không dùng “native hơn” làm lý do xây Host.

Chỉ cân nhắc bước sau nếu pilot có evidence:

- Shared login: chỉ khi nhiều harness/UI thật cần nó và chi phí login riêng là
  vấn đề đo được. Trước daemon, thử stdio gateway do harness spawn, ví dụ
  `mate plugin gateway --project shop`; mỗi agent một process, harness sở hữu
  vòng đời. Đây là phương án tương lai, không CLI v1 đã tồn tại.
- Native views (cards/table/detail/form/board): chỉ khi **hai app thật** cần và
  chấp nhận cung cấp adapter. MCP chính thức không trả `mate.view/v1`; không
  tự có panel sau login. Schema/renderer/control route là design riêng lúc đó.
- Office mutation/Install/Connect: spec dashboard phải đổi trước, kèm model
  authentication/authorization và kế hoạch giữ UI nhỏ.
- Package cache/lock/digest/upgrade: chỉ khi dùng distributed packages, không
  cho local-path pilot. Webhook/Linear Agent API là integration ngoài riêng.

Giữ hướng dài hạn package ngoài binary + một project config. V1 chứng minh
seam nhỏ bằng Linear; không coi việc cài một app là bằng chứng đã cần Host.

Nguồn nền: [task management](../../task-management.md),
[console commands](../../console-commands.md),
[Linear research](../research/linear-for-mate-2026-10-12.md).
