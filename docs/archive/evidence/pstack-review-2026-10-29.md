# Review theo pstack: kết quả pilot

CON-134, ngày 2026-10-29. Bản nâng cấp giữ một reviewer Crew và ngân sách dispatch
hiện có; thêm kiểm tra giả định, đường caller bị ảnh hưởng, probe có khả năng bác bỏ
kết luận và phân loại bằng chứng. Quy tắc chi tiết: [review](../../review.md).

## Kết luận

Đáng thử trên task thật vì bằng chứng tốt hơn và ca thiếu proof được phân loại đúng.
Pilot chưa cho thấy bắt thêm lỗi: cả hai bản đều tìm đúng 3/3 lỗi được cài vào.
Bản mới dùng thêm khoảng 13% thời gian và tổng token ở sáu cặp này. Đây là tín hiệu
thăm dò từ repo nhỏ, chưa phải ước lượng chất lượng hay ROI trên dự án thực.

## Đối chứng và kết quả

A là prompt full/scoped được xuất nguyên bản từ main
`9e6a6836ac6c48ad1f551f45dd63810126a947c8` trước chỉnh sửa; main này đã có CON-106
và CON-109 về Notes/material findings. B dùng chính renderer production mới.
12 phiên Codex CLI 0.165.0, cùng `gpt-6-luna`, effort `medium`, tool/environment,
giới hạn 300 giây mỗi phiên; không đặt token cap. Thứ tự A/B cân bằng theo
[protocol đăng ký trước](../../research/pstack-review-evaluation-method-2026-10-29.md).

| Tình huống | A: hiện tại | B: nâng cấp |
| --- | --- | --- |
| S1: sai đơn vị timestamp qua restart | Bắt đúng bằng static reasoning | Bắt đúng, chạy Save→Load→Lookup và thấy expiry sai |
| S2: retry sau timeout tạo side effect trùng | Bắt đúng, repro được | Bắt đúng, repro được |
| S3: rename helper giữ compatibility | pass | pass |
| S4: live check thiếu credential và bị skip | changes-requested; nhận ra thiếu proof | incomplete; chỉ rõ proof còn thiếu |
| S5: sửa F1 nhưng làm mất item khi phân trang | Đóng F1; repro lỗi mới qua Page | Đóng F1; repro lỗi mới qua caller Collect thật |
| S6: sửa docs, code không đổi | pass; chạy command README vừa sửa, kế thừa suite cũ | pass; kiểm mapping tĩnh, kế thừa runtime proof |

| Chỉ số | A | B |
| --- | ---: | ---: |
| Lỗi đúng được phát hiện | 3/3 | 3/3 |
| Lỗi có repro thực thi đúng | 2/3 | 3/3 |
| False pass trên S1/S2/S4/S5 | 0/4 | 0/4 |
| Chặn nhầm thay đổi đúng S3/S6 | 0/2 | 0/2 |
| Đúng phân loại thiếu proof ở S4 | 0/1 | 1/1 |
| Chạy lại full runtime gate không cần ở S6 | 0 | 0 |
| Tổng thời gian sáu review | 210.556 s | 238.263 s |
| Lệnh shell đã hoàn thành | 29 | 31 |
| Input tokens, gồm phần cache | 650,829 | 737,968 |
| Cached input tokens | 561,408 | 634,496 |
| Output tokens | 11,118 | 12,632 |
| Tổng input + output tokens | 661,947 | 750,600 |

Thời gian tăng 13.2%; tổng token tăng 13.4%. Không cộng cached input thêm lần nữa
vào tổng. Đây không phải số tiền, không suy chi phí USD từ token tổng. A ở S4 không
bịa bug code hoặc claim live thành công; khác biệt là phân loại unknown, không tính
thành một lỗi code mới mà B bắt được.

## Kiểm chứng thực thi

Các oracle riêng của S1/S2/S5 fail trên target có lỗi và pass trước regression.
Reviewer không được xem oracle/gold. 12/12 run kết thúc exit 0, HEAD cuối trùng target
và tracked diff không đổi. Probe do reviewer tạo được lưu qua command/source/output,
kể cả khi reviewer xóa file tạm sau chạy. Hai báo cáo pass của B (S3/S6) đều qua
validator production, bao gồm closure F1 của S6.

`make check` và `make build` qua. Test runtime gọi `checkReview` với git fixture thật,
kiểm pass kèm defect/gap, thiếu/trùng section, verdict trong code fence, duplicate/
unknown/open prior finding, Notes không chặn, và tương thích review cũ. Những chốt
freshness SHA/base/brief/hand-back/prior-report vẫn qua suite hiện có.

81 test live được khai báo đã skip trong `make check`; không lấy suite xanh làm
bằng chứng cho chúng. Pilot chạy brief qua `codex exec` với wrapper chung, không
chạy toàn vòng Herdr spawn → status handoff → budget stop → merge. Các invocation
production vẫn giữ nguyên; chưa thay binary đang dùng hay restart Mate của người dùng.

## Độ tin cậy và giới hạn

Một agent khác chấm từ report/command/output theo ID trung tính, không được xem
mapping A/B; chấm semantic độc lập với schema. Định dạng report có thể gợi ý biến
thể nên đây không phải blind tuyệt đối. Mỗi case chỉ có một cặp, fixture nhỏ, cache
và timing có nhiễu; không diễn giải “3/3” thành độ chính xác ngoài thực tế.

Ba lượt lỗi lab được giữ riêng: Node bị chặn metadata của stdout; sandbox macOS
bị lồng; và zsh heredoc cần TMPPREFIX trong scratch. Không tính chúng vào bảng chính.
Sau khi sửa môi trường, chạy lại toàn bộ 12 phiên với prompt giữ nguyên. Sandbox
ngoài chặn đọc gold/source/candidate khác và chặn ghi ngoài lab; Codex không tạo
sandbox lồng. Shell thật và heredoc đã qua preflight. [Chi tiết loại trừ](pstack-review-2026-10-29/lab-notes.md).

Probe retry của cả hai bản chỉ log số effect, chưa có assertion để làm regression test. Reviewer không tự chạy probe trên baseline; người chấm kiểm khả năng phân biệt từ delta/source, còn oracle riêng của lab có chạy cả hai revision.

Một số probe dùng cleanup cuối command nên exit tổng là 0 dù test fail; chấm theo
output thực và báo cáo, không coi exit 0 đó là pass. Gate chỉ kiểm cấu trúc và mâu
thuẫn explicit; nó không tự chứng minh nội dung evidence là đúng.

## Artifact và chạy lại

- [Đủ manifest, prompt, report, command/output, usage, probe source và oracle](pstack-review-2026-10-29/pilot.json.gz).
- [Các lượt lab bị loại](pstack-review-2026-10-29/excluded-lab-runs.json.gz).
- [Chấm độc lập đã khóa trước mở nhãn](pstack-review-2026-10-29/scored.md) và [JSON](pstack-review-2026-10-29/scored.json).
- [Log make check](pstack-review-2026-10-29/make-check.log).
- Báo cáo từng cặp: [S1-A](pstack-review-2026-10-29/S1-A.md), [S1-B](pstack-review-2026-10-29/S1-B.md), [S2-A](pstack-review-2026-10-29/S2-A.md), [S2-B](pstack-review-2026-10-29/S2-B.md), [S3-A](pstack-review-2026-10-29/S3-A.md), [S3-B](pstack-review-2026-10-29/S3-B.md), [S4-A](pstack-review-2026-10-29/S4-A.md), [S4-B](pstack-review-2026-10-29/S4-B.md), [S5-A](pstack-review-2026-10-29/S5-A.md), [S5-B](pstack-review-2026-10-29/S5-B.md), [S6-A](pstack-review-2026-10-29/S6-A.md), [S6-B](pstack-review-2026-10-29/S6-B.md).
- [Runner và hướng dẫn tái lập](../../../scripts/revieweval/README.md).

Bước áp dụng tiếp: dùng bản thử cho một nhóm task thật, theo dõi finding được tác giả
xác nhận, vòng sửa, thời gian/token và tỷ lệ incomplete do môi trường. Giữ một
reviewer và scope hiện có trong giai đoạn này.
