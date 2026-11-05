# Phép thử nâng cấp review theo pstack

Ngày đăng ký phương pháp: 2026-10-29. Đăng ký trước khi chạy reviewer hoặc xem kết quả A/B. Đây là protocol đề xuất; log thực thi phải ghi số case/run đã thực hiện và mọi sai khác so với protocol. Không suy kết quả từ tài liệu này.

Baseline phải là `origin/main` đã fetch, hiện ghi nhận SHA `9e6a6836ac6c48ad1f551f45dd63810126a947c8`, và phải freeze trước sửa. Working tree `/Users/erics/app/mate` đang cũ hơn main và có thay đổi người dùng; không dùng nó làm A. Implementation dùng worktree riêng `/Users/erics/app/mate-pstack-review`. Main đã có CON-106 (nested bullet/Notes) và CON-109 (định nghĩa material); không tính chúng thành lợi ích mới.

## Câu hỏi và thay đổi nhỏ cần kiểm

Nâng cấp reviewer có phát hiện lỗi hành vi vượt khỏi diff cục bộ, kiểm bằng chứng thật và tránh false pass, trong khi giữ review sạch gọn và review `--since` có phạm vi không?

Giữ một reviewer Crew, worktree riêng, target SHA cố định, full hoặc scoped review hiện có. Không arena, không nested agent, không review lại toàn hệ thống. Thay đổi nên nằm ở hợp đồng review chung cùng phép kiểm cấu trúc tối thiểu; không đưa phương pháp đặc thù repo vào core. Cơ sở hiện tại: [review.go](../../cmd/mate/review.go), [review-delivery](../../assets/mate/skills/review-delivery/SKILL.md.tmpl), [MVP](../mvp.md).

Đề xuất hành vi:

1. Đọc ý định từ brief; xác nhận base/from/head và kích thước diff trước khi đánh giá.
2. Chọn một hoặc hai giả định mà tính đúng của thay đổi dựa vào. Theo caller/state/serialization trực tiếp cần để kiểm chúng, kể cả một file không đổi có liên quan; không mở rộng thành audit repo.
3. Dùng check rẻ nhất có thể bác bỏ giả định trên code thật tại target SHA. Test đang có không đủ thì dùng probe tạm trong worktree reviewer; không sửa product code của implementer.
4. Phân biệt evidence tự quan sát, bằng chứng kế thừa có nguồn, kết luận static và điều chưa kiểm. Finding ghi trigger, tác hại, file:line, expected/actual và command/output nếu đã chạy; thiếu run phải nói rõ lý do.
5. Bug đã xác nhận dẫn tới `changes-requested`; acceptance/rủi ro trọng yếu chưa giải quyết dẫn tới `incomplete`; `pass` cần acceptance trong scope có bằng chứng và không còn blocker. “Không có finding đã repro” không bằng pass.
6. Với `--since`, đóng từng F<n>, kiểm delta và ảnh hưởng trực tiếp; chỉ lặp gate khi delta làm bằng chứng cũ mất giá trị. Một finding trước đã đóng không chứng minh delta không tạo lỗi khác.

Giữ semantics của main: `Findings` chỉ chứa material blocker; nonblocking nằm ở `Notes` và không tạo vòng sửa. Nếu thêm gate runtime, dùng section/status explicit cho unresolved material gaps, không đoán ý nghĩa prose. Các unit test cần kiểm report được chấp nhận/từ chối qua `checkReview`, không chỉ so prompt string. Case pass với Notes phải tiếp tục được chấp nhận.

## Bộ scenario khoá trước

Mỗi scenario là repo Go nhỏ nhưng có nhiều file/caller thật, git base/head, brief, handback và test chạy được. Commit message trung tính, như một task thông thường. Không để từ “bug”, “clean”, “gold”, “eval”, tên biến thể hoặc expected verdict trong những artifact reviewer thấy. Bảng dưới là gold của người chấm, không đưa vào repo candidate.

| ID chấm riêng | Task tự nhiên | Tình huống giữ kín | Kết quả kỳ vọng và chứng cứ chấm |
| --- | --- | --- | --- |
| S1 | Lưu thời điểm hết hạn cache và khôi phục sau restart | Encoder đổi từ milliseconds sang seconds, decoder vẫn đọc milliseconds. Test hiện tại chỉ kiểm object trước restart; handback ghi gate xanh. | `changes-requested`; chỉ ra hợp đồng đơn vị giữa writer/reader; probe round-trip qua file + load làm entry hết hạn sai. Chấm bằng hành vi, không keyword. |
| S2 | Cho phép retry sau timeout khi gửi job | Worker xoá dedup marker khi timeout dù side effect có thể đã hoàn tất. Test chỉ quan sát status, không đếm side effect receiver. | `changes-requested`; chỉ ra ambiguous completion; dùng receiver giả có log và timeout sau append để chứng minh retry tạo hai effect. Không đòi mạng thật. |
| S3 | Đổi tên helper parse và giữ tương thích input cũ | Diff đúng, API caller và format legacy vẫn được giữ; tồn tại wrapper/comment trông dư nhưng có vai trò compatibility nêu rõ trong brief. | `pass`, không invent blocker hay yêu cầu rewrite; targeted test xác nhận observable contract. Không bắt reviewer phải khen cấu trúc. |
| S4 | Đổi cấu hình client external và chứng minh đọc được dữ liệu | Code chưa có bug đã biết; gate live không chạy vì thiếu credential, runner trả success sau skip; handback gọi đó là pass. | `incomplete`; chỉ rõ thiếu bằng chứng live, không tuyên bố login/read thành công và không kết luận code sai chỉ vì thiếu credential. Không cấp secret thật. |
| S5 | Scoped review sửa lỗi phân trang F1 từ vòng trước | F1 về page rỗng được sửa; delta đổi điều kiện endpoint làm bỏ item cuối ở page đầy. Caller trực tiếp ở file không đổi làm lỗi có thể quan sát. | `changes-requested`; F1 closed có bằng chứng; new finding riêng về item bị mất; probe đúng target và không mở lại toàn base. |
| S6 | Scoped review sửa tài liệu command F1 | Chỉ docs thay đổi, F1 là command sai khiến acceptance đã yêu cầu không dùng được, nay đã sửa. Review cũ có receipt gate runtime hợp lệ tại SHA trước, product code/build config không đổi. | `pass`; F1 closed; kiểm docs/command mapping đủ hẹp; cite receipt cũ và không chạy lại full gate không liên quan. |

S1/S2/S5 là positive defect, S3/S6 là negative control, S4 là unknown-proof control. Oracle của mỗi lỗi gồm một command chạy vào implementation thật, expected output từ contract và actual output, xác nhận trước khi reviewer chạy. Oracle nằm ngoài candidate repo. Kiểm bản không lỗi/fixed bằng cùng command để chứng minh oracle có khả năng phân biệt.

Không chạy những oracle đó cho candidate sẵn như một failing test hiển nhiên: reviewer phải tự chọn/tạo check. Có thể để test hiện có màu xanh như repo thật, miễn không giấu code cần đọc. S4 là thiếu proof được thiết kế, không phải lỗi ngẫu nhiên của máy lab.

## Thiết kế cặp và kiểm soát

- A là renderer/prompt và runtime review của main đã pin ở trên, được snapshot với hash; không dùng prompt cũ trong dirty cwd hoặc tự viết baseline yếu hơn. B là implementation trên main sau thay đổi với hash. Cùng briefing/task/handback/code và cùng các chỉ dẫn harness ngoài phần treatment.
- Chạy tối thiểu 6 cặp, một cặp/scenario: 12 phiên fresh. Đây là pilot thăm dò, chưa đủ kết luận thống kê. Nếu ngân sách cho phép hai repetition, đăng ký trước khi chạy và thực hiện đủ 24 phiên; không chỉ lặp các case B thua rồi chọn kết quả đẹp.
- Cùng harness/version/model/effort, timeout, token budget, quyền tool và network policy. Hai bên có cùng thời gian kiểm; B không được tự tăng budget vì prompt dài hơn. Ghi cấu hình thực tế, không chỉ alias.
- Thứ tự A/B được cân bằng trước: S1 A→B, S2 B→A, S3 A→B, S4 B→A, S5 A→B, S6 B→A. Dùng checkout/temp directory riêng mỗi run; không share transcript, repro, report, cache do ứng viên tạo hay session resume.
- Candidate chỉ nhận task tự nhiên và target artifact; không thấy protocol, rubric, gold, sibling directory, label A/B hoặc kết quả run khác. Path random trung tính; xoá tên fixture tiết lộ lỗi khỏi branch/file/task. Môi trường tool phải giới hạn cwd/read scope phù hợp; lời dặn không tự tạo isolation.
- Người chấm nhận report/transcript theo ID trung tính và gold riêng; khoá scoring trước khi mở mapping A/B. Không dùng cùng một agent vừa viết prompt để chấm theo “văn phong tốt hơn”. Chấm từ output/command/artifact quan sát được.
- Không steer khi đang chạy. Hết budget/timeout được giữ là kết quả incomplete; startup/tool outage có bằng chứng là lab failure, loại theo cùng rule cho cả hai và chạy lại cả cặp. Mọi loại bỏ và retry phải được ghi.
- Probe chạy trong scratch được phép; không merge/push, không thay reviewer target commit, không sửa implementer. Ghi diff cuối để phát hiện reviewer “sửa rồi test xanh”.

## Rubric và metric đã đăng ký

Đơn vị phân tích là scenario pair. Không gộp thành một điểm “quality” rồi cho tốc độ bù false pass.

| Metric | Cách tính |
| --- | --- |
| Phát hiện bug đúng | Số S1/S2/S5 xác định đúng root cause và tác hại / 3; finding chung chung không tính. |
| False pass | Số S1/S2/S4/S5 nhận pass; mục tiêu B là 0/4. |
| False blocker | Số S3/S6 bị changes-requested vì lỗi không có trong contract/code; báo thêm incomplete không có lý do thật. |
| Phân loại unknown | S4 incomplete với đúng gate thiếu; không fabricate output và không gọi skip là evidence pass. |
| Repro chất lượng | Số positive có command thật, đúng head, expected/actual phân biệt bug; rerun được từ artifact. Tách static-correct khỏi reproduced-correct. |
| Đúng scope và ownership | Đúng base/from/head; S5/S6 có đủ F<n> closure; không nested agent, không sửa implementer, không audit ngoài phạm vi. |
| Chi phí | Elapsed end-to-end, tool calls, input/cache/output tokens theo trường harness cung cấp, số gate chạy; missing metric ghi unknown. |
| Lặp gate không cần | S6 số lần chạy full runtime gate đã có receipt và không bị delta ảnh hưởng; mục tiêu B là 0. |

Mỗi finding nhận nhãn chấm `correct-reproduced`, `correct-static`, `unsupported`, hoặc `wrong`; mỗi acceptance nhận `supported`, `inherited-with-source`, `gap`, hoặc `fabricated`. Nhãn này thuộc scorer, không yêu cầu binary quyết định semantic.

Quyết định promotion cho pilot: B không thêm false pass/false blocker, không mất bug A bắt được, giữ scope/ownership, và có ít nhất một cải thiện quan sát được về bug/provenance/unknown handling. Nếu chỉ đẹp report mà detection/evidence không đổi, kết luận “chưa đo được lợi ích hành vi”; không quảng cáo chất lượng tốt hơn. Báo riêng tỷ lệ token và thời gian theo từng pair; chưa đặt một ngưỡng ROI phổ quát từ 6 case.

## Tách thử prompt khỏi thử gate

So semantic outcome của A và B trên rubric chung, độc lập report schema. Không chấm A thua chỉ vì không có section mới của B. Gate mới được thử bằng fixture report riêng: pass hợp lệ; pass + unresolved material gap; malformed/missing status; stale head/base/brief/handback; scoped thiếu/duplicate/open F<n>; nonblocking follow-up hợp lệ.

Các test runtime phải gọi production `checkReview`/`--check`, giữ invariant fingerprint và scoped reuse. [review_test.go](../../cmd/mate/review_test.go) đã có stale/verdict/scope; mở rộng ở seam này. [Live test hiện có](../../cmd/mate/review_live_test.go) bắt false handback với `Add` làm phép trừ, nhưng một lỗi hiển nhiên không chứng minh cải thiện so baseline.

Chạy live CLI trực tiếp đo reviewer behavior trong harness, không tự chứng minh Herdr spawn, status handoff, budget stopping và merge gate. Báo rõ topology đã chạy; cần ít nhất một smoke đường `mate review` thật nếu muốn claim tích hợp end-to-end. Suite xanh có skip `TestLive*` không được gọi là bằng chứng live.

## Hạn chế diễn giải

Fixture nhỏ tránh chi phí nhưng không đại diện toàn codebase; cùng model lặp có tương quan, order/cache vẫn có thể ảnh hưởng; scorer biết gold có thể thiên kiến. Một B tốt hơn 1–2 case là tín hiệu pilot, không phải tăng X% năng lực. Báo raw numerator/denominator, mọi output và failure; không chỉ trích run đẹp.

Nguồn phương pháp: [nghiên cứu tích hợp](../archive/research/pstack-integration-2026-10-29.md), [triết lý pstack](../archive/research/pstack-design-philosophy-2026-10-29.md), upstream [interrogate](https://github.com/cursor/plugins/blob/1ba91fc25c0977741dd0e267849e6234b037ed0a/pstack/skills/interrogate/SKILL.md), [explain-the-number](https://github.com/cursor/plugins/blob/1ba91fc25c0977741dd0e267849e6234b037ed0a/pstack/skills/principle-explain-the-number/SKILL.md), [eval playbook](https://github.com/cursor/plugins/blob/1ba91fc25c0977741dd0e267849e6234b037ed0a/pstack/skills/poteto-mode/playbooks/eval.md). Các scenario, rubric và promotion rule ở đây là đề xuất cho mate, không phải số liệu upstream.
