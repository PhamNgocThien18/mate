# MCP plugin cho Codex và pi

Ngày: 2026-10-13. Trạng thái: triển khai trong draft PR; chờ Linear pilot.

## Tiến độ triển khai

- C1/C2: Codex 0.162.1 nhận một root-table override `mcp_servers={...}`
  thay vì dotted overrides. Parser giữ nguyên argv/cwd; live stdio, HTTP và
  mock OAuth gọi read tool trên fresh/resume cùng session. Không sửa config home.
- P1/P3: pi 1.0.4 đã có native MCP/OAuth và `registerMcpServer`; không cần
  MCP SDK bridge hay tải dependency khi launch. Load extension Mate bằng `-e`;
  extension gọi native `createMcpExtension` với loader chỉ workspace,
  bỏ qua global/project MCP giống Claude/Codex mà giữ native OAuth cache.
- P2: extension hooks và explicit skills đã triển khai. Live đo recall,
  sentinel echo và một prompt/answer receipt trên fresh/resume. pi chưa ghi
  session trống trước message đầu; stop lúc này restart fresh với thông báo,
  theo native lifecycle. Event compact
  mới có replay offline. Inbox, stow và compact live còn cần nghiệm thu.
- S1/S2: fixture stdio/HTTP/mock OAuth/tool error và wiring preflight/doctor/
  transcript đã triển khai. Ca unavailable/timeout/cancellation chưa có live proof.
- L1: **chưa chạy Linear thật trên Codex và pi**. Người dùng chọn draft PR
  chờ pilot; mock issuer không chứng minh tương thích Linear.
- R1: capability hỗ trợ adapter dựa trên fixture proof ở phiên bản đã đo;
  chưa coi đây là hoàn thành điều kiện phát hành Linear. `make check`, build
  và live checks được ghi riêng trong [evidence](../../evidence/codex-pi-mcp-2026-10-13.md).

Phần dưới giữ thiết kế/acceptance gốc để reviewer đối chiếu các gate còn thiếu.
Đợt này gom vào một draft PR theo yêu cầu người dùng; không cài binary hệ thống.

## 1. Kết quả cần đạt

Một workspace cài plugin HTTP hoặc stdio trong `.mate/plugins.yaml` có thể
khởi động Mate bằng Claude, Codex hoặc pi. Mate trên mỗi harness nhận cùng
instance, workflow skill và công cụ MCP; gọi read tool thành công sau start
và resume. Đổi plugin có hiệu lực ở lần start tiếp theo.

Lỗi đang gặp: bật `linear-work` rồi tạo Mate Codex bị từ chối trước launch.
Codex có cơ chế MCP nhưng adapter của Mate chưa truyền cấu hình và khai báo
`MCPConfig` unsupported. pi còn thiếu cả MCP adapter và `Hooks` cho vai Mate.
Đây là công việc triển khai còn thiếu của Mate.

Không coi việc bỏ refusal hoặc thấy server trong danh sách là hoàn thành.
Linear HTTP OAuth phải có bằng chứng read tool thành công trên cả hai harness.

## 2. Điểm xuất phát và bằng chứng

| Phần | Hiện trạng | Nơi đối chiếu |
| --- | --- | --- |
| Resolve plugin | Workspace instances được resolve theo project; chỉ Mate nhận plugin | `internal/spawn/plugins.go`, `internal/plugin` |
| Hợp đồng MCP | `MCPServer` gồm Name, Type, URL, Command, Cwd; `MCPConfigurer` render cấu hình ra bytes | `internal/harness/launch.go`, `contract.go` |
| Codex | Prepare và Build từ chối MCP; `MCPConfig` unsupported; Hooks đã verified | `internal/harness/codex/codex.go` |
| Codex parser | 0.162.1 nhận dotted overrides HTTP/stdio; chưa đo auth, tool call, resume | [audit](../evidence/codex-mcp-config-audit-2026-10-12.md) |
| pi | 0.99.1 chạy Crew; Hooks và MCPConfig unsupported; launch tắt auto extensions/skills | `internal/harness/pi/pi.go` |
| pi extension | `-e` vẫn nạp extension chỉ định khi có `--no-extensions`; có input, before_agent_start, agent_end | [contract](../../evidence/pi-contract-2026-10-01.md) |
| pi resume | session_start.reason là startup cả khi resume; không dùng reason để phân biệt | Cùng evidence pi |
| Claude | Chỉ stdio qua `claude -p` đã đo; HTTP OAuth và pane interactive chưa đo | [plugins](../../plugins.md), [Linear pilot](../../evidence/linear-pilot-2026-10-12.md) |

Khi triển khai phải ghi lại version thực tế; các version trên là mốc bằng chứng,
không phải giả định rằng mọi version mới giữ nguyên API.

## 3. Quyết định phạm vi

- Giữ config workspace và tên instance hiện có; không buộc cài lại Linear.
- Plugins vẫn chỉ đến Mate. Không tự cấp toàn bộ plugin workspace cho Crew.
- Không hot reload vào agent đang chạy, không tự restart agent khi sửa YAML.
- Core resolve dữ liệu chung; cú pháp Codex, pi extension và auth của từng
  harness nằm trong adapter tương ứng. Không thêm nhánh Linear vào core.
- Không ghi đè config/login/extensions của người dùng. Cấu hình launch và
  file sinh ra đặt trong state của Mate, có ownership và cleanup rõ ràng.
- HTTP dùng streamable HTTP; không quảng cáo hỗ trợ SSE nếu chưa triển khai.
- OAuth/token thuộc harness hoặc bridge riêng của pi; không xây shared OAuth
  daemon hay Plugin Host dùng chung trong đợt này.
- `doctor` vẫn offline. Nó báo compatibility/config, không gọi network hoặc
  chứng nhận rằng login/server/tool đang hoạt động.
- Giữ refusal cho transport/auth/version chưa hỗ trợ, nêu rõ instance và
  cách xử lý. Không silently bỏ server, tự chuyển harness hoặc tắt plugin.

## 4. Thiết kế Codex

### 4.1. Config và launch

Implement `MCPConfigurer` trong `internal/harness/codex`; dùng một renderer
chung cho capability và Build để tránh hai cách encode khác nhau.

HTTP: phát `-c mcp_servers.<instance>.url=<TOML string>`.
Stdio: phát command executable, args array và cwd theo schema Codex thực tế.
Đo parser của cwd trước khi chốt; nếu Codex không hỗ trợ cwd, dùng launcher
wrapper do adapter sinh, giữ nguyên argv và chạy ở package cwd. Không bỏ cwd
hoặc nối command qua shell string không escape.

Tên instance phải trở thành đúng một key TOML kể cả có dấu `-`/`.`;
quote key/value bằng encoder, không nội suy YAML thẳng vào argv. Kiểm tra
duplicate, transport mixing, thiếu command/url và config không hợp lệ trước
khi mở tab. Renderer phải có thứ tự ổn định.

Fresh và resume dùng cùng tập override; resume ID vẫn ở đúng vị trí hiện có.
Không sửa `CODEX_HOME/config.toml` của người dùng. Probe xác định config MCP
có sẵn trong Codex home merge với overrides như thế nào, và phát hiện collision
tên instance; không để plugin cùng tên bị thay thế âm thầm.

### 4.2. Auth và quan sát

Đo workflow `codex mcp login` với cùng name/url và Codex home mà pane dùng.
Xác minh token OAuth được phiên start/resume nhận khi config chỉ được truyền
qua launch overrides; không giả định login command nhìn thấy các override đó.
Ghi hướng dẫn login chính xác sau probe, gồm cách đưa config cho lệnh login.

Ưu tiên OAuth cho Linear, không yêu cầu người dùng ghi token vào YAML/argv.
Bearer env là phần bổ sung nếu cần: schema hiện chưa có field auth/env.
Chỉ thêm tham chiếu tên env sau khi xác định đường truyền env qua Herdr và
allowlist `LaunchSpec`; không mở allowlist toàn bộ env. Không persist giá trị
credential trong config sinh ra, log, snapshot hoặc evidence.

Ghi actual tool name/result vào fixture transcript; cập nhật classifier nếu
tên thực tế khác giả định `mcp__`. Lỗi auth/server cần thấy được ở pane và
diagnostics, không thành một tool call thành công giả.

## 5. Thiết kế pi

### 5.1. Chọn cơ chế MCP sau probe

Không giả định pi có native MCP config. Task đầu xác minh native support
hoặc extension bridge được duy trì trên version đang dùng. Ưu tiên cơ chế
có sẵn nếu đáp ứng HTTP, stdio, OAuth, resume và isolation.

Nếu cần bridge: một extension do Mate quản lý, load bằng đường dẫn tuyệt đối
`-e`, dùng MCP SDK để initialize, list tools và register tools trong pi.
Pin dependency/version và cung cấp artifact chạy được; không download/install
package mỗi lần mở Mate, không dựa vào package global chưa khai báo.
Chi tiết bundling và artifact location chốt bằng prototype trước task code.

Bridge phải giữ input schema, structured result, text và tool errors;
đặt tên tool ổn định có namespace instance, xử lý collision và tên không hợp
lệ; ghi mapping vào fixture transcript. Không hardcode danh sách tool Linear.
Đo cancellation, timeout và shutdown: đóng HTTP client, kết thúc child stdio,
không để process mồ côi sau stop/start lỗi. MCP stdout không được lẫn log bridge.

Nếu pi không có OAuth phù hợp, bridge quản lý auth trong phạm vi adapter pi:
workflow login rõ ràng, storage có quyền hạn phù hợp và tách theo server/account,
không lưu token vào plugin YAML hay transcript. Chốt đường dẫn storage sau
probe, không dùng chung token cache của Claude/Codex. Nếu cần lệnh login của
Mate, giữ nó ở lớp CLI chuyển tiếp sang adapter, không đưa OAuth vào core.

### 5.2. Hooks để pi làm Mate

Sinh extension hooks phiên bản cố định trong `internal/harness/pi`, khai báo
qua Prepare. Map events sang semantics `mate hook` hiện có: input giữ nguyên
sentinel và nội dung; recall chèn qua before_agent_start; turn end/shutdown
không tạo receipt hoặc log trùng. Chỉ map event đã đo có payload phù hợp.

Đọc hợp đồng `HookInstaller` và lifecycle start/stow/refresh trước khi sửa.
Mở rộng hợp đồng ở mức tối thiểu nếu hook extension không biểu diễn được bằng
lệnh shell; adapter giữ logic event, core không nhận tên event của pi.
Xác định fresh/resume từ launch intent/session ID, không dựa vào
session_start.reason. Live proof phải có memory recall sau restart, inbox
delivery và echo trong sent.log như các Mate hiện tại.

Giữ `--no-extensions` để không tự nạp extension của người dùng; chỉ load
extension Mate chỉ định, kiểm tra vẫn hoạt động cùng `--no-approve`.
Mate pi cần nhận built-in skills và plugin skills: khai báo `Info.SkillsDir`
và explicit skill loading bằng cơ chế pi đã đo, giữ Crew launch isolation.
Không chỉ ghi SKILL.md rồi cho rằng model đã nạp trong khi có `--no-skills`.

Chỉ cho pi xuất hiện trong picker Mate khi Hooks verified. MCPConfig và
Hooks là hai capability riêng; không dùng MCP thành công để bỏ gate Hooks.

## 6. Task và thứ tự thực hiện

Mỗi task là một PR độc lập review được, với evidence hoặc acceptance rõ ràng.

| ID | Việc | Phụ thuộc | Tiêu chí nghiệm thu |
| --- | --- | --- | --- |
| C1 | Codex probe start/resume/config merge/cwd/OAuth dưới launch flags thực tế | Không | Evidence version, argv redacted, login flow và read tool fresh/resume; xác định schema và config collision |
| P1 | pi probe native MCP/bridge, packaging, auth và skill loading | Không | Quyết định cơ chế kèm prototype gọi read tool HTTP/stdio, resume và dependency pin; ghi điều chưa đo |
| S1 | MCP fixtures dùng chung | Không | Stdio và HTTP trả marker xác định; có ca unavailable, tool error, auth-required, timeout; chạy không cần Linear credential |
| C2 | Codex renderer, Prepare/Build, auth wiring đã chọn | C1, S1 | Unit argv/escaping/cwd/fresh/resume; giữ gate production đến khi live proof đạt |
| P2 | pi hooks và skills cho vai Mate | P1 | Unit event mapping/ownership; live recall sau restart, sentinel echo, inbox và stow; bật Hooks chỉ sau proof |
| P3 | pi MCP config/bridge, auth và process lifecycle | P1, S1 | Unit config/names/results/errors; live HTTP/stdio/read/resume, cancellation và cleanup; gate MCP còn đóng nếu chưa đủ proof |
| S2 | Wiring spawn, preflight, doctor, picker và transcript | C2, P2, P3 | Start nhận cùng instances/skills, disabled không truyền, Crew không nhận; refresh từ chối trước stop; transcript ghi đúng MCP |
| L1 | Linear pilot trên Codex và pi trong Herdr | S2 | Mỗi harness login OAuth, read thật, stop/resume cùng session và read lại; evidence không lộ credential |
| R1 | Bật capability, tài liệu và build kiểm tra | L1 | Contract suite + make check + live gates đạt; docs compatibility đúng; build/version được kiểm và smoke trên binary mới |

Codex có thể hoàn thành và phát hành trước pi sau khi đạt gates riêng;
không trì hoãn fix Codex để chờ hooks pi. Không gọi hỗ trợ pi hoàn thành
cho đến khi cả Hooks, MCP và Linear OAuth đều đạt. Chỉ chạy lịch tuần tự
hoặc tách nhánh theo dependencies; bảng không yêu cầu delegation.

## 7. Kiểm tra bắt buộc

Offline regression đi qua real spawn seam, không chỉ renderer:

- Cùng workspace plugin start Claude/Codex/pi truyền đúng server, cwd và
  skill; chỉ các adapter đủ capability được launch.
- Nhiều instance cùng package, tên có dấu chấm/gạch ngang, path có khoảng
  trắng, argv có quote/newline và config thiếu/không hợp lệ.
- Start fresh/resume, plugin đổi giữa hai lần start, disabled/removed
  không còn tool và skill cũ; file/skill người dùng không bị ghi đè.
- Preflight refresh thất bại không stop Mate đang chạy; doctor không gọi
  network và nói rõ offline compatibility khác live connection.
- Pi Crew giữ flags, session và behavior hiện có; plugin không tự lọt vào Crew.
- MCP server failure không làm giả sent.log receipt, completion hoặc usage.

Live suites `TestLive*`, opt-in `MATE_LIVE=1`, dùng isolated homes/state và
provider credentials theo quy tắc live harness hiện có. Fixtures phải chạy
trong pane Herdr dưới flags production, không chỉ `exec`/print mode.
Mỗi harness: stdio read, HTTP read, tool error, unavailable server,
fresh/resume; pi thêm hooks/skills/shutdown. OAuth Linear có pilot riêng nếu
không thể tự động login; thiếu account/login thì ghi chưa đo, không đánh verified.

`make check` xanh có live skip chỉ chứng minh offline. Khai báo expected skips
theo policy repo; báo riêng số live test đã chạy. Evidence lưu dưới
`docs/evidence/`, gồm command, version, redacted config/argv, tool name,
expected/actual result, session ID trước/sau, cleanup và các giới hạn.

## 8. Điều kiện phát hành và xử lý thất bại

Trước bật capability: implementation có Impl đúng contract, fixture suite
đạt, pane production gọi tool thành công, auth và resume đã đo cho transport
được công bố. Nếu chỉ stdio đạt, công bố và validate stdio riêng; capability
verified không được khiến preflight chấp nhận HTTP chưa hỗ trợ. Nếu hợp đồng
bool hiện tại thiếu mức transport, bổ sung validation chung dùng bởi doctor,
preflight và Build thay vì để ba nơi suy đoán khác nhau.

Giữ các ca refusal cho transport sai; thay tests từ chối Codex mọi MCP bằng
positive launch tests và negative config tests. Cập nhật `docs/plugins.md`,
`docs/mvp.md`, adapter notes và plan pilot cũ để dẫn tới evidence mới; không
sửa lịch sử dated evidence thành khẳng định đã đo từ trước.

Cuối cùng build binary mới, đối chiếu `mate --version` và binary trong PATH.
Smoke workspace fixture có plugin qua start/resume trên bản build đó. Cài
binary vào vị trí hệ thống theo quyền ghi của môi trường; không kết luận
người dùng đã chạy bản mới chỉ từ việc `make build` thành công.

Nếu gate không đạt, ghi chính xác task còn thiếu và giữ thông báo capability
actionable. Trước đợt phát hành đầy đủ, người dùng có thể dùng Claude hoặc
tắt plugin để chạy Codex; đây là workaround, không phải acceptance của plan.
