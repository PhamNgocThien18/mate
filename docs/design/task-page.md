# Trang task của admin dashboard: Hành trình và Dấu vết

Bản thiết kế cho `#/p/<project>/t/<crew>` (tầng task của admin `#/`, mục 10 của [dashboard.md](../dashboard.md)).
Mockup tĩnh của tab Hành trình, vẽ bằng dữ liệu thật của `mihuv2/hu-246-editor-block-knob` (31/10): [task-page/hanh-trinh.html](task-page/hanh-trinh.html).
Office và Agent City không đổi; bản này chỉ nói về admin.

## Vì sao làm lại

Trang hiện nay (đo trên hu-246): 8.500px, 11 khối, 3.2M token kể sáu lần, bốn bảng cùng chia theo loại việc, 29 segment liệt kê hết, mỗi khối một đoạn giải thích cách đo.
Mỗi khối trả lời một câu của projector, không phải câu captain hỏi khi mở task.
Bốn thứ captain cần nhất lại không có: nội dung brief, bản hand-back, kết luận review, link PR (PR chỉ nằm trong status line).

## Hai tab, một header

Trên trang, tab và chữ dùng tiếng Anh như phần còn lại của admin: **Journey** là Hành trình, **Trace** là Dấu vết; tài liệu này giữ tên tiếng Việt.

Một task có hai cách đọc, cùng một dòng thời gian ở hai độ cao:

| Tab | Câu hỏi | Dùng khi |
| --- | --- | --- |
| **Hành trình** (mặc định) | Mate giao gì → crew đã làm gì qua từng lượt → kết quả ở đâu | đánh giá task có đi đúng quy trình không |
| **Dấu vết** | từng bước của agent tốn gì, lặp gì, sai gì | gỡ lỗi vì sao một lượt tốn thế |

Header dùng chung, đứng trên hai tab, là nơi route `/usage` cuộn tới:
trạng thái cuối (merged / changes requested / discarded / đang chạy / didn't start, từ vựng của CON-55), token crew, token Mate quanh task, model call, thời gian spawn→close, thời gian crew chờ máy, context đỉnh, câu hỏi / lần bị trả lại.
Mỗi số ở header xuất hiện đúng một lần trên trang; các tab chỉ chia nhỏ nó.

Bấm một chương ở Hành trình mở Dấu vết đúng khoảng stage của chương đó; Dấu vết giữ nguyên trục thời gian theo prompt để hai tab khớp mốc.

## Tab Hành trình

Trả lời "ai giao gì cho ai, lúc nào, tốn bao nhiêu". Ba khối, task bình thường vừa một màn hình:

1. **Swimlane**: sáu làn Captain · Mate · Observer · Crew · Máy (build, test, gate) · GitHub trên một trục thời gian từ lượt Mate spawn crew đến lượt Mate đóng crew. Thanh là đang làm, chấm là sự kiện, mũi tên đánh số là bàn giao giữa hai làn, nét đứt là chờ hoặc im lặng. Bấm một thanh ở làn Crew mở Dấu vết đúng khoảng stage.
2. **Từng bước**: bảng, mỗi dòng một mũi tên: lúc, từ → đến, chuyện gì (một câu, dòng phụ là chi tiết), giá (thời gian · token · call của bên làm bước đó).
3. **Quy trình repo**: bảng các bước mà harness của project dặn crew đi (đọc entry point, `make agent-start`, gate, chụp ảnh, verify UI, learn, hand-over…), mỗi dòng tự tick từ lệnh crew đã chạy, file đã đọc, skill đã nạp; cột bằng chứng là lệnh và mốc giờ. Trả lời "crew có đi theo quy trình repo không, bỏ bước nào".
4. **Đọc nhanh**: ba thẻ kết luận: task có đi đúng vòng không và chỗ cần soi (kể cả số bước quy trình bị bỏ); Mate tốn bao nhiêu so với crew; crew chờ máy bao lâu.

Những gì mỗi làn vẽ, và lấy ở đâu:

| Làn | Nội dung | Nguồn |
| --- | --- | --- |
| Captain | lời captain trong brief, news/câu hỏi và trả lời, lúc nhận báo cáo của Mate | brief `## Captain's words` (**API chưa trả**), `questions`, `exchanges` của Mate có `source: captain` |
| Mate | các lượt Mate dính tới crew này: lượt spawn (activity `crew.spawned`), lượt nhận digest về crew, lượt merge/đóng; nhãn là việc Mate làm, giá là token của lượt | `/api/projects/{p}/mate` `exchanges` lọc theo crew (**hiện không có trường crew trên exchange, phải khớp theo activity và nội dung digest**) |
| Observer | incident mở/đóng về crew này (no_turn, stale, runtime_lost…) | `incidents.log` (**API chưa trả**) |
| Crew | prompt turn, các đoạn làm việc gộp từ stage (đọc code, sửa, chờ máy, trả bài), status line, lệnh fail, idle giữa hai lượt | `prompt_turns`, `overview.steps`, `status_lines`, `executions` |
| Máy | execution dài hơn 5 giây (build, test, gate, chụp), gate chạy nền từ lúc crew đẩy đến lúc task-notification | `executions`, prompt 2 `prompt` |
| GitHub | PR mở, review verdict, merged, cách merge | status line `pr-open`, `.meta` (`pr_url`, `pr_state`, `pr_sync`, `merge_commit`) (**API chưa trả**), `reviews` |

Chương "Mate giao" (brief, tài liệu nạp, model/effort/lane) và "Trả bài" (handback: tiêu chí pass/fail, lệch, còn mở) là hai dòng trong bảng Từng bước, nở ra khi bấm.

### Quy trình repo: danh sách bước lấy ở đâu

Mỗi project kể quy trình của mình một kiểu (mihuv2: AGENTS.md + `docs/harness/mate.md`, tám entry point `/name` và `$name` là shim về `docs/harness/workflows/`), nên mate không đoán từ tài liệu mà để project khai trong `project.yaml`:

```yaml
workflow:
  - step: Đọc docs/harness/mate.md
    read: docs/harness/mate.md
  - step: make agent-start
    command: ^make agent-start
  - step: Gate pre-merge trên commit
    command: ^make pre-merge
  - step: Chụp ảnh bằng make shot
    command: ^make shot
  - step: ios-ui-verify sau khi đổi UI
    skill: ios-ui-verify
    files: '\.swift$'   # chỉ áp dụng khi diff chạm file Swift
  - step: learn.md trước khi trả bài
    read: docs/harness/workflows/learn.md
  - step: Handback có dòng Learning
    handback: ^Learning:
```

Một bước là xong khi có một bằng chứng khớp: `command` khớp lệnh Bash trong `executions`, `read` khớp path của Read/cat/head, `skill` khớp tên trong `skills`, `handback` khớp một dòng trong handback.md. Mọi mẫu là regex Go. `files` (thay cho `when: ui` ở bản nháp) là regex trên danh sách file crew đã đổi: không khớp file nào → "n/a"; không đọc được danh sách (nhánh đã xoá, `.meta` không có merge_commit) → "unknown". Project không khai `workflow` thì khối ẩn.
Kết quả tính ở server (`journey.workflow[]`), vì UI không đọc project.yaml. Đợt đầu mate chép sẵn danh sách này cho mihuv2 khi onboard; sau này kit harness tự sinh.

Trên hu-246 (31/10) khối này cho ra: crew đi 4/9 bước (đọc mate.md, gate hai lần, handback đủ tiêu chí và dòng Learning, `gh pr create`), bỏ 5 (agent-start, gate-plan, `make shot` thay bằng xcodebuild tự chế, ios-ui-verify dù đổi UI, learn.md), nạp 0 skill. Crew theo brief và AGENTS.md, phần nào brief chép thì làm, phần nào brief chỉ trỏ tới file thì bỏ.

### API cần thêm cho tab này

Một trường mới `journey` trong `GET /api/projects/{p}/tasks/{crew}` (hoặc endpoint riêng, đọc một lần, không poll):

- `brief`: `captain_words`, `task_section` (từ `# Task` đến hết `## Open decisions`), `bytes`; phần vai trò và rules không trả.
- `handback`: nguyên văn markdown + `closes`, `criteria[]{name, pass, command}`, `still_open[]`.
- `meta`: `pr_url`, `pr_state`, `pr_sync`, `merge_commit`, `lane`, `size`, `teardown`, `cleanup`, `launched_at`.
- `reviews[]`: như tầng project, kể cả rỗng.
- `mate_turns[]`: các lượt Mate dính tới crew này (spawn, digest, merge, đóng) với `at`, `what`, `tokens`, `model_calls`, `duration_ms`; tách phần của crew này khỏi việc khác trong cùng lượt là việc sau, đợt đầu ghi cả lượt và nói rõ.
- `incidents[]`: `at`, `kind`, `state`, `text` từ `incidents.log`.
- `workflow[]`: `step`, `state` (done | missed | n/a), `evidence{at, command|path|skill}`, tính từ `project.yaml` `workflow` đối chiếu với `executions`, `skills`, handback.

Không đổi projector, không đổi `performance`.

## Tab Dấu vết

Trang usage hiện nay rút lại. Giữ một bảng theo loại việc, một timeline, findings; phần còn lại là drill-down.

1. **Timeline stage** là xương sống: trục thời gian theo prompt, mỗi stage một khối rộng theo token, màu theo loại việc, pin skill, dấu lệnh fail. Thay cho cả ba khối Stages, Work over time, Work segments.
2. **Findings** ngay dưới timeline, tối đa 3 mở sẵn, còn lại gập. Repeats và Most expensive step thành hai finding, không còn là hai thẻ.
3. **Bảng theo loại việc** một cái, ẩn hàng 0, hàng Mixed gộp một dòng kèm cột "gồm những gì". Hàng là link lọc timeline.
4. **Drill-down**: bấm stage mở segment của nó và evidence (execution, call) bên dưới timeline; không còn danh sách 29 segment.
5. **Chi tiết** (gập): Tool mix by prompt, recording details, execution không gắn prompt, raw model calls.

Chữ giải thích cách đo vào tooltip ⓘ ở tiêu đề khối. Task đã đóng không hiện Observer/Ingest/Usage age.

## Quy tắc chung

- Mỗi con số kể một lần. Số ở tab phải cộng lên được số ở header.
- Trang đầu của mỗi tab dưới hai màn hình với task bình thường.
- Unknown là `?`, zero thật là `–`, như thẻ Stages hiện nay.
- Từ vựng số theo `humanize.js`; trạng thái theo CON-55.
- "Mixed activity" chiếm 51% ở hu-246 là chuyện của projector (CON-75), không sửa trong đợt này.

## Thứ tự làm

| PR | Việc | Tách file |
| --- | --- | --- |
| 1 (CON-148) | header chung + tab bar; tab Hành trình với dữ liệu đã có (prompt turn, status line, question, skill, execution, PR từ status line); route `/usage` mở Dấu vết. Làn Observer chờ PR2 vì incident chưa có trong API | `ui/task.js` tách khỏi `app.js`, view-model tính một lần từ body |
| 2 (CON-148) | API `journey` (brief, handback, meta, reviews, mate_turns, incidents, workflow, changed_files, missing) và trên tab Hành trình: làn Mate (thanh = lượt), làn Observer (incident), brief và handback nở ra ở dòng spawn và dòng PR, khối Quy trình repo, thẻ Đọc nhanh đọc PR/merge từ `.meta`; khai `workflow` cho mihuv2 trong `~/workspace/.mate/projects/mihuv2/project.yaml` | `internal/dashboard/journey.go`, `store.WorkflowStep`, seam `Deps.ChangedFiles` |
| 3 | Dấu vết rút gọn: timeline xương sống, findings, một bảng loại việc, drill-down, Chi tiết gập | `ui/trace.js` |

Test: `ui_observability_test.cjs` theo tab Dấu vết, `ui_journey_test.cjs` cho tab Hành trình với fixture hình hu-246; cả hai chạy trong `go test` qua `ui_admin_test.go`, DOM giả dùng chung ở `ui_testdom.cjs`.
