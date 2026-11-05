# Dời code hành động ra khỏi `cmd/mate`

- Ngày: 2026-11-05; sửa theo review độc lập cùng ngày.
- Trạng thái: kế hoạch của CON-215. Các bước B1 đến B5 ở mục 5 là issue con của CON-203: B1 CON-228, B2 CON-229, B3 CON-230, B4 CON-231, B5 CON-232.
- Baseline đã đọc: `origin/main` f2baf16 (sau đợt 5 của CON-203: CON-209, CON-166, CON-188, CON-220, CON-221).
- Liên quan: CON-203 (tách core), CON-164 (đưa logic ra khỏi `cmd/mate`), CON-172 (test `cmd/mate` chạy song song), CON-216 (Mate headless ngoài Herdr).
- Tài liệu cần sửa khi làm: `docs/architecture.md` mục 1 (nếu thêm package) và mục 2 (đoạn `Engine.Act`).

## 1. Hiện trạng

Từ CON-209, mọi hành động của console và của Office/City vào core qua một cửa: `Engine.Act` (`internal/core/act.go:30`). `Act` giữ khoá theo target, tự chạy 5 hành động chat (`talk`, `act.go:126`), còn lại gọi `Options.Act` (`internal/core/engine.go:107-111`). `Options.Act` là `engineActions` của `cmd/mate` (`cmd/mate/console_actions.go:112`), gắn ở `cmd/mate/console.go:176`.

Phần thân của các hành động đó vẫn ở `cmd/mate`:

| File | Dòng | Nội dung |
| -- | -- | -- |
| `console_actions.go` | 420 | `consoleAction` (switch theo hành động), `engineActions`, `tuiAction`, 11 hàm `*Action` |
| `console_box.go` | 359 | 5 hàm `*Action` (`resolve`, `reply`, `peek`, `restart_mate`, `clear_composer`); `restartHolds`, `refusalReason`, `mateNotRecorded`, `restartHarness`, `boxSendRefusal` |
| `console_notice.go` | 181 | `explain_notice` (`consoleNoticeAction`), cấu hình Jev (`consoleNoticeClient`, `jevSettings`, `jevClient`, `keyFilePath`) |

Test gọi thẳng các hàm này nằm ở khoảng 15 file test thường và 13 file test live (mục 5, từng bước).

`internal/core` không được link tới `cmd/` hay `internal/ui/` (`internal/core/boundary_test.go`). Vì vậy hàm hành động nào gọi một hàm của `cmd/mate` thì phải dời hàm đó xuống trước. Phần lớn các hàm đó cũng là hàm của lệnh CLI.

## 2. Từng hành động gọi gì trong `cmd/mate`

Cột cuối là lệnh CLI cũng gọi hàm đó. "Không" nghĩa là hành động chỉ gọi package `internal/` (thường là `spawn`, `store`, `matehost`), dời được ngay.

| Hành động (`coreapi`) | Hàm thân | Hàm của `cmd/mate` mà nó gọi | Lệnh CLI cũng dùng hàm đó |
| -- | -- | -- | -- |
| `start`, `resume`, `onboard` (project) | `startMateAction` | `consoleHarness` → biến global `harnesses` (`harnesses.go:17`) | `harnesses`: gần như mọi lệnh. `consoleHarness` chỉ hành động dùng |
| `onboard` (workspace) | `addProjectAction` | `addProject` (`project.go:135`) → `ensureProjectDoc` (`project.go:335`) → `projectDocTemplate` (`project.go:349`); `repoConfigFor` (`project.go:104`) → `gitTopLevel`, `detectDefaultBranch` (`git.go`) | `mate project add` (`project.go:60`, `:66`), `mate project repo add` (`project_repo.go:70`) |
| `stop` (Mate) | nhánh trong `consoleAction` | không | |
| `stop` (crew) | `stopCrewAction` | `crewStopReport` (`crew.go:362`); `recordUsageAtClose` (`learning_usage.go:34`) → `usageBlock` → `dashboardDeps` (`dashboard.go:100`), `writeWhyHead` (`usage_why.go:71`), `upsertLearningSection`, `oneLineMsg` (`hook.go:174`) | `crewStopReport`: `mate crew stop` (`nudge.go:34`), `mate project remove` (`project.go:256`). `recordUsageAtClose`: `mate crew stop` (`crew.go:287`), `mate merge` (`merge.go:59`), `mate land` (`land.go:100`, `:109`), lần dừng crew vì vượt ngân sách của autopilot (`crew_budget.go:177`) |
| `repair` | nhánh trong `consoleAction` | không | |
| `mode` | `toggleModeAction` | không | |
| `resolve` | `boxResolveAction` | `consoleOutbox` (`console_auto.go:151`) → `consoleMateHandle`, `clockFunc`, `sleepFunc`; `refusalReason` | `consoleOutbox`: `mate mate stop` (qua `stowBeforeStop`, `mate.go:166`), `mate pr watch` (`pr.go:131`), `mate mate refresh` (qua `mateRefresher`, `console_auto.go:205`); vòng outbox và autopilot của engine (`console_auto.go:140`, `:186`) |
| `reply` | `boxReplyAction` | không | |
| `peek` | `boxPeekAction` | `screenOf` (`send.go:247`) | `mate send`, `mate peek`, `mate state`, `mate brief append` (qua `resolveCrewHandle`, `send.go:218`); observer của engine (`console_watch.go:141`) |
| `diff` | `crewDiffAction` | `crewDiffText` (`diff.go:74`) | `mate diff` (`diff.go:49`); `mate dashboard` và dashboard của engine (`dashboardDeps.Diff`, `dashboard.go:105`) |
| `restart_mate` | `restartMateAction` | `consoleOutbox`; `recallClip` (`recall.go:898`); `restartHarness` → `harnesses`; `mateNotRecorded`; `restartHolds` | `recallClip`: `mate recall`, `mate land` (`land.go:377`, `:383`) |
| `restart_crew` | `relaunchCrewAction` | không | |
| `clear_composer` | `clearComposerAction` | không | |
| `remove_project` | `removeProjectAction` | `removeProjectOptions` (`project.go:240`) → `stowBeforeStop` (`mate.go:155`) → `consoleOutbox` | `mate project remove` (`project.go:227`); `stowBeforeStop`: `mate mate stop` (`mate.go:136`) |
| `merge` | `mergeCrewAction` | `recordUsageAtClose` | như ở `stop` (crew) |
| `open_news`, `open_work`, `okay_work` | `openNewsAction`, `openWorkAction`, `okayWorkAction` | `requireProject` (`memory.go:104`) | `requireProject`: 15 file lệnh CLI (`backlog.go`, `checkpoint.go`, `land.go`, `memory.go`, `recall.go`, `work.go`…) và `console_commands.go` |
| `explain_notice` | `consoleNoticeAction` | `screenOf`; client Jev do `consoleNoticeClient` dựng ở `console.go:159` | `jevSettings`, `jevClient`: `configuredObserver` (`jev_observer.go:33`, `:41`), mà `liveDeps` gọi cho mọi lệnh đọc pane |
| `say`, `interrupt`, `answer`, `fork`, `fork_close` | `Engine.talk` | đã ở `internal/core` (CON-209) | |

## 3. Chỗ mới cho từng hàm

Luật của issue: hàm mà CLI cũng dùng thì đặt vào package miền, không vào `internal/core`. Hàm chỉ hành động dùng thì vào `internal/core`.

| Hàm hôm nay | Chỗ mới | Ghi chú |
| -- | -- | -- |
| `requireProject` | `(*store.Workspace).RequireProject(name) error` | `requireProject` ở `cmd/mate` thành một dòng gọi sang store, để không phải sửa 16 file gọi nó (trong đó có `recall.go` mà CON-167 đang dời). CON-172 có thể bỏ hàm bọc sau |
| `screenOf` | `harness.Registry.Screen(kind) (ScreenProfile, error)` | `deps.Harnesses` đã là `harness.Registry` |
| `crewStopReport` | `spawn.StopResult.CrewLine(project, crew string) string` | theo mẫu `spawn.MergeResult.Line` (`internal/spawn/merge.go:87`) |
| `consoleOutbox`, `consoleMateHandle`, `clockFunc`, `sleepFunc` | `spawn.MateOutboxDeps(ws, deps) outbox.Deps` và `spawn.MateOutbox(ws, deps) *outbox.Sender` (= `outbox.New(ws, MateOutboxDeps(ws, deps))`) | `spawn` chưa import `outbox`, và `outbox` không kéo theo `spawn` (`go list -deps ./internal/outbox`), nên không có vòng import. Cùng tầng service. Tách `MateOutboxDeps` ra để người gọi thêm field vào `outbox.Deps` được: CON-211 thêm điều kiện "có console" cho vòng xả outbox |
| `stowBeforeStop` | `spawn.StowBeforeStop(...)` | giữ nguyên chữ in ra stderr |
| `removeProjectOptions` | hàm trong `spawn` cạnh `RemoveProject` (`internal/spawn/project_remove.go`) | trả `spawn.RemoveProjectOptions` có `Stow` gọi `StowBeforeStop` |
| `crewDiffText`, `ensureTrailingNewline` | `spawn.CrewDiff(ctx, ws, git, project, crew, stat)` | hàm đọc meta của crew bằng khoá của `spawn` (`MetaBranch`, `MetaWorktree`). `dashboardDeps.Diff` vẫn là chỗ nối, vì `dashboard` không import `spawn` (CON-165) |
| `addProject`, `projectAddOptions` | `spawn.AddProject(ws, name, opts)` | cạnh `spawn.RemoveProject` |
| `ensureProjectDoc`, `projectDocTemplate` | `(*store.Workspace).EnsureProjectDoc(name)` | `store` đã import `memory` (nơi có `memory.ProjectTemplate`). `m7_empty_repo_live_test.go:71` gọi `projectDocTemplate`: đổi sang `memory.ProjectTemplate` |
| `repoConfigFor`, `gitTopLevel`, `detectDefaultBranch` | `spawn.RepoConfigFor(ctx, git, ...)` trên `gitx`; thêm `gitx.Git.SymbolicHead` cho `symbolic-ref --short HEAD` | không dùng `gitx.CurrentBranch`: `rev-parse --abbrev-ref HEAD` lỗi trên repo chưa có commit, mà M7 cho phép repo rỗng. `runGit` ở lại `cmd/mate` cho `project_repo_source.go` |
| `recordUsageAtClose`, `usageBlock`, `upsertLearningSection`, `learningUsageHeading` | `internal/usage` (package CON-168 tạo) | `writeWhyHead` theo CON-168 vào đó. `internal/usage` import `dashboard`, nên nằm ở tầng service, và `dashboard` không được import ngược lại. CON-168 để `learning_usage.go` ngoài phạm vi của nó vì file thuộc `mate learnings`; dời phần ghi usage vào `internal/usage` là quyết định của kế hoạch này. `usageBlock` dựng `dashboard.New` với `dashboardDeps`, nhưng `CrewPerformance` chỉ đọc `Deps.Now` (`internal/dashboard/performance.go:26`), nên ở package mới truyền `dashboard.Deps{}`. `oneLineMsg` chép riêng, như `internal/wake` đã làm (`internal/wake/wake.go:620`, bản chép ở `:660`) |
| `recallClip` (chỗ `restart_mate` dùng) | chép riêng trong `internal/core` | chép đúng bản ở `recall.go:898` (gộp khoảng trắng, cắt `r[:n]` rồi thêm "…"). Không dùng `wake.ClipRunes` (`internal/wake/wake.go:611`): hàm đó cắt `r[:n-1]`, câu "held: …" sẽ đổi. `recallClip` thật đi theo CON-167 |
| `harnesses` (chỗ `consoleHarness`, `restartHarness` dùng) | `Options.Spawn.Harnesses` của engine | trong console, `deps.Harnesses` chính là `harnesses` (`baseDeps`, `jev_observer.go:65`), và fixture test cũng vậy (`console_wiring_test.go:49`). Engine có `Spawn.Harnesses` rỗng thì `Parse` nào cũng lỗi: hành động trả lỗi đó, không panic |
| 16 hàm `*Action` (11 ở `console_actions.go`, 5 ở `console_box.go`), switch của `consoleAction`, `restartHolds`, `refusalReason`, `mateNotRecorded`, `restartHarness`, `consoleHarness`, `peekLines`, `clearComposerKey`, `restartConfirmWindow`, `consoleNoticeAction`, `noticeLabels`, kiểu `noticeClassifier` | `internal/core` (file mới, ví dụ `act_mate.go`, `act_crew.go`) | chỉ hành động dùng. `restartHolds` thành field của `Engine`. `noticeClassifier` thành kiểu exported của core, ví dụ `core.NoticeClassifier` |
| `boxSendRefusal` | một file `_test.go` trong `cmd/mate` | chỉ test gọi (`console_box_test.go:505`, `acceptance_two_projects_live_test.go:721`) |
| `consoleNoticeClient`, `jevSettings`, `jevClient`, `keyFilePath`, `jevMode` | ở lại `cmd/mate`, đổi tên file thành `jev.go` | là phần dựng dependency: `configuredObserver` và `liveDeps` cũng dùng. Engine nhận client qua `Options.Notice` |
| `tuiAction` | ở lại `cmd/mate` | chỉ console dùng; CON-214 thay bằng `HeadlessOnly` trong request của client |
| `engineActions` | bỏ (B5) | engine tự dựng hành động |

## 4. Đích cuối

Sau B5:

- `Engine.Act` tự chạy mọi hành động trong `coreapi`. Hành động nào chưa có thì trả "<action> is not wired in this build" như hôm nay (`console_actions.go:100`). `explain_notice` khi không có Jev cũng trả đúng câu đó, như hôm nay (không có Jev thì `engineActions` không bọc `consoleNoticeAction`, `console_actions.go:114`).
- `core.Options` có `Notice core.NoticeClassifier`. `cmd/mate` chỉ gán khi client khác nil: `consoleNoticeClient` trả `(*notice.Client)(nil)` khi Jev tắt, và con trỏ nil đó gán vào interface thì interface khác nil. Nếu gán nhầm, trang web sẽ mời Jev và hành động gọi `Classify` trên client nil. `DashboardOptions.Deps.Notice` lấy từ `Options.Notice != nil`, không do `cmd/mate` điền nữa.
- `cmd/mate` không còn `console_actions.go` và `console_box.go`. `console_notice.go` đổi thành `jev.go`, chỉ còn phần đọc cấu hình Jev.
- Process tạo engine (từ CON-214 chỉ còn `mate core`, `cmd/mate/core.go`) truyền `Workspace`, `Spawn`, `Notice`. Không truyền hàm hành động.
- Các builder của vòng nền (`engineOptions`, `consolePilot`, `mateRefresher`, `consoleBudgetActions`, GC) ở lại `cmd/mate`. Chúng là phần dựng dependency, và CON-211 dùng lại chúng ở `cmd/mate/core.go`. Kế hoạch này không dời chúng.
- Test của hành động phần lớn ở lại `cmd/mate` và gọi `Engine.Act` qua một helper (B4). Dời chúng sang `internal/core` không bắt buộc; bước nào dời thì phải dựng lại fixture (`consoleFixture`, `newBoxFixture`) bên đó.

## 5. Các bước

Mỗi bước một issue, một PR, không đổi hành vi trừ những chỗ ghi ở mục 6. Agent không chạy được test live (`MATE_LIVE=1`). Bước nào sửa test live thì PR ghi rõ test đó mới chỉ được biên dịch, và captain chạy `MATE_LIVE=1` cho các test đó trước khi merge.

### B1 (CON-228). Dời các hàm nhỏ mà hành động và CLI dùng chung xuống package miền

- Hàm: `requireProject` → `store`; `screenOf` → `harness.Registry.Screen`; `crewStopReport` → `spawn.StopResult.CrewLine`; `consoleOutbox` → `spawn.MateOutboxDeps`, `spawn.MateOutbox`; `stowBeforeStop`, `removeProjectOptions` → `spawn`.
- File: `internal/store/` (thêm `RequireProject`), `internal/harness/registry.go`, `internal/spawn/` (file mới cho outbox và stow, `stop.go`, `project_remove.go`); trong `cmd/mate`: `memory.go`, `send.go`, `console_watch.go`, `console_box.go`, `console_actions.go`, `console_notice.go`, `crew.go`, `nudge.go`, `project.go`, `mate.go`, `pr.go`, `console_auto.go` và test của chúng.
- Không sửa `recall.go`, `usage.go`, `usage_why.go` (CON-167, CON-168 đang làm).
- Cỡ: khoảng 1 ngày.
- Chờ: không. Sửa cùng chỗ dựng outbox với CON-211 (`console_auto.go`): bên merge sau gộp lại.

### B2 (CON-229). Dời `mate diff` và phần đăng ký project ra khỏi `cmd/mate`

- Hàm: `crewDiffText` → `spawn.CrewDiff`; `addProject`, `repoConfigFor` → `spawn`; `ensureProjectDoc`, `projectDocTemplate` → `store`; thêm `gitx.Git.SymbolicHead`.
- File: `internal/spawn/` (file mới `diff.go`, `project_add.go`), `internal/store/`, `internal/gitx/gitx.go`; trong `cmd/mate`: `diff.go`, `dashboard.go`, `project.go`, `project_repo.go`, `git.go`, `console_actions.go`; test: `diff_test.go` (268 dòng, dời phần kiểm nội dung sang `internal/spawn`), `project_test.go`, `multi_repo_test.go`, `console_project_add_test.go`, `m7_empty_repo_live_test.go` (`projectDocTemplate`), `console_diff_live_test.go`.
- Cỡ: khoảng 1,5 ngày.
- Chờ: B1 (cùng sửa `project.go` và `console_actions.go`).

### B3 (CON-230). Dời phần ghi usage vào learning record sang `internal/usage`

- Hàm: `recordUsageAtClose`, `usageBlock`, `upsertLearningSection`, `learningUsageHeading` → `internal/usage`. `writeLearningUsageLine` (chỉ in) ở lại `cmd/mate`.
- File: `cmd/mate/learning_usage.go`, `crew.go`, `merge.go`, `land.go`, `crew_budget.go`, `console_actions.go`; test: `learning_usage_test.go` (dời), `land_test.go` (đổi tên gọi).
- Cỡ: khoảng 1 ngày.
- Chờ: CON-168 (tạo `internal/usage` và dời `writeWhyHead`). Sửa `land.go` cùng CON-167 (CON-167 sửa chỗ gọi `recallClip` ở `land.go:377`, `:383`; B3 sửa `:100`, `:109`): khác đoạn, bên merge sau gộp.

### B4 (CON-231). Cho core tự chạy các hành động của Mate và project

- Hành động: `start`, `resume`, `onboard` (cả hai dạng), `stop` (Mate), `repair`, `mode`, `restart_mate`, `clear_composer`, `remove_project`, `open_news`, `open_work`, `okay_work`.
- `Engine.Act`: hành động core đã có thì core chạy; còn lại vẫn gọi `Options.Act` tới B5. `restartHolds` thành field của `Engine`. Engine có `Workspace` nil thì hành động trả lỗi như `talk` (`act.go:128-130`).
- Test: thêm một helper trong `cmd/mate` trả `core.New(core.Options{Workspace: w, Spawn: deps, Act: engineActions(w, deps, nil)}).Act`, như `console_headless_only_test.go:39` đang dựng engine không `Start`. Mọi test đang gọi `consoleAction` chuyển sang helper này một lần, kể cả 12 test live: hầu hết vừa start Mate vừa gọi một hành động của B5 trên cùng hàm (`cold_mate_live_test.go:95`, `console_box_live_test.go:91`, `assign_queue_live_test.go:97`, `merge_live_test.go:225`…), nên helper phải có `Act:` cho tới B5. Engine chưa `Start` vẫn phải chạy được `Act` sau CON-210.
- File: `internal/core/` (file hành động mới), `cmd/mate/console_actions.go`, `cmd/mate/console_box.go`; test: `console_wiring_test.go`, `console_recovery_test.go`, `console_project_add_test.go`, `console_project_remove_test.go`, `console_headless_only_test.go`, `console_box_test.go` (`newBoxFixture` start Mate qua `consoleAction`), và các test live gọi `consoleAction`.
- Cỡ: khoảng 2 ngày.
- Chờ: B1, B2, CON-210 (cùng sửa `Engine.Act` trong `internal/core/act.go`). Sửa `core.Options` trong `engine.go` cùng CON-211 (CON-211 thêm `ConsolePresent`): khác field, bên merge sau gộp.

### B5 (CON-232). Cho core tự chạy các hành động crew, hộp thư và Jev; bỏ `engineActions`

- Hành động: `stop` (crew), `restart_crew`, `merge`, `diff`, `resolve`, `reply`, `peek`, `explain_notice`.
- Thêm `Options.Notice` (mục 4: chỉ gán khi client khác nil, có test); `DashboardOptions.Deps.Notice` lấy từ đó.
- Bỏ `engineActions`, `console_actions.go`, `console_box.go`; `boxSendRefusal` sang file test; `console_notice.go` đổi thành `jev.go`. Helper test của B4 bỏ `Act:`.
- `Options.Act`: core tự chạy mọi hành động, nên field này chỉ còn làm chỗ thay cho test khoá của core (`internal/core/act_test.go`, `dashboard_test.go` dùng `h.act`). Luật "Act nil thì dashboard chỉ đọc" (`internal/core/dashboard.go:27-28`, `:129-135`) phải đổi thành một field rõ ràng, vì sau bước này Act nil là bình thường.
- File: `internal/core/`, chỗ dựng engine (sau CON-214 là `cmd/mate/core.go`), `cmd/mate/console_dashboard.go` hoặc chỗ CON-214 dời phần dựng dashboard tới, `console_notice.go`; test: `console_box_test.go`, `console_crew_stop_test.go`, `console_notice_test.go` (phần hành động), `console_dashboard_test.go`, `console_load_test.go`, `console_headless_only_test.go`, `peek_test.go:93`, `jev_observer_test.go:172`.
- Sửa `docs/architecture.md` mục 2: bỏ dòng **Target** của CON-215.
- Cỡ: khoảng 2 ngày.
- Chờ: B2, B3, B4, CON-214. CON-214 viết lại `console.go` và `console_dashboard.go`, bỏ engine khỏi console và thay `tuiAction`. Làm B5 sau CON-214 thì B5 chỉ sửa một chỗ dựng engine, và không giành `console.go` với CON-214. Field `jev` của `GET /api/core` (CON-212) đọc từ `Dashboard.Deps.Notice` như hôm nay; B5 đổi nguồn của nó sang `Options.Notice`.

### Thứ tự

```
B1 ──> B2 ──┐
            ├──> B4 ──┐
CON-210 ────┘         ├──> B5 ──> CON-172
CON-214 ──────────────┤
CON-168 ──> B3 ───────┘
```

- B1 làm được ngay. B3 làm song song với B1, B2 khi CON-168 xong.
- CON-172 chờ cả 5 bước, vì nó sửa gần như mọi file của `cmd/mate`.
- CON-211..214 không chờ bước nào ở đây. B4 làm được trước hay sau CON-211..214.
- CON-169, CON-170, CON-171 đã xong; không bước nào chờ chúng.

## 6. Thay đổi hành vi

### Do việc dời

- **Câu lỗi git khi đăng ký repo (B2).** `repoConfigFor` hôm nay gọi `git` qua `runGit`; phần lỗi git trong "repo path X is not a git repository: …" có dạng `git rev-parse --show-toplevel: <stderr>`. Qua `gitx` thì thành `git rev-parse --show-toplevel failed in <dir> (exit 128): <dòng đầu stderr>` (`internal/gitx/gitx.go:131-134`). Phần đầu câu giữ nguyên, và test chỉ kiểm "not a git repository" (`project_test.go:174`, `console_project_add_test.go:64`). Kế hoạch chấp nhận thay đổi này.
- **`crewDiffText` với workspace nil (B2).** Hôm nay trả `usageError` của `cmd/mate`, mã thoát 2 (`cmd/mate/main.go:27-31`). Ở `spawn` thì thành lỗi `observability.CodeUsage`, mã thoát 1. Không lệnh nào gọi với workspace nil (`resolveWorkspace` không trả nil mà không kèm lỗi), nên không ai thấy.

Ngoài hai chỗ trên, các bước chỉ dời code: chữ trả về của mọi hành động giữ nguyên, và test kiểm nội dung dời theo mà không sửa chuỗi mong đợi.

### Do hành động chạy trong process core (CON-214), không do việc dời

Các thay đổi này xảy ra khi console thành client (CON-214), dù code hành động nằm ở đâu. Ghi ở đây vì issue CON-215 yêu cầu.

- **`restartHolds` sống qua lần restart console.** `restart_mate` bị giữ lại khi composer của Mate còn chữ chưa gửi. Lần bấm thứ hai trong 2 phút (`restartConfirmWindow`) thì restart mà không stow, và chữ đó mất. Comment ở `cmd/mate/console_box.go:278-282` nói: restart console giữa hai lần bấm thì console hỏi lại, "which is the safe side". Từ CON-209, console và web đã dùng chung một `restartHolds` cho mỗi engine. Từ B4, nó là field của `Engine`; từ CON-214, engine nằm ở core. Khi đó restart console không xoá lần giữ; chỉ restart core mới xoá. Câu hỏi cho captain ở B4 (CON-231); mặc định giữ trong bộ nhớ của core, không ghi xuống đĩa.
- **Đổi `.mate/.env` của Jev cần restart core.** Client Jev dựng một lần lúc engine bật (`console.go:159`). `docs/jev-notices.md:17` viết "mở lại console để đọc `.env`". Từ CON-214 phải là `mate core restart`. Lỗi cấu hình Jev hôm nay hiện ở status line của console (`console.go:251-256`); CON-212 đã đưa chúng vào `notices` của `GET /api/core`. Ghi ở CON-214.
- **Môi trường process.** Khi hành động chạy trong core, các package sau đọc môi trường của process core chứ không phải của console:
  - `spawn` đọc `HERDR_CONFIG_PATH`, `XDG_CONFIG_HOME`, `HOME` (`internal/spawn/deps.go:326-337`); `runtime.Herdr` đọc cùng ba biến (`internal/runtime/herdr.go:268-274`) và chuyển nguyên `os.Environ()` (PATH, key API…) cho `herdr` (`:421`);
  - harness đọc thư mục cấu hình của mình từ biến môi trường (`internal/harness/claude/locator.go:17`, `:42`; `internal/harness/codex/locator.go:17`);
  - console tìm `herdr` bằng `findTool(os.Getenv, "herdr")` (`console.go:145`).

  Core do console bật (CON-214) nên thường cùng môi trường; core bật bằng tay từ shell khác thì có thể khác. Ghi ở CON-214.

## 7. Quan hệ với các việc đang chạy

- CON-210 sửa `Engine.Act` và `Engine.Stop` (từ chối hành động khi đang tắt, huỷ hành động đang chạy). B4 chờ CON-210 để không xung đột ở `internal/core/act.go`.
- CON-211 thêm `ConsolePresent` vào engine và điều kiện cho vòng xả outbox. B1 tách `spawn.MateOutboxDeps` để chỗ dựng sender của engine thêm được điều kiện đó.
- CON-214: B5 chờ (mục 5).
- CON-167 dời `recall.go`. B1 không sửa `recall.go`: `requireProject` giữ tên ở `cmd/mate`, và `restart_mate` dùng bản chép riêng của `recallClip`. CON-167 và B3 cùng sửa `land.go`, khác đoạn.
- CON-168 tạo `internal/usage`. B3 chờ nó. Nếu CON-168 chọn tên package khác thì B3 theo tên đó.
- CON-223 sửa `internal/matehost/repl*.go`, `cmd/mate/matehost.go`, `dispatch.go`, không đụng file nào của B1..B5. `open_news` và `open_work` hôm nay ghi file yêu cầu cho REPL (`matehost.RequestNewsFocus`, `RequestWorkFocus`). Nếu CON-223 hoặc CON-224 đổi cách REPL nhận yêu cầu đó, bên merge sau sửa chỗ gọi.
- CON-216 (CON-225, CON-226) đổi cách bật host của Mate headless trong `spawn`. Hành động `start`, `restart_mate` chỉ gọi `spawn.StartMate`, nên không phải sửa theo.

## 8. Không làm

- Dời các builder của vòng nền và của dashboard (`engineOptions`, `consolePilot`, `mateRefresher`, `consoleBudgetActions`, `dashboardDeps`) ra khỏi `cmd/mate`.
- Cho lệnh CLI đi qua `Engine.Act` (CON-203 đã loại).
- Đổi chữ trả về của hành động.
- Dời `jevSettings`, `jevClient` sang `internal/notice`. Có thể làm sau, nhưng không cần cho đích ở mục 4.
