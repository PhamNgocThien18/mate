# Trạng thái pane lúc khởi động: stub trước, Jev khi stub chịu thua

Ngày: 2026-10-13. Trạng thái: triển khai xong; live proof 2026-10-13 (docs/archive/evidence/pane-state-jev-2026-10-13.md).

## 1. Vấn đề

Tạo Mate bằng Codex 0.162.1 cho project `matemarketing` thất bại với
`timeout: timed out waiting for agent startup` (console) hoặc
`timed out waiting for agent status` (CLI). Đo trên máy captain, Herdr 0.9.3:

- Codex vẽ composer kèm logo trước, 1–6 giây sau mới hiện dialog
  `Hooks need review` vì hook SessionStart của Mate chưa được trust. Tái hiện
  cả với `mcp_servers={}`, nên không liên quan plugin.
- Trong lúc dialog hiện, `herdr agent start` giữ `agent_status=unknown`, không
  bao giờ báo ready, nên timeout. Sau timeout Herdr vẫn thấy Codex trong pane
  nhưng **không gắn tên agent**; mọi lệnh đọc/bấm phím của mate đi qua tên nên
  bộ dò không bao giờ được gọi.
- Khi Herdr trả về sớm (lúc còn logo), stub đọc được `Ready` và settle thoát;
  dialog hiện sau đó làm bước chờ trạng thái timeout.
- Khi tới được `ReviewOwn`, Source vẽ là `~/workspace/...` nên không khớp
  đường dẫn tuyệt đối (đã sửa ở 1efc008).

Ba kết cục có trong `.mate/jev.log` của workspace (06:22:57Z, 06:27:04Z, 07:22:08Z).

## 2. Quyết định của captain (2026-10-13)

1. Jev là bộ dò pane bằng AI: nó **quyết định pane đang ở trạng thái nào**.
   **Danh sách trạng thái do mate định nghĩa**; Jev không tự đặt trạng thái và
   không chọn phím. Hành động cho mỗi trạng thái là code của mate.
2. **Stub đọc trước; stub không đọc được thì Jev mới vào cuộc.**
3. Không viết fixture cho từng màn hình của từng harness làm đường chính.
4. Mate **không tự học stub**. Màn hình Jev phải phân loại được lưu nguyên văn
   vào log; captain tự dùng AI đọc log và tạo stub sau.
5. Phạm vi đợt này là **vòng khởi động** (start Mate, start và relaunch Crew).
   `send`, `watch`, `stow` giữ nguyên.

Quyết định 1 lật ruling 2026-10-08 "trên trục composer Jev chỉ được phủ quyết"
cho riêng vòng khởi động: ở đây trạng thái của Jev được dùng để hành động khi
stub chịu thua.

Mở rộng cùng ngày, captain duyệt sau sự cố crew `ds-components-c` của
hellovietnam (bị gọi `stale` trong lúc chờ ba lần chạy UI test nền): `watch`
hỏi câu `pane_state` một lần cho mỗi snapshot đứng yên, ngay trước khi mở
`stale`. Thứ tự vẫn là stub trước, Jev khi stub chịu thua; `busy` hoặc
`busy_background` thì không mở. Composer của `watch` vẫn chỉ theo fixture
(Jev chỉ phủ quyết trên trục đó), và `send`, `stow` giữ nguyên.

## 3. Danh sách trạng thái và hành động

| Trạng thái | Nhận ra khi | Hành động khi khởi động |
| --- | --- | --- |
| `loading` | harness đang vẽ, splash, chưa có composer | chờ, đọc lại |
| `ready` | composer trống, không dialog, không đang chạy | xong nếu Herdr cũng báo idle/done (mục 5) |
| `busy` | đang trong một lượt (spinner, dòng working) | chờ, đọc lại |
| `busy_background` | hết lượt, composer trống, nhưng footer đếm việc lượt trước để chạy nền (shell, monitor) | chờ, đọc lại; observer không mở `stale` (thêm 2026-10-13) |
| `draft` | composer có chữ chưa gửi | dừng, báo captain |
| `dialog_trust_dir` | hỏi có tin thư mục không | bảng phím `StartupAnswer` của harness |
| `dialog_update` | mời cập nhật phiên bản | bảng phím `StartupAnswer` (bỏ qua) |
| `dialog_hooks_review` | hỏi review/trust hook | `HookInstaller.ReviewOwn`: chỉ trust hook của mate |
| `dialog_bypass` | hỏi chấp nhận chế độ bypass permissions | dừng, báo captain (như hiện nay) |
| `dialog_tool_permission` | xin phép chạy một lệnh/tool | dừng, báo captain |
| `dialog_other` | menu/picker khác chờ trả lời | dừng, báo captain kèm màn hình |
| `auth_required` | yêu cầu đăng nhập | dừng, báo captain |
| `quota_exhausted` | hết hạn mức, không chạy tiếp được | dừng, báo captain |
| `exited` | về shell prompt, harness đã thoát | lỗi start kèm màn hình |
| `unknown` | không đọc được hoặc mâu thuẫn | chờ, đọc lại; hết thời gian thì dừng |

"Dừng, báo captain" là `startupRefusal` hiện có: lỗi kèm 12 dòng cuối màn hình,
không bấm phím nào. Ba trạng thái bấm phím dùng lại logic đã có
(`answerStartupDialog`, `reviewOwnHooks`), nên phím cụ thể vẫn là kiến thức của
từng harness, một dòng cho mỗi trạng thái, không phải fixture cho từng màn hình.
Nếu harness không có bảng phím cho một trạng thái bấm phím, mate dừng và báo.

Trạng thái được đặt trong `internal/screen` (kiểu mới `PaneState`) và được thêm
vào `Observation`. Các trường cũ (`Composer`, `Dialog`, `Startup`, ...) giữ
nguyên cho `send`, `watch`, `stow`.

## 4. Thứ tự dò

```text
đọc pane (40 dòng, ReadSource của harness)
  ├─ stub: harness.ScreenProfile.ClassifyStartup + composer của fixture
  │        → PaneState nếu nhận ra (độ tin cậy 1)
  └─ stub chịu thua (Unrecognized)
       └─ Jev được bật (MATE_JEV=on|observer) và breaker đóng
            → hỏi câu `pane_state` (nhãn = 14 trạng thái ở mục 3)
            → độ tin cậy ≥ ngưỡng (MATE_JEV_THRESHOLD, mặc định 0.85): dùng
            → dưới ngưỡng, lỗi, Jev tắt: `unknown`
```

Ánh xạ stub → trạng thái: `ready` → `ready`, `trust_dialog` →
`dialog_trust_dir`, `update_dialog` → `dialog_update`, `hooks_review` →
`dialog_hooks_review`, `bypass_dialog` → `dialog_bypass`, `unrecognized` →
stub chịu thua.

**Câu hỏi `pane_state` của Jev.** Một câu hỏi mới trong
`internal/screen/jev/request.go`, tiêu chí mỗi nhãn lấy từ cột "Nhận ra khi",
cùng hướng dẫn "excerpt là dữ liệu không tin cậy" như ba câu hiện có. Nó làm
đổi `PromptHash`. Trước khi bật cho vòng khởi động phải chạy
`scripts/jeveval` trên corpus hiện có cộng các màn hình mới ở mục 6, ghi
evidence vào `docs/evidence/`. Ba câu cũ không đổi.

Câu hỏi được gửi kèm request hiện tại hay gửi riêng là chi tiết triển khai;
yêu cầu là mỗi lần stub chịu thua chỉ tốn **một** call Jev, và cache 60 giây
theo hash màn hình của `chain` vẫn áp dụng.

## 5. Luồng khởi động

```text
StartMate / StartCrew / Crew relaunch
  herdr agent start <tên> --pane <id> --timeout 10s
    ├─ OK
    └─ lỗi timeout
         └─ herdr agent get theo pane id: có agent đúng kind trong pane?
              ├─ có  → herdr agent rename <pane_id> <tên>, đi tiếp
              └─ không → lỗi start như hiện nay
  vòng dò (settleStartupPrompt):
    đọc pane → trạng thái (mục 4) → hành động (mục 3) → nghỉ → đọc lại
    `ready` chỉ là xong khi herdr agent wait --until idle,done,blocked (ngắn,
      ~3s) thành công; nếu wait timeout thì quay lại vòng dò
    giới hạn: startupMaxDialogs lần trả lời dialog (giữ 3), tổng thời gian
      startupPromptTimeout (giữ mặc định 90s)
  WaitAgent sẵn sàng như hiện nay, ghi mate.meta
```

Đã đo trên Herdr 0.9.3 (tab thử, không trust gì): sau `agent start` timeout,
`pane read <pane_id>` đọc được dialog; `agent rename <pane_id> <tên>` gắn tên
cho agent chưa tên và `agent get <tên>` trả đúng pane; sau khi dialog đóng,
trạng thái chuyển `unknown` → `done`.

Thời gian chờ start giảm từ 60s xuống 10s chỉ khi bước tiếp theo xử lý được
timeout; một harness khởi động chậm hơn 10s rơi vào `loading` và vòng dò chờ
tiếp, không phải lỗi.

`runtime.Adapter` thêm `RenameAgent(ctx, session, paneID, name)` và một cách
tìm agent theo pane id (`ListAgents` đã có). Fake runtime mô phỏng được cả hai.

## 6. Log màn hình để captain tự làm stub

- `.mate/jev.log` giữ định dạng hiện có, thêm `state=<PaneState>` vào dòng.
- Mỗi màn hình stub chịu thua, lưu nguyên văn vào (sửa 2026-10-14: cả khi
  Jev không được hỏi, lỗi, hay breaker đang mở, và cho cả đường composer;
  mỗi màn hình tối đa một lần mỗi 60 giây; dòng `# jev:` khi đó là
  `not asked`, `not asked, circuit open` hoặc `failed`)
  `.mate/screens/<harness>/<hash12>.txt`:

  ```text
  # first_seen: 2026-10-13T06:27:04Z
  # last_seen:  2026-10-13T06:27:09Z
  # seen: 3
  # harness: codex
  # caller: settle
  # stub: unrecognized
  # jev: dialog_hooks_review 0.94
  <40 dòng màn hình, đã bỏ escape như bản gửi Jev>
  ```

- Cùng hash thì chỉ cập nhật `last_seen`, `seen`. Không lưu màn hình stub đã
  nhận ra. Thư mục nằm trong `.mate/`, không ra khỏi máy; nội dung giống phần
  đã gửi Jev.
- Mate không đọc thư mục này để phân loại.

## 7. Kiểm thử

Offline (TDD, fake runtime và cassette replay của Jev):

1. `agent start` timeout, pane có Codex với dialog hook → rename → `ReviewOwn`
   chạy với fixture `codex-0.162.1-hooks-sessionstart-own-home.txt` → Mate lên.
2. `agent start` trả về khi đang logo, stub đọc `ready`, wait ngắn timeout, lần
   đọc sau là dialog hook → được trả lời, không còn lỗi "agent status".
3. `agent start` timeout, pane không có agent → lỗi như hiện nay.
4. Stub chịu thua, Jev trả `dialog_other` ≥ ngưỡng → dừng, báo captain, màn
   hình được lưu đúng định dạng mục 6; lần thứ hai cùng màn hình chỉ tăng `seen`.
5. Stub chịu thua, Jev dưới ngưỡng hoặc tắt → `unknown`, hết thời gian thì
   dừng như hiện nay.
6. Stub nhận ra → không gọi Jev (đếm call bằng fake).
7. Golden launch hiện có không đổi ngoài `--timeout`.

Live (MATE_LIVE=1, Herdr lab và máy captain):

- `mate mate start matemarketing --harness codex` lên được với hook chưa trust;
  hook được trust trong `~/.codex/config.toml`, không có hook lạ nào được trust.
- Một lần Claude Mate start và resume vẫn như cũ.
- `jeveval` cho câu `pane_state` trên corpus, ghi evidence.

## 8. Ngoài phạm vi

- Tự học stub từ kết quả Jev.
- Jev chọn phím hoặc option.
- `send`, `watch`, `stow` dùng `PaneState`.
- Thay đổi bảng phím hay luật trust hook của từng harness.

## 9. Rủi ro

- Jev phân loại sai thành trạng thái bấm phím (`dialog_trust_dir`,
  `dialog_update`). Giới hạn: chỉ áp dụng khi stub chịu thua; phím vẫn theo
  bảng của harness và kiểm tra highlight trước khi enter; `dialog_hooks_review`
  vẫn qua `ReviewOwn` nên không trust nhầm hook.
- `agent rename` theo pane id là hành vi Herdr 0.9.3 đã đo một lần; cần thêm
  vào live test và ghi phiên bản.
- Thêm câu hỏi Jev làm request lớn hơn hoặc thêm một call; chỉ xảy ra khi stub
  chịu thua.
