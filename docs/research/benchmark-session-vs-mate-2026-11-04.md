# Benchmark: một session Claude Code so với Mate

Issue: CON-159. Ngày đăng ký phương pháp: 2026-11-04. Đăng ký trước khi chạy bất kỳ phương án nào và trước khi viết spec dự án thử. Đây là protocol; log thực thi phải ghi số run đã chạy và mọi sai khác so với protocol. Không suy kết quả từ tài liệu này. Mục `## Pilot` ở cuối để trống cho tới khi có số thật.

Bối cảnh: comment trên video TikTok ngày 03/11 nói "Claude Code gọi sub agent lên làm y như video này". Câu trả lời của captain: một session Claude Code làm được cả một dự án nhỏ, Mate không đấu ở đó; Mate không mặc nhiên rẻ token hơn, nên phải đo. Tập video sau sẽ công bố số của benchmark này, vì vậy cách đo phải công bằng và kiểm lại được.

## 1. Câu hỏi

Với cùng một dự án nhỏ có spec chốt, cùng model, cùng tool, cùng ngân sách:

1. Phương án nào làm xong dự án đạt chuẩn với ít token hơn, nhanh hơn, ít lần người phải nhúng tay hơn?
2. Phương án nào phục hồi sau khi bị ngắt giữa chừng mà không phải giao lại từ đầu?
3. Thước chính theo phương châm của captain (2026-10-16): **số tiêu chí nghiệm thu đạt trên một triệu token**, và một dự án chỉ tính là xong khi đạt mọi tiêu chí.

Không hỏi: "Claude Code có làm được không". Giả định đăng ký trước là A làm được dự án nhỏ, và lợi thế của B (nếu có) nằm ở token, can thiệp và phục hồi, không nằm ở khả năng hoàn thành.

## 2. Hai phương án

### A: một session Claude Code

- Một session `claude` tương tác trong một pane, cwd là repo của dự án thử, quyền `--permission-mode bypassPermissions` để không có prompt xin quyền (B cũng chạy không prompt).
- Được dùng subagent và agent teams (`CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1`), worktree, background agent, hook, skill. Không có Mate, không có reviewer ngoài.
- Prompt mở đầu cố định (ghi nguyên văn trong log): nội dung `spec.md`, kèm câu "Làm cho tới khi mọi tiêu chí nghiệm thu đạt. Tự quyết định cách chia việc. Chỉ hỏi khi spec không trả lời được."
- Khi session dừng mà dự án chưa xong, người chạy gõ đúng một câu "tiếp tục". Mỗi lần như vậy là một can thiệp loại N (mục 5.5).

### B: Mate điều phối

- Một workspace mate riêng cho benchmark, ví dụ `~/bench`, tạo bằng `mate init`, để timeline và `mate usage` không lẫn với dự án thật trong `~/workspace`. `WORKSPACE.md` của workspace này không bắt dùng Linear; task tracker là `backlog.md` của Mate (docs/task-management.md), trừ khi captain muốn thử đúng cấu hình Linear.
- `mate project add bench <repo>`; `project.yaml`: `review: "on"`, `next_work: pick`, headless.
- Mate chạy trên Claude Code (`mate_harness: claude`). Crew và reviewer ghim về cùng model và effort với A bằng `.mate/crew-dispatch.json` (mọi rule đều `--harness claude --model <M> --effort <E>`), để B không được lợi hay thiệt vì chọn model khác.
- Prompt mở đầu: cùng `spec.md`, gửi cho Mate như target (CON-110: ghi vào `## Captain` của PROJECT.md): "Target: làm xong spec này. Chỉ dừng khi bị chặn."
- Mate hỏi gì qua `mate news`, người chạy trả lời. Mỗi câu trả lời là một can thiệp (mục 5.5), tính như với A.

### B2 (tuỳ chọn, báo cáo riêng): Mate như captain vẫn dùng

Cùng như B nhưng để dispatch mặc định (Codex, pi, Sonnet theo độ khó). Không so trực tiếp với A vì khác model; chỉ để trả lời "cấu hình thật của captain tốn bao nhiêu". Chạy sau khi A và B đã đủ số run.

## 3. Điều kiện công bằng

| Điều kiện | Cách giữ |
| --- | --- |
| Cùng spec | Một file `spec.md` đóng băng, ghi SHA-256 vào log trước run đầu. Không sửa giữa chừng. Sửa là bỏ toàn bộ run cũ. |
| Cùng tiêu chí xong | Bộ acceptance test hộp đen viết trước spec freeze, không đưa cho agent (mục 4). Cả hai arm chỉ thấy danh sách tiêu chí bằng chữ trong `spec.md`. |
| Cùng model, effort | Ghi model id và effort của A; B ghim qua `crew-dispatch.json`. Mate (điều phối) chạy cùng model. |
| Cùng tool | Cùng Claude Code version, cùng máy, cùng MCP (tốt nhất: không MCP nào ngoài mặc định), cùng `~/.claude` config dir hoặc hai config dir sao chép y nhau. |
| Cùng harness repo | Repo khởi đầu là một commit giống nhau: `AGENTS.md` ngắn (stack, lệnh test, quy ước), không có skill riêng cho arm nào. |
| Cùng ngân sách | Trần mỗi run: **150 triệu token** (tổng mọi agent) và **8 giờ** wall-clock. Vượt là run thất bại, vẫn ghi số. Captain có thể đổi trần trước run đầu, ghi vào log. |
| Cùng người chạy | Một người, trả lời câu hỏi của agent theo `faq.md` viết sẵn cùng spec; câu không có trong FAQ thì trả lời ngắn nhất có thể và chép nguyên văn vào log, dùng lại cho arm kia. |
| Thứ tự | Xen kẽ A, B, A, B… để lệch quota và giờ trong ngày chia đều. |

## 4. Dự án thử

Yêu cầu: spec viết được thành 12–20 tiêu chí nghiệm thu hộp đen, nhiều module phụ thuộc nhau (schema → API → UI → import/export), đủ lớn để không chạy xong trong một lượt, đủ nhỏ để lặp 3 lần mỗi arm trong ngân sách. Không dùng dự án đang chạy thật (mihuv2, plantrip, hellovietnam) khi chưa được captain đồng ý. Không iOS ở vòng đầu để tránh lane xcode và simulator.

Ba ứng viên, chọn một trước khi viết spec:

| | Ứng viên | Stack | Vì sao | Rủi ro |
| --- | --- | --- | --- | --- |
| 1 | **Sổ trực ca** (duty roster): đội, thành viên, ca trực, phân công, lịch tuần, xuất CSV, đăng nhập bằng link token, REST API + OpenAPI, test | Go + SQLite + HTML server-render, một binary | Rẻ nhất để lặp 3×2 run; một binary nên acceptance test hộp đen dễ; captain đọc được code Go; stack trùng repo mate | Domain quen với model, A có thể làm nhanh |
| 2 | **Mini tracker**: project, issue, trạng thái, nhãn, comment, Kanban, tìm kiếm, nhập JSON, REST API, test | Go + SQLite + htmx | Nhiều module phụ thuộc nhất, giống cách Mate tự chia việc | Model thuộc lòng domain này, dễ "nhớ" thay vì làm |
| 3 | **Đặt lịch salon**: dịch vụ, thợ, khung giờ, đặt lịch, admin Filament, nhắc lịch giả lập, API cho app | Laravel + Postgres + Sail, như plantrip-api | Domain thật của captain (ehairvietnam), tiêu chí dễ phán; harness kit Laravel đã có | Docker và Sail tốn thời gian dựng ở cả hai arm; mỗi run dài, khó lặp 3 lần |

Đề xuất: **ứng viên 1 cho vòng pilot**, vì lặp được rẻ; ứng viên 3 cho vòng "thật" sau khi protocol đã chạy trơn.

Khi captain chọn xong: viết `spec.md`, `faq.md`, bộ acceptance test, commit vào repo riêng `bench-spec` (private), ghi SHA-256 của ba file vào log. Agent chỉ nhận `spec.md` và repo khởi đầu.

## 5. Thước đo

Mỗi run ghi đủ bảy số. Số nào chưa đo ghi `chưa đo`, không ước lượng.

### 5.1 Wall-clock

Từ lúc gửi prompt mở đầu đến lúc arm tự khai xong lần cuối và acceptance chạy đạt. Thời gian người chạy đi vắng vẫn tính (đó là điểm đo). Ghi riêng thời gian chờ người trả lời câu hỏi.

### 5.2 Tổng token

Cùng một cách đếm cho cả hai arm, là cách `mate usage` đếm (docs/dashboard.md): một lần gọi model là một `message.id`; `TOTAL = input + cache_read + cache_write + output`; `thinking` ghi riêng, không cộng vào TOTAL. Cộng mọi agent: session chính, subagent, teammate, Mate, crew, reviewer, scout.

- A: parse transcript Claude Code của run: `~/.claude/projects/<slug>/<session>.jsonl`, `<session>/subagents/agent-*.jsonl`, và mọi session teammate mở trong khung giờ run (agent teams là session riêng). Dùng script `scripts/claudeusage` (viết trước pilot, dùng lại parser `internal/harness/claude`, có test) để hai arm chung một parser. Đối chiếu với `/usage` của session chính; lệch trên 5% thì ghi và tìm nguyên nhân trước khi dùng số.
- B: `mate reindex --workspace ~/bench` rồi `mate usage bench`; lấy dòng tổng kể cả Mate và `tokens_with_reviews` của từng task. Không bỏ token của Mate: chi phí điều phối là thứ cần đo.
- Ghi thêm số lần gọi model của mỗi arm.

### 5.3 Tỉ lệ nghiệm thu đạt

Chạy bộ acceptance test hộp đen trên commit cuối của arm, trên máy sạch (clone mới, build theo README của arm). Ghi `đạt/tổng` và danh sách tiêu chí trượt. Tiêu chí UI không tự động được thì captain chấm theo checklist, chấm mù: đổi tên thư mục A/B thành X/Y trước khi chấm.

### 5.4 Rework

Ba số: (a) số lần arm tự khai "xong" trước khi acceptance đạt; (b) số dòng thay đổi (`git diff --shortstat`) từ lần khai xong đầu tiên tới commit cuối; (c) với B thêm số lượt review `changes-requested` và `incomplete` từ `mate usage`/timeline. Với A, (c) là số lần người chạy phải gõ "chưa đạt, tiêu chí X" sau khi acceptance trượt (cũng tính là can thiệp loại C).

### 5.5 Can thiệp của người

Mọi lần người gõ vào sau prompt mở đầu, phân loại:

| Loại | Là gì | Ghi chú |
| --- | --- | --- |
| Q | Trả lời câu hỏi sản phẩm mà spec không trả lời được | Chép câu hỏi và câu trả lời; dùng lại cho arm kia nếu gặp lại |
| N | Nudge: "tiếp tục" khi arm dừng mà chưa xong | Chỉ có ở A theo thiết kế; nếu B cần nudge thì ghi và giải thích |
| C | Sửa hướng: báo tiêu chí trượt, chỉ lỗi | Sau khi acceptance trượt |
| M | Người tự sửa code, config, môi trường | Run có M là run hỏng, vẫn ghi số, không tính vào kết luận |

Prompt xin quyền tool phải bằng không ở cả hai arm; nếu có, ghi riêng, không tính vào bảng.

### 5.6 Phục hồi sau gián đoạn

Chạy riêng, một run mỗi arm, sau khi ba run chính đã xong. Mốc ngắt cố định: khi acceptance đạt từ 40% tới 60% (người chạy chạy acceptance ngầm mỗi 30 phút để biết). Cách ngắt: `kill -9` toàn bộ tiến trình `claude` (A: session chính, subagent, teammate; B: Mate và mọi crew), kể cả pane. Chờ 10 phút. Nối lại bằng đúng một câu "tiếp tục": A `claude --resume <session>`; B `mate mate start bench`. Đo: có xong không, token và thời gian cộng thêm so với trung vị run chính, số can thiệp thêm, việc bị mất (diff giữa commit cuối trước ngắt và trạng thái sau khi nối lại 30 phút).

### 5.7 Thước chính

`tiêu chí đạt / (TOTAL token / 1 000 000)`, tính trên từng run, báo trung vị và khoảng. Run không đạt mọi tiêu chí vẫn tính thước này nhưng đánh dấu "chưa xong".

## 6. Quy trình một run

1. Clone repo khởi đầu vào thư mục mới; ghi commit, Claude Code version (`claude --version`), mate commit (`mate --version`), model id, effort, ngày giờ, mã run (`A1`, `B1`…).
2. Chụp `spec.md` SHA-256 và so với log; lệch thì dừng.
3. Gửi prompt mở đầu; bấm giờ.
4. Người chạy chỉ: trả lời Q theo `faq.md`, nudge N, báo C sau acceptance trượt. Mọi lần gõ ghi vào `runs/<mã>.md` kèm giờ.
5. Khi arm khai xong: chạy acceptance. Trượt thì ghi C, tiếp tục. Đạt hết hoặc chạm trần thì dừng; bấm giờ.
6. Thu token (5.2), diff (5.4), bảng can thiệp (5.5).
7. Dọn: teardown worktree, docker, tiến trình (`mate gc` cho B).

## 7. Lặp và phân tích

- Ít nhất **3 run** mỗi arm cho dự án thử; thêm 1 run phục hồi mỗi arm. Tổng tối thiểu 8 run. B2 thêm 3 run nếu còn ngân sách.
- Báo trung vị và khoảng (min–max) cho từng thước. Không tính p-value với n=3; nói thẳng "ba lần" khi công bố.
- Kết luận viết theo mẫu: "A thắng ở …, B thắng ở …, chưa phân định ở …". Thua ở đâu nói ở đó, kể cả trong video.
- Mọi file log, transcript, diff lưu trong `bench-spec/runs/` để kiểm lại được.

## 8. Những gì benchmark này không nói

- Không nói về dự án kéo dài nhiều tuần với yêu cầu đổi giữa chừng; đó là tình huống B được thiết kế cho, nhưng vòng đầu không đo được. Nếu kết quả vòng đầu cho A thắng về token, đó là kết quả về dự án nhỏ spec chốt, đúng như giả định đăng ký.
- Không nói về quota subscription: mọi số là token, không quy ra tiền (phương châm 2026-10-16).
- n=3 chỉ đủ để thấy lệch lớn. Lệch dưới 20% coi là chưa phân định.
- Người chạy là captain, không mù với arm khi chạy; chỉ chấm UI là mù.

## 9. Việc phải xong trước run đầu

- [ ] Captain chọn ứng viên (mục 4) và chốt trần ngân sách (mục 3).
- [ ] `spec.md`, `faq.md`, acceptance test viết xong, commit `bench-spec`, ghi SHA-256.
- [ ] Repo khởi đầu với `AGENTS.md` ngắn, commit, ghi SHA.
- [ ] `scripts/claudeusage` chạy được trên một session có subagent, số khớp `/usage` trong 5%.
- [ ] Workspace `~/bench` dựng xong, `crew-dispatch.json` ghim model, `mate usage bench` in được bảng rỗng.
- [ ] Mẫu `runs/<mã>.md` có sẵn các mục 5.1–5.6.

## Pilot

Chưa chạy. Điền sau khi có số thật, mỗi run một bảng, kèm ngày giờ, commit và mọi sai khác so với protocol.
