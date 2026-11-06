# Tách `store.Workspace` theo trách nhiệm

- Ngày: 2026-11-06; sửa theo review độc lập cùng ngày.
- Trạng thái: kế hoạch của CON-173, chờ captain duyệt trong PR. Issue code ở mục 6 chỉ tạo sau khi duyệt, gắn vào CON-181.
- Baseline đã đọc: `origin/main` 4d88f09 (sau đợt 8b của CON-203).
- Liên quan: CON-181 (tách component), CON-164 (logic ra khỏi `cmd/mate`), CON-172 (test `cmd/mate` chạy song song), CON-213, CON-214, CON-232 (tách core), CON-190 (`internal/runtime/herdr`), CON-178 (comment ADR), CON-226 (core giám sát host).
- Tài liệu cần sửa khi làm: `docs/architecture.md` mục 1 (đoạn các package trộn tầng, nếu có) và mục 3 (câu "Path helpers live in `internal/store/layout.go`"); `GLOSSARY.md` dòng _Trong code_ của **Wake** nếu bước S8 bỏ export `WakeCursorFile`.

## 1. Hiện trạng

### Cách đo

Số chỗ gọi đếm bằng `go/types` (`golang.org/x/tools/go/packages`, đọc cả file test): mỗi identifier mà `types.Info.Uses` trỏ tới một method có receiver `store.Workspace`. Số method đếm được bằng `grep -c '^func (\w\+ \*\?Workspace)' internal/store/*.go` (bỏ file test). "Closure" của một hàm là mọi method nó dùng, kể cả qua các hàm nó gọi trong module. Con số này hơi rộng hơn thực tế, vì hàm được gọi có thể dùng workspace của chính nó. Công cụ đo không nằm trong repo. Bước nào làm thì đo lại trên `main` lúc đó.

### Số đo

- `store.Workspace` có **157 method** (140 exported, 17 unexported) trong 29 file không phải test, tổng 4.601 dòng. Ngày 2026-11-04 (872657d, lúc viết CON-173) là 142. Trong hai ngày, 6 issue thêm 15 method: CON-162 (3, `console_lock.go`), CON-211 (7, `core_lock.go`), CON-204 (`snapshot`, `updateConfig`), CON-165 (`CrewIsScout`), CON-228 (`RequireProject`), CON-229 (`EnsureProjectDoc`).
- Số method theo file: `layout.go` 42, `workspace.go` 20, `project.go` 14, `migrate.go` 8, `log.go` 8, `core_lock.go` 7, `repo.go` 6, `outbox.go` 6, `auto.go` 6, `meta.go` 5, `harness_profile.go` 5, `crewdoc.go` 4, mỗi file còn lại 1–3.
- Ngoài `store`, 20 package (cộng `scripts/m7measure`) có nhắc tên kiểu `*store.Workspace`, và 120 file không phải test import `store` (48 file trong `cmd/mate`, 21 file trong `internal/spawn`). Có khoảng **710 chỗ gọi** method trong khoảng 117 file không phải test (707 nếu không tính 3 chỗ trong `scripts/m7measure`), và khoảng **1.700 chỗ** trong gần 200 file test. Review độc lập đếm ra 1.713–1.719, tuỳ cách gộp file test.
- Số method khác nhau mà mỗi package dùng (file không phải test): `cmd/mate` 58, `spawn` 46, `timeline` 26, `migrate` 23, `dashboard` 17, `query` 16, `core` 15, `outbox` 15, `recall` 15, `recovery` 14, `prwatch` 14, `wake` 12, `autopilot` 12, `refresh` 11, `box` 9, `watch` 8, `usage` 6, `hook` 3, `learnings` 2, `db` 2.
- Không package nào ngoài `store` khai báo interface thay cho `*store.Workspace`. Mọi bên dùng đều nhận đủ 140 method exported.
- 15 method exported không được package nào khác gọi, kể cả trong test của package khác. 9 trong số đó chỉ có test ngoài (`package store_test`) của chính `internal/store` gọi: `AutoCursorFile`, `Config`, `CoreHolder`, `CrewStopLock`, `JevLog`, `MigrateLockFile`, `OutboxFile`, `RelRepo`, `SaveConfig`. Danh sách đủ 15: `AutoCursorFile`, `Config`, `CoreHolder`, `CoreLogFile`, `CrewHarnessProfile`, `CrewStopLock`, `JevLog`, `MigrateLockFile`, `OutboxFile`, `ProjectsDir`, `RecoverLockFile`, `RelRepo`, `SaveConfig`, `SetAuto`, `WakeCursorFile`.

### Chỗ hay xung đột

Số commit không phải merge từ 2026-10-07 tới 4d88f09: `project.go` 22, `workspace.go` 15, `layout.go` 14, `repo.go` 9. Đọc hunk của từng commit thì thấy:

- `project.go`: 15 trong 22 commit sửa phần schema (`ProjectConfig`, `MateConfig`, `BudgetConfig`, `WorkflowStep` và các hằng `Review*`, `NextWork*`, `MetaBudget*`). Chỉ 7 commit sửa method (`AddProject` 3 lần, `Held` 2, `MateHeadless`, `RelRepo`/`removeFlag`). Schema và method nằm chung file, nên hai việc không liên quan vẫn đụng nhau.
- `layout.go`: gần như mọi commit chỉ thêm một hàm đường dẫn, và nhiều commit thêm vào cùng một chỗ, quanh `CrewHandback` và `CrewLearning` (CON-73, CON-112, CON-129, CON-130, CON-131). Các dòng thêm vào nằm sát nhau nên git không tự gộp được.
- `workspace.go`: vừa có schema (`WorkspaceConfig`, `CrewLaunch`), vừa có phần đọc lại cấu hình (CON-204, CON-218), vừa có `Open`/`Init`.

Thêm field vào cùng một struct (`ProjectConfig`) vẫn đụng nhau dù tách file thế nào. Git chỉ gộp được khi các dòng thêm không nằm sát nhau.

## 2. Nhóm method và đích

Nhóm là lý do một method phải đổi. Tổng 54 + 18 + 16 + 5 + 14 + 9 + 8 + 9 + 12 + 6 + 6 = 157. Cột "Chỗ gọi" là số chỗ gọi trong file không phải test ngoài `store` (trong ngoặc là số chỗ trong file test).

| Nhóm | Method | Chỗ gọi | Gồm | Đích |
| -- | -- | -- | -- | -- |
| G1 Đường dẫn | 54 (53 exported) | 296 (883) | 42 hàm của `layout.go`, cộng 12 hàm chỉ ghép đường dẫn nằm ở file khác: `ConsoleLockFile`, `CoreLockFile`, `CoreSocket`, `CoreLogFile`, `CrewStopLock`, `RecoverLockFile`, `MigrateLockFile`, `MigrateLog`, `OutboxFile`, `outboxLockFile`, `CrewHarnessProfile`, `CrewReviewMeta` | Kiểu giá trị `store.Paths` nhúng trong `Workspace` (S1). File `layout.go` (cấp workspace), `layout_project.go`, `layout_mate.go`, `layout_crew.go` (S2) |
| G2 Cấu hình workspace | 18 | 41 (48) | `workspace.yaml`: `LoadConfig`, `SaveConfig`, `updateConfig`, `snapshot`, `Config`, `Session`, `RecordedRoot`, `SetRoot`, `SetSession`, `Layout`, `LayoutOld`, `Defaults`, `CrewAutocompact`, `CrewContextHigh`, `LaneCap`; `.env` (`LoadEnv`); `pricing.yaml` (`LoadPricing`, `SavePricing`) | Method của `*Workspace`, file `workspace_config.go` (tách khỏi `workspace.go`), `env.go`, `pricing.go` |
| G3 Danh sách project và repo | 16 | 119 (160) | `Projects`, `Project`, `RequireProject` (hôm nay ở `workspace.go`), `AddProject`, `EnsureProjectDoc`, `RemoveProject`, `LoadProject`, `SaveProject`, `normaliseProject`, `RelRepo`; 6 hàm của `repo.go` | Method của `*Workspace`, file `project.go` (chỉ method) và `repo.go`. Schema (`ProjectConfig`, `MateConfig`, `BudgetConfig`, `WorkflowStep`, các hằng) sang `project_config.go` |
| G4 Chế độ Auto/Manual | 5 | 16 (53) | `Auto`, `Held`, `SetMode`, `SetAuto`, `removeFlag` (cờ `.manual`, `.auto`) | Method của `*Workspace`, file `mode.go` mới |
| G5 Hồ sơ Crew | 14 | 84 (229) | `.meta` (`ReadCrewMeta`, `WriteCrewMeta`, `UpdateCrewMeta`, `CrewIDs`), harness profile (4), `CrewRules`, `ReplaceCrewBrief`, `WriteCrewSessionInput`, review (`AddCrewReview`, `ReviewLinks`), `CrewIsScout` | Method của `*Workspace`; file giữ nguyên (`meta.go`, `harness_profile.go`, `crewdoc.go`, `review.go`, `scout.go`); `CrewIDs` sang `meta.go` |
| G6 Hồ sơ và sổ của Mate | 9 | 52 (114) | `ReadMateMeta`, `WriteMateMeta`, `MateHeadless`, 3 hàm của `mate_sessions.go`, `EditBacklog`, `EditBacklogEntry`, `ReplaceMemoryFile` | Method của `*Workspace`; `ReadMateMeta`, `WriteMateMeta` (từ `meta.go`) và `MateHeadless` (từ `project.go`) sang `mate_meta.go` mới; `mate_sessions.go`, `backlog.go`, `memory.go` giữ nguyên |
| G7 Log chỉ ghi nối | 8 | 45 (175) | `.status`, `sent.log`, `incidents.log`, `jev.log`: `Append*`, `Read*`, `SentLogSize` | Method của `*Workspace`, file `log.go`; `SentLogSize` dời từ `outbox.go` sang |
| G8 Outbox và con trỏ | 9 | 16 (21) | `ReadOutbox`, `UpdateOutbox`, `writeOutbox`; con trỏ của auto và wake (6 hàm của `auto.go`) | Method của `*Workspace`, file `outbox.go`, `auto.go` |
| G9 Khoá giữa các process | 12 | 18 (27) | `LockConsole`, `ConsoleHolder`, `ConsoleRunning`, `LockCore`, `CoreHolder`, `CoreRunning`, `LockCrewStop`, `CrewStopping`, `LockRecover`, `TryMateMaintenance`, `TryMateLock`, `WithCrewSend` | Method của `*Workspace`; file giữ nguyên (`console_lock.go`, `core_lock.go`, `crew_stop_lock.go`, `recover_lock.go`, `maintenance.go`, `crew_send.go`, hàm chung trong `lock.go`) |
| G10 Migrate layout 1 → 2 | 6 | 6 (3) | `LockMigrate`, `MigrateLocked`, `AppendMigrateLog`, `ReadMigrateLog`, `SetLayoutProjectDirs`, `WriteCrewBrief` | Kiểu view `store.Migration`, lấy bằng `ws.Migration()` (S3); file `migrate.go` |
| G11 Nền: ranh giới và ghi file | 6 (1 exported) | 14 (0) | `resolve`, `Resolve`, `mkdirAll`, `writeFile`, `appendLine`, `seedOnce` | Method của `*Workspace`; `resolve`, `Resolve` ở `boundary.go`; 4 hàm còn lại sang `fsio.go` mới |

### Vì sao không tách thành package con hay kiểu riêng cho mỗi nhóm

- Các nhóm G2..G9 dùng chung trạng thái của handle: gốc workspace đã resolve, phép kiểm ranh giới (`resolve`), ghi nguyên tử (`writeFile`) và khoá cấu hình (`cfgMu`, CON-204). Nếu tách ra package con, `store` phải export `resolve` và `writeFile`. Khi đó package nào cũng ghi được vào `.mate/` mà không qua `store`, trái với luật trong `internal/store/doc.go`.
- Một kiểu riêng cho mỗi nhóm, lấy qua hàm truy cập (`ws.Logs().AppendSent(…)`), buộc phải sửa khoảng 710 chỗ gọi trong code thường và khoảng 1.700 chỗ trong test, đúng những file mà các Crew đang sửa song song.
- Hai vấn đề của CON-173 có cách giải rẻ hơn. Bên dùng nhận quá nhiều method: mỗi bên khai báo interface hẹp của mình (mục 3). Hay xung đột merge: chia file theo nhóm (S2). Không cách nào trong hai cách này phải sửa chỗ gọi.
- Ngoại lệ là hai nhóm có lý do riêng:
  - G1 thành kiểu `Paths`, vì đây là nhóm lớn nhất (54 method, 34%) và chỉ ghép đường dẫn. `Paths` chỉ giữ gốc workspace, nên trình biên dịch bảo đảm các hàm đó không gọi được `resolve`, `writeFile` hay cấu hình. Nhúng vào `Workspace` thì method được promote, và `w.CrewMeta(p, c)` vẫn biên dịch ở mọi chỗ.
  - G10 thành view `Migration`, vì chỉ `internal/migrate` dùng 6 method này. Chúng chỉ tồn tại để chuyển layout 1 sang 2. Gom lại thì sau này bỏ layout 1 chỉ cần xoá một file, và `Workspace` mất hai hàm ghi nguy hiểm: `SetLayoutProjectDirs` và `WriteCrewBrief`. `WriteCrewBrief` ghi đè brief mà không kiểm file đã có chưa, khác `ReplaceCrewBrief`.

### Trường hợp đặc biệt: các khoá và migrate

- `crew_stop_lock.go`, `recover_lock.go`, `maintenance.go`, `crew_send.go` thuộc **G9**, cùng `console_lock.go` và `core_lock.go`. Lý do: các hàm này cùng đổi khi cách khoá đổi. Chúng cùng dùng `platform.Lock`/`TryLock` qua `lock.go`, cùng luật "không có flock thì báo lỗi" (CON-177), cùng luật "không bao giờ xoá file khoá" và "khoá tự nhả khi process chết". Bên gọi mỗi khoá chỉ có 1–3 chỗ. `WithCrewSend` cũng thuộc G9: hợp đồng của nó là xếp hàng các lần gửi giữa các process, còn file `send.json` là bản ghi của khoá, giống JSON của holder trong `console.lock`. Đường dẫn của các khoá (`CrewStopLock`, `RecoverLockFile`, `ConsoleLockFile`, `CoreLockFile`) sang G1, vì chúng chỉ ghép chuỗi.
- `migrate.go` có 8 method thuộc ba trách nhiệm. `MigrateLockFile` và `MigrateLog` sang **G1** vì chỉ là đường dẫn. 6 method còn lại thuộc **G10**, kể cả `LockMigrate` và `MigrateLocked`: khoá này chỉ có để `mate migrate` không chạy hai lần cùng lúc, và sẽ mất cùng migrate. Đây là chỗ duy nhất kế hoạch xếp một khoá ngoài G9. `LockRecover` thì ở lại G9, vì `recovery` cũng dùng nó.

### Chữ ký trước và sau, theo nhóm

Với các nhóm ở lại `*Workspace`, chữ ký trong `store` giữ nguyên, chỉ đổi file. Chỗ đổi nằm ở bên dùng: tham số `*store.Workspace` thành interface của package đó, và method xuất hiện trong interface với đúng chữ ký cũ.

| Nhóm | Trước | Sau |
| -- | -- | -- |
| G1 | `func (w *Workspace) CrewMeta(project, crew string) string` (`layout.go:183`) | `func (p Paths) CrewMeta(project, crew string) string` (`layout_crew.go`). Bên gọi vẫn viết `ws.CrewMeta(p, c)` |
| G2 | `func New(ws *store.Workspace, d *db.DB, deps Deps) *Ingester` (`internal/timeline/ingest.go:190`) | `func New(ws Workspace, d *db.DB, deps Deps) *Ingester`, với `timeline.Workspace` có `CrewContextHigh() int`, `LoadConfig() error`, `LoadPricing() (store.PricingConfig, error)`… |
| G3 | `func (s *Server)` đọc `s.ws.LoadProject(name)`, `s.ws` là `*store.Workspace` (`internal/dashboard/server.go:112`) | `s.ws` có kiểu `dashboard.Workspace`, trong đó có `LoadProject(name string) (store.ProjectConfig, error)` và `Projects() []store.ProjectRef` |
| G4 | `func Load(ws *store.Workspace, project string) (View, error)` (`internal/box/view.go:68`) | `func Load(ws Workspace, project string) (View, error)`, với `box.Workspace` có `Auto(project string) bool` |
| G5 | `type Deps struct { WS *store.Workspace; … }` (`internal/prwatch/prwatch.go:55`) | `WS Workspace`, với `prwatch.Workspace` có `UpdateCrewMeta(project, crew string, set map[string]string) error` |
| G6 | `func HandleSessionStart(w *store.Workspace, project string, raw []byte) (SessionStart, error)` (`internal/hook/hook.go:173`) | `func HandleSessionStart(w Workspace, …)`, với `hook.Workspace` gồm `ReadMateMeta`, `WriteMateMeta`, `AppendSent` |
| G7 | `func New(ws *store.Workspace, deps Deps) *Watcher` (`internal/watch/watch.go:296`) | `func New(ws Workspace, deps Deps) *Watcher`, với `watch.Workspace` có `AppendIncident(project string, entry store.IncidentEntry) error` và `ReadStatus(project, crew string, from int64) ([]store.StatusEntry, int64, error)` |
| G8 | `func New(ws *store.Workspace, deps Deps) *Pilot` (`internal/autopilot/autopilot.go:133`) | `func New(ws Workspace, deps Deps) *Pilot`, với `autopilot.Workspace` có `ReadAutoCursor(project string) (map[string]int64, error)` và `ReadOutbox(project string) ([]store.OutboxItem, error)` |
| G9 | `func New(ws *store.Workspace, deps Deps) *Sender` (`internal/outbox/outbox.go:205`) | `func New(ws Workspace, deps Deps) *Sender`, với `outbox.Workspace` có `TryMateMaintenance(project string, exclusive bool) (release func(), acquired bool, err error)` |
| G10 | `func (w *Workspace) AppendMigrateLog(e MigrateEntry) error`; bên gọi `ws.AppendMigrateLog(e)` | `func (m Migration) AppendMigrateLog(e MigrateEntry) error`; bên gọi `ws.Migration().AppendMigrateLog(e)` |
| G11 | `func Open(ws *store.Workspace) (*DB, error)` (`internal/db/db.go:79`) | `func Open(ws Workspace) (*DB, error)`, với `db.Workspace` gồm `StateDir() string` và `Resolve(path string) (string, error)` |

## 3. Interface hẹp ở phía bên dùng

### Luật

1. Interface khai báo trong package dùng nó, tên là `Workspace`, đặt trong file `workspace.go` của package đó. Mỗi package có một dòng `var _ Workspace = (*store.Workspace)(nil)` để interface luôn khớp với store.
2. Interface của một hàm gồm các method hàm đó gọi, cộng interface của mọi hàm ở package khác mà nó truyền workspace sang (nhúng interface đó vào). Ví dụ `query.Load` gọi `box.Load`, nên `query.Workspace` nhúng `box.Workspace`.
3. Hàm nào truyền workspace cho một hàm cần `*store.Workspace` thì cũng phải giữ kiểu cụ thể. Vì vậy phải làm từ lá đi lên.
4. `ws == nil` không còn bắt được một `*store.Workspace` nil khi nó nằm trong interface. Package nào đổi kiểu mà có câu kiểm nil thì thay bằng một hàm nhỏ trong package đó: nil thật, hoặc `*store.Workspace` nil. Kèm một test truyền `(*store.Workspace)(nil)`. Chỗ kiểm nil trong các package sẽ đổi: `internal/query/box.go:186`, `internal/query/load.go:51`, `internal/dashboard/server.go:129`, `internal/prwatch/prwatch.go:118`, `internal/spawn/crew_stop.go:451`.

Bên gọi và test không phải sửa gì, vì `*store.Workspace` thoả mọi interface. Đây chính là lớp chuyển tiếp.

### Bên dùng đổi sang interface

Số method trong bảng là closure đo trên 4d88f09. "Nhúng" là interface của package khác mà nó chứa.

| Package | Interface | Method | Nhúng | Ghi qua store? | Bước |
| -- | -- | -- | -- | -- | -- |
| `box` | `box.Workspace` (`Load`, `LoadSince`) | 9 | | không | S4 |
| `query` | `query.BoxWorkspace` (`LoadBox`, `loadBox`); `query.Workspace` (`Load`, `LoadLive`, `load`) | 12; 20 | `box.Workspace` trong `BoxWorkspace`; `BoxWorkspace` trong `Workspace` (`load` → `loadProject` → `loadBox`, `load.go:140`) | không (`LoadConfig` chỉ đọc lại `workspace.yaml`) | S4 |
| `dashboard` | `dashboard.Workspace` (`Options.Workspace`, `Server.ws`) | 26 | `query.BoxWorkspace` | không | S4 |
| `spawn` (chỉ `ListCrews`) | `spawn.CrewReader` | 11 | `box.Workspace` | không | S5 |
| `usage` | `usage.Workspace` | khoảng 31 | `dashboard.Workspace`, `spawn.CrewReader`, `db.Workspace` | không qua store (xem mục 7) | S5 |
| `learnings` | `learnings.Workspace` | 2 | | không qua store (xem mục 7) | S5 |
| `db` | `db.Workspace` | 2 | | (ghi `mate.db`, chủ của nó là `db`) | S5 |
| `timeline` | `timeline.Workspace` | 26 | `box.Workspace` | có: `AppendIncident` | S6 |
| `watch` | `watch.Workspace` | 8 | | có: `AppendIncident` | S6 |
| `autopilot` | `autopilot.Workspace` | 17 | `box.Workspace` | có: `AppendSent`, `SetMode`, `UpdateCrewMeta` | S6 |
| `outbox` | `outbox.Workspace` | 15 | | có | S6 |
| `prwatch` | `prwatch.Workspace` (`Deps.WS`, `Sweep`, `Ensure`…) | 14 | | có | S6 |
| `hook` | `hook.Workspace` | 3 | | có | S6 |

Với `dashboard`, interface cũng là bằng chứng rằng Office/City không ghi gì qua store. S7 giữ điều đó bằng test.

### Bên dùng giữ `*store.Workspace`, và vì sao

- **`cmd/mate`** (58 method, 48 file): đây là gốc lắp ráp (tầng 7). Mỗi lệnh `cmdX` tự mở workspace bằng `resolveWorkspace`, nên không có chỗ nào để truyền interface vào. Mỗi file dùng tối đa 9 method (`review.go`, `memory.go`, `land.go`). Chỗ cần sửa ở `cmd/mate` là dời logic ra package miền (CON-164). Package miền nhận logic đó sẽ khai báo interface của mình theo luật trên.
- **`spawn`** (46 method, 20 file): đây là bên ghi chính. Các hàm của nó gọi nhau và truyền `w` qua lại, nên interface sẽ gần bằng toàn bộ 46 method. Chỉ `ListCrews` (`crew_stop.go:450`) nhận interface, vì `usage` gọi nó, và sau này cả `wake`, `recall`. Nếu `ListCrews` giữ kiểu cụ thể, các package đó không hẹp lại được. Các hàm chỉ đọc khác của `spawn` (`MateStatus`, `status.go:62`, 5 method; `HostLiveness`, `hostproc.go:219`, 4; `CrewDiff`, `diff.go:32`, 5) cũng nhận interface được. Nhưng hôm nay mọi bên gọi chúng (`core`, `migrate`, `recovery`, `cmd/mate`) đều giữ kiểu cụ thể, nên đổi chúng chưa thu hẹp bên nào. Kế hoạch để lại cho tới khi một bên gọi chỉ đọc cần chúng.
- **`wake`, `recall`** (21 và 27 method, đều chỉ đọc trừ `WriteWakeCursor`): `Deps` của chúng có hàm callback nhận `*store.Workspace`. Đó là `wake.Deps.Friction` (`internal/wake/wake.go:42`, gọi ở `:144`) và `recall.Deps.Crews`, `Facts`, `MemoryCheck` (`internal/recall/recall.go:59`, `:68`, `:74`, gọi ở `:304`, `:460`, `:809`). Code của các callback nằm trong `cmd/mate`: `frictionCheck` (`friction.go:37`), `printBacklog` (`backlog.go:67`), `projectFactsLines` (`project.go:307`), `memoryCheck` (`memory.go:229`), nối ở `recall_deps.go:24-34` và `wake.go:59`. Khi `Render` nhận interface thì không truyền tiếp `w` cho các callback này được, trừ khi sửa các hàm đó trong `cmd/mate`. Kế hoạch giữ `wake.Render`, `recall.Render` ở kiểu cụ thể. Chúng hẹp lại được khi CON-164 dời 4 hàm trên ra package miền, hoặc khi đổi callback thành closure không nhận workspace. Cách nào cũng sửa `cmd/mate`, nên phải chờ CON-172 (câu hỏi 5 ở mục 9).
- **`core`**: giữ handle cho engine và truyền nó cho 13 hàm ghi của `spawn` (`StartMate`, `StopMate`, `AddProject`…).
- **`recovery`, `refresh`, `migrate`**: gọi các hàm ghi của `spawn` (`StartMate`, `RelaunchCrew`, `ReloadMate`, `SessionSpec`). `migrate` dùng view `Migration` (S3).

Captain cần chốt việc `spawn` và `cmd/mate` giữ kiểu cụ thể (câu hỏi 2 ở mục 9), vì CON-173 có nêu hai bên này.

## 4. Đích cuối

Sau S8:

- `store.Workspace` vẫn là handle duy nhất mà `store.Open` và `store.Init` trả về. Nó nhúng `Paths` (54 method chỉ ghép đường dẫn, có test giữ không làm I/O). Trên chính `*Workspace` còn 98 method (97 của G2..G9 và G11, cộng `Migration()`), chia vào file theo nhóm. G10 nằm sau `ws.Migration()` (6 method). 13–15 method exported không ai ngoài store dùng sẽ thành unexported (S8).
- 13 package ở bảng mục 3 thấy từ 2 tới khoảng 31 method qua interface của mình, thay vì 140 method exported. 6 bên ghi giữ kiểu cụ thể; `wake` và `recall` giữ kiểu cụ thể tới khi callback của chúng rời `cmd/mate` (danh sách ở mục 3).
- Một test giữ các package chỉ đọc (`box`, `query`, `dashboard`) không có method ghi trong interface của chúng. Test của store giữ mỗi file đúng một nhóm, và giữ file đường dẫn không import gì ngoài `fmt` và `path/filepath`.
- Không đổi định dạng file nào trong `.mate/`, không đổi đường dẫn nào. `mate.db` vẫn dẫn xuất từ các file nguồn (`docs/timeline.md`): kế hoạch không đụng `db` và `timeline` ngoài kiểu của tham số.

## 5. Các bước

Mỗi bước một issue, một PR, không đổi hành vi, tối đa 2 ngày. Không bước nào sửa file trong `cmd/mate` hay `internal/core`, nên không giành file với CON-172, CON-214 và CON-232. Agent không chạy được test live (`MATE_LIVE=1`); không bước nào cần sửa test live.

### S1. Tách đường dẫn thành kiểu `store.Paths` nhúng trong `Workspace`

- Thêm `type Paths struct{ root string }` vào `layout.go`. `Workspace` đổi thành `struct { Paths; cfg …; cfgMu … }`. `Init` (`workspace.go:178`) và `OpenForMigrate` (`:253`) dựng `&Workspace{Paths: Paths{root: root}}`. Code trong store đang viết `w.root` vẫn biên dịch, vì field của struct nhúng cũng được promote.
- Đổi receiver của 54 hàm G1 từ `(w *Workspace)` thành `(p Paths)`, ngay trong file đang chứa chúng: `layout.go` (42), `console_lock.go`, `core_lock.go` (3), `crew_stop_lock.go`, `recover_lock.go`, `migrate.go` (2), `outbox.go` (2), `harness_profile.go`, `review.go`. Chưa dời file.
- Thêm `paths_test.go` (test trong package): bảng ghim kết quả của cả 54 hàm với một gốc cố định. Đây là lưới an toàn cho S2.
- Chỗ gọi ngoài store: 0. `GLOSSARY.md` và `docs/architecture.md` ghi `store.Workspace.CrewHandback`; selector đó vẫn đúng, nên không phải sửa.
- File: chỉ `internal/store/`.
- Cỡ: nửa ngày.
- Chờ: không. CON-213 có thể sửa `core_lock.go` (bước này chỉ đổi 3 dòng receiver ở `:59-66`): bên merge sau gộp.

### S2. Chia file của `internal/store` theo nhóm, thêm test giữ nhóm

- Chỉ dời code, không đổi chữ ký:
  - `layout.go` → giữ phần cấp workspace (gồm cả đường dẫn khoá, `core.*`, `migrate.*`); thêm `layout_project.go`, `layout_mate.go`, `layout_crew.go`; gom 12 hàm G1 từ các file khác về đây.
  - `workspace.go` → giữ kiểu `Workspace`, `Init`, `Open`, `OpenForMigrate`, `resolveRoot`, `SessionName`. Thêm `workspace_config.go` (G2 cùng `Defaults`, `WorkspaceConfig`, `CrewLaunch`, `ProjectRef`, `emptyDocument`). `Projects`, `Project`, `RequireProject` sang `project.go`. `mkdirAll`, `writeFile` sang `fsio.go`.
  - `project.go` → giữ method G3. Thêm `project_config.go` (schema và hằng của `project.yaml`) và `mode.go` (G4). `CrewIDs` sang `meta.go` (G5). `MateHeadless` sang `mate_meta.go` mới, cùng `ReadMateMeta`, `WriteMateMeta` từ `meta.go` (G6). Khoá của `mate.meta` (`MetaMateMode`, `MateModeHeadless`, `MetaMatePID`, `MetaMateSocket`) cùng `MateHostDetached`, `MateRecorded` sang `mate_meta.go`; khoá của `.meta` crew (`MetaBudget*`, `MetaFollowups`) sang `meta.go`. Hằng và hàm thường không bị test nhóm kiểm.
  - `SentLogSize` (`outbox.go`) sang `log.go` (G7).
  - `appendLine` (`log.go`) và `seedOnce` (`crewdoc.go`) sang `fsio.go`.
  - Sau các bước dời này, mỗi file có method chỉ thuộc một nhóm. `workspace.go` không còn method nào.
- File nào có phần lớn nội dung ở lại thì giữ tên cũ, để git còn gộp được các sửa đổi làm song song.
- Thêm `groups_test.go` (package `store`, `go/parser`), với một bảng file → nhóm. Test hỏng khi:
  - một file không phải test khai báo method của `Workspace`, `Paths` hay `Migration` mà không có trong bảng;
  - method của `Paths` nằm ngoài các file `layout*.go`, hoặc file `layout*.go` khai báo method của `*Workspace`;
  - file `layout*.go` import gì khác `fmt` và `path/filepath`.
  Câu báo lỗi nói cách thêm file vào bảng. Bảng chỉ đổi khi có file mới, nên bản thân nó không thành chỗ xung đột mới.
- `doc.go` thêm một đoạn liệt kê các nhóm. `docs/architecture.md` mục 3: sửa câu "Path helpers live in `internal/store/layout.go`" thành `layout*.go`.
- Lưới an toàn: `layout_golden_test.go` (`workspace.yaml`, `project.yaml` từng byte) và `paths_test.go` của S1 qua mà không sửa.
- Cỡ: 1 ngày.
- Chờ: S1, CON-213 (CON-213 sửa `internal/store/` để đọc khoá; S2 dời gần hết các file của store).

### S3. Gom các method chỉ `mate migrate` dùng vào `store.Migration`

- `type Migration struct{ w *Workspace }`, lấy bằng `func (w *Workspace) Migration() Migration`. Dời 6 method G10 sang receiver `Migration`. Path `MigrateLockFile`, `MigrateLog` ở lại `Paths`.
- Bên gọi: 6 chỗ trong `internal/migrate/migrate.go`; test `internal/store/migrate_test.go` và test của `internal/migrate`.
- File: `internal/store/migrate.go`, `groups_test.go`, `internal/migrate/migrate.go` và test.
- Cỡ: nửa ngày.
- Chờ: S2.

### S4. Cho `box`, `query`, `dashboard` nhận interface hẹp

- Thêm `box.Workspace` (9 method), `query.BoxWorkspace` (12 method, nhúng `box.Workspace`, cho `LoadBox`), `query.Workspace` (20 method, nhúng `query.BoxWorkspace`, vì `load` gọi `loadBox` qua `loadProject`, `load.go:140`), `dashboard.Workspace` (26 method, nhúng `query.BoxWorkspace`). `dashboard.Workspace` không có `LoadConfig`: việc đọc lại cấu hình là của engine.
- Đổi tham số và field của các hàm sau, cùng mọi hàm không export trong package nhận workspace từ chúng: `box.Load`, `box.LoadSince` (`view.go:68`, `:76`), `incident.go`; `query.Load`, `LoadLive`, `load` (`load.go:38-51`), `LoadBox`, `loadBox` (`box.go:171-186`); `dashboard.Options.Workspace` (`server.go:98`), `Server.ws` (`:112`), `api.go`, `read.go`.
- Thay 3 câu kiểm nil theo luật 4 ở mục 3; thêm test truyền `(*store.Workspace)(nil)`.
- Bên gọi không đổi: `core` (`dashboard.go:129`), `usage` (`why.go:36`, `learning.go:58`), `cmd/mate`.
- Cỡ: 1 ngày.
- Chờ: không. Không đụng store, nên làm song song được với S1..S3. CON-178 sửa comment trong `query/attention.go`, `types.go`, `field.go`, khác file.

### S5. Cho `usage`, `learnings`, `db` và `spawn.ListCrews` nhận interface hẹp

- Thêm `spawn.CrewReader` (11 method, nhúng `box.Workspace`), chỉ cho `ListCrews` (`internal/spawn/crew_stop.go:450`). Các hàm khác của `spawn` giữ kiểu cụ thể.
- Thêm `usage.Workspace`, `learnings.Workspace`, `db.Workspace` theo bảng ở mục 3. Đổi tham số của các hàm exported, cùng các hàm không export nhận workspace từ chúng: `usage.Write`, `RecordAtClose`; `learnings.Open`, `List`, `Read`, `Close`; `db.Path`, `Open`, `OpenRead`.
- Không đổi `wake` và `recall` (mục 3: callback trong `Deps` của chúng nhận `*store.Workspace`, và code callback nằm trong `cmd/mate`). Hai package này vẫn gọi được `ListCrews` và `db.*` bằng kiểu cụ thể.
- Câu kiểm nil của `ListCrews` (`crew_stop.go:451`) theo luật 4.
- File: `internal/spawn/crew_stop.go`, `internal/usage/{learning,ledger,usage,why}.go`, `internal/learnings/learnings.go`, `internal/db/db.go`, cộng mỗi package một `workspace.go`.
- Cỡ: 1 ngày.
- Chờ: S4 (`usage` nhúng `dashboard.Workspace`). CON-178 sửa comment ADR trong `crew_stop.go`; CON-190 sửa `spawn/deps.go` và test live của `spawn`. Cả hai khác đoạn với bước này; bên merge sau gộp.

### S6. Cho `timeline`, `watch`, `autopilot`, `outbox`, `prwatch`, `hook` nhận interface hẹp

- Thêm interface theo bảng ở mục 3. Đổi tham số và field sau, cùng mọi hàm không export nhận workspace từ chúng (ví dụ `timeline.listCrewMetas`, `watch.openCrews`, `prwatch.sweepCrew`): `timeline.New` (`ingest.go:190`), `Ingester.ws` (`:134`), `timeline/meta.go`; `watch.New` (`watch.go:296`), `Watcher.ws` (`:235`); `autopilot.New` (`autopilot.go:133`), `Pilot.ws` (`:117`), `rearm.go`; `outbox.New` (`outbox.go:205`), `Sender.ws` (`:196`); `prwatch.Deps.WS` (`prwatch.go:55`), `Sweep`, `ReadPID`, `WritePID`, `RemovePID`, `Running`, `Ensure` (`sweep.go`, `detach.go`); `hook.HandlePrompt`, `HandleStop`, `HandleSessionStart` (`hook.go`).
- Câu kiểm nil của `prwatch.Watch` (`prwatch.go:118`) theo luật 4.
- Bên gọi không đổi: `spawn` (`outbox.New`), `recovery` (`prwatch.Sweep`), `cmd/mate`, `scripts/m7measure` (`timeline.New`).
- Cỡ: 1 ngày. Nếu PR quá lớn thì tách hai: `timeline`, `watch` (observer) và `autopilot`, `outbox`, `prwatch`, `hook` (giao tin).
- Chờ: S4 (`timeline` và `autopilot` nhúng `box.Workspace`). CON-226 (Backlog) có thể sửa `outbox` hay `prwatch`; nếu nó chạy trước thì bên merge sau gộp.

### S7. Thêm test: package chỉ đọc không gọi method ghi của store

- Trong `groups_test.go` của S2, thêm cột loại cho mỗi file hoặc nhóm (đường dẫn, đọc, ghi), và luật đặt tên: method ghi bắt đầu bằng một trong các tiền tố `Add`, `Append`, `Archive`, `Edit`, `Ensure`, `Freeze`, `Lock`, `Remove`, `Replace`, `Save`, `Set`, `Try`, `Update`, `With`, `Write`, theo sau là một chữ hoa hoặc hết tên (để `Settings` hay `Address` không bị tính là ghi); method đọc thì không. Luật chỉ áp cho method exported. Review độc lập đã lần theo các lời gọi ghi bên trong store của cả 140 method exported: hôm nay mọi method có tiền tố ghi đều ghi, và không method nào khác ghi. `LoadConfig` tính là đọc: nó đọc lại file vào bộ nhớ, không ghi đĩa. `ReadStatus`, `CrewStopping`, `CoreRunning` và vài hàm khác lấy khoá chia sẻ hoặc khoá thử nhưng không ghi gì.
- Thêm `internal/archtest/store_readers_test.go` (`go/parser`, như `layers_test.go`). Với `box`, `query`, `dashboard`, test kiểm hai điều. Một: interface trong `workspace.go` không có method mang tiền tố ghi. Hai: file không phải test chỉ nhắc `*store.Workspace` ở dòng `var _ Workspace = …`.
- Sửa `docs/architecture.md` mục 1: thêm một đoạn về interface ở bên dùng và test này. Mục 4: thêm một dòng vào bảng boundary test.
- Cỡ: nửa ngày.
- Chờ: S2, S5, S6.

### S8. Bỏ export các method của store không ai ngoài store dùng

- Đo lại trên `main` lúc làm. Trên 4d88f09 có 15 method (danh sách ở mục 1). Giữ export `CoreLogFile` và `CoreHolder` nếu CON-213 đã dùng chúng.
- 9 trong 15 method được test ngoài (`package store_test`) của chính store gọi (mục 1). Với mỗi test đó: dời phần cần method sang một file test trong `package store`, hoặc kiểm qua API exported mà nó phục vụ.
- `GLOSSARY.md` dòng _Trong code_ của **Wake** đang ghi `store.Workspace.WakeCursorFile`: đổi sang `store.Workspace.ReadWakeCursor`.
- File: `internal/store/` (cả test), `GLOSSARY.md`.
- Cỡ: 1 ngày.
- Chờ: S2, CON-213.

### Thứ tự

```
S1 ──> S2 ──> S3
CON-213 ──┘ └──────────┐
                        ├──> S7
S4 ──┬──> S5 ───────────┤
     └──> S6 ───────────┘
S2, CON-213 ──> S8
```

- S1 và S4 làm được ngay, song song với nhau.
- S4..S6 không đụng `internal/store`, nên không phải chờ S2.
- S2 nên làm trong một lần ngồi và merge ngay: chỉ dời code và có test, nên thuộc loại việc nhỏ captain cho merge thẳng.
- Tổng khoảng 6,5 ngày công.
- Sau S8 còn `wake` và `recall`. Hai package này hẹp lại được khi 4 hàm callback (`frictionCheck`, `printBacklog`, `projectFactsLines`, `memoryCheck`) rời `cmd/mate` theo CON-164. Việc đó sửa `cmd/mate`, nên chờ CON-172 và không nằm trong 8 bước này (câu hỏi 5).

## 6. Issue code dự kiến

Chỉ tạo sau khi captain duyệt kế hoạch. Mỗi issue là issue con của CON-181 (như CON-228..232 là con của CON-203, cha của kế hoạch CON-215), trạng thái Todo, có quan hệ blockedBy theo cột "Chờ".

| Bước | Tiêu đề dự kiến | Phạm vi | Chờ | Cỡ |
| -- | -- | -- | -- | -- |
| S1 | Tách đường dẫn của store thành kiểu Paths nhúng trong Workspace | `internal/store/` (receiver của 54 hàm, `paths_test.go`) | không | 0,5 ngày |
| S2 | Chia file của internal/store theo nhóm trách nhiệm, thêm test giữ nhóm | `internal/store/` (dời code, `groups_test.go`, `doc.go`), `docs/architecture.md` mục 3 | S1, CON-213 | 1 ngày |
| S3 | Gom các method chỉ mate migrate dùng vào store.Migration | `internal/store/migrate.go`, `internal/migrate/` | S2 | 0,5 ngày |
| S4 | Cho box, query và dashboard nhận interface hẹp thay cho *store.Workspace | `internal/box/`, `internal/query/`, `internal/dashboard/` | không | 1 ngày |
| S5 | Cho usage, learnings, db và spawn.ListCrews nhận interface hẹp | `internal/{usage,learnings,db}/`, `internal/spawn/crew_stop.go` | S4 | 1 ngày |
| S6 | Cho timeline, watch, autopilot, outbox, prwatch và hook nhận interface hẹp | `internal/{timeline,watch,autopilot,outbox,prwatch,hook}/` | S4 | 1 ngày |
| S7 | Thêm test giữ các package chỉ đọc không ghi qua store | `internal/store/groups_test.go`, `internal/archtest/`, `docs/architecture.md` mục 1, 4 | S2, S5, S6 | 0,5 ngày |
| S8 | Bỏ export các method của store không ai ngoài store dùng | `internal/store/` (cả test), `GLOSSARY.md` | S2, CON-213 | 1 ngày |

Theo câu trả lời của captain ở mục 9: S8 có thể bỏ (câu 4), S4..S7 có thể đổi tên interface (câu 3). Nếu captain muốn hẹp cả `spawn` hay `cmd/mate` (câu 2) hoặc `wake`, `recall` (câu 5), thì thêm issue cho phần đó sau khi có câu trả lời, không tạo trước.

## 7. Rủi ro và phát hiện

- **Nil nằm trong interface.** Một `*store.Workspace` nil gán vào interface thì interface khác nil, và lời gọi method sẽ panic trong store. Luật 4 ở mục 3 có danh sách 5 câu kiểm nil cần thay, kèm test cho từng cái.
- **Callback trong `Deps` nhận `*store.Workspace`.** Lần đo đầu chỉ theo lời gọi hàm, nên bỏ sót các field kiểu hàm (`wake.Deps.Friction`, `recall.Deps.Crews`, `Facts`, `MemoryCheck`); review độc lập tìm ra chúng. Trên 4d88f09, ngoài `store`, `spawn` và `core`, chỉ 4 field này có kiểu hàm nhận `*store.Workspace` (`grep -rn 'func(.*\*store.Workspace' internal`). Bước nào làm thì grep lại.
- **Interface "hẹp" vẫn lớn ở vài chỗ:** `usage` khoảng 31, `dashboard` và `timeline` 26. Giá trị nằm ở chỗ danh sách được viết ra, trình biên dịch giữ nó, và bên chỉ đọc chứng minh được là không ghi. Không chia nhỏ thêm, vì hàm của các package này gọi lẫn nhau.
- **Dời file làm hỏng nhánh đang mở.** Một nhánh sửa đoạn code bị dời sẽ xung đột với S2. Trước khi làm S2: kiểm `gh pr list` và `git branch -r` xem nhánh nào sửa `internal/store`, giữ tên cũ cho phần lớn nội dung, và merge ngay trong ngày.
- **`Paths` exported và nhúng**, nên bên ngoài có thể viết `ws.Paths = …`. Field `root` không export, nên bên ngoài chỉ gán được giá trị rỗng hoặc bản chép của một workspace khác. Không ai làm vậy hôm nay. Nếu captain muốn chặn hẳn, dùng kiểu `paths` không export: method vẫn được promote, chỉ mất khả năng nhận `store.Paths` như một giá trị.
- **Thêm cuộc gọi store vào package đã đổi:** Crew phải thêm một dòng vào interface của package. Trình biên dịch báo ngay chỗ nào thiếu. Đó là chủ ý.
- **Phát hiện ngoài phạm vi.** `internal/usage/learning.go:88`, `:116` và `internal/learnings/learnings.go:185` ghi file learning bằng `os.WriteFile`, không qua phép kiểm ranh giới của store, dù `internal/store/doc.go` nói store là package duy nhất ghi `.mate/`. `docs/architecture.md` mục 3 đã ghi `learnings` là chủ của file đó. Kế hoạch này không sửa; cần một issue riêng nếu captain muốn.
- **Một trùng lặp.** `ReplaceCrewBrief` (`crewdoc.go:90`) và `WriteCrewBrief` (`migrate.go:155`) cùng ghi `brief.md`, chỉ khác ở chỗ hàm đầu kiểm file đã có. S3 tách `WriteCrewBrief` vào `Migration`, nên phần còn lại của code không lẫn hai hàm được.

## 8. Không làm

- Đổi định dạng hay đường dẫn của file nào trong `.mate/`; đổi quan hệ giữa `mate.db` và các file nguồn.
- Đổi cách khoá: G9 chỉ được xếp nhóm, không sửa.
- Tách `store` thành package con, hay thêm hàm truy cập theo nhóm cho mọi bên gọi (mục 2).
- Interface cho phần ghi của `spawn`, cho `core`, `recovery`, `refresh`, `migrate`, `wake`, `recall` và các lệnh trong `cmd/mate` (mục 3).
- Sửa bất kỳ file nào trong `cmd/mate` hay `internal/core`.
- Viết bản giả (fake) của store cho test: test vẫn dùng workspace thật trong `t.TempDir()`.
- Sửa các chỗ ghi file không qua store ở mục 7.

## 9. Câu hỏi còn mở

| # | Câu hỏi | Mặc định đề xuất |
| -- | -- | -- |
| 1 | Giữ một handle `*store.Workspace`, chia file theo nhóm và cho bên dùng khai báo interface hẹp, thay vì tách thành nhiều kiểu hay package con có lớp chuyển tiếp? | Giữ một handle. Tách kiểu riêng chỉ cho G1 (`Paths`) và G10 (`Migration`), lý do ở mục 2 |
| 2 | `spawn` (trừ `ListCrews`), `core`, `cmd/mate`, `recovery`, `refresh`, `migrate` giữ `*store.Workspace`? CON-173 nêu `spawn` và `cmd/mate` trong danh sách bên dùng chính. | Có. `cmd/mate` hẹp lại dần khi logic dời ra theo CON-164; `spawn` là bên ghi chính, và các hàm chỉ đọc của nó (`MateStatus`, `HostLiveness`, `CrewDiff`) chỉ được bên giữ kiểu cụ thể gọi |
| 3 | Tên interface ở bên dùng: `Workspace` trong từng package (`box.Workspace`, `query.Workspace`…), hay tên khác? | `Workspace`, đặt trong file `workspace.go` của package đó |
| 4 | Có làm S8 (bỏ export 13–15 method không package nào khác dùng, kèm sửa test `store_test` của 9 method) không? | Có, sau CON-213, đo lại lúc làm |
| 5 | Có hẹp `wake` và `recall` không? Muốn vậy phải sửa `cmd/mate`: dời 4 hàm callback ra package miền (CON-164), hoặc đổi callback thành closure không nhận workspace. | Để sau CON-172. Khi CON-164 dời 4 hàm đó thì làm luôn, không mở issue riêng trước |
