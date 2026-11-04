# Benchmark: một session Claude Code so với Mate

Issue: CON-159. Ngày đăng ký phương pháp: 2026-11-04. Đăng ký trước khi chạy bất kỳ phương án nào và trước khi viết spec dự án thử. Đây là protocol; log thực thi phải ghi số run đã chạy và mọi sai khác so với protocol. Không suy kết quả từ tài liệu này. Mục `## Pilot (2026-11-04, một run mỗi arm)

Cùng starter 3be9b61, cùng 17 tiêu chí ẩn, cùng người chạy (phiên Claude Code của captain, trả lời theo FAQ, đếm mọi lần gõ). Log từng run: `~/bench-spec/runs/A1.md`, `~/bench-spec/runs/B1.md`; tư liệu màn hình và bảng usage: `~/bench-spec/runs/footage/` (repo local, chưa đẩy lên đâu).

### Bảng so sánh

| Thước | A1: một session Claude Code | B1: Mate |
| --- | --- | --- |
| 5.1 Wall-clock | 6 phút 25 giây (11:09:58 → 11:16:23) | 45 phút 49 giây (11:21:15 → 12:07:04), gấp 7,1 |
| 5.1 Chờ người | 0 | 0 |
| 5.2 Token tổng | 1.250.021 | 14.857.700, gấp 11,9 |
| 5.2 cache_read / cache_write / output | 1.108.291 / 94.148 / 47.546 | 13.563.200 / 1.030.000 / 283.200 |
| 5.2 Số lần gọi model | 18, không subagent | 333: Mate 73, crew 128, reviewer 132 |
| 5.3 Nghiệm thu | 17/17 ngay lần khai xong đầu | 17/17 ngay lần khai xong đầu |
| 5.4a Khai xong hụt | 0 | 0 |
| 5.4b Dòng đổi sau khai xong | 0 | 0 |
| 5.4c Review trả lại trước merge | không có lớp review | 1/11 report (lỗi thật: `--db` có `#`, `?`, `%` mở nhầm file) |
| 5.5 Can thiệp Q/N/C/M | 0/0/0/0 | 0/0/0/0; Mate không mở news nào |
| 5.6 Phục hồi | chưa chạy | chưa chạy |
| 5.7 Tiêu chí đạt trên 1M token | 13,6 | 1,14 |
| Code vào main | 1 commit, 8 file Go, 2.097 dòng | 8 commit, 33 file Go, 4.469 dòng, có bộ test e2e C01–C17 riêng và README |
| Cách chia việc | agent tự ghi "việc đủ nhỏ, tự làm" | scout → foundation → 4 crew song song → integration; 7 crew, 10 reviewer |

Token của B1 chia ba phần gần bằng nhau: Mate điều phối 4,9M (33%), crew 4,92M (33%), reviewer 5,04M (34%). Crew đứng chờ Mate tổng 29 phút trong 46 phút (cột WAITED của `mate usage`): độ trễ điều phối là phần lớn của wall-clock. Lượt Mate trung bình 258k token vì mỗi lượt mang theo ngữ cảnh 67–95k.

### Đọc kết quả

- A thắng ở 5.1, 5.2, 5.7: nhanh gấp 7, rẻ gấp 12, cùng 17/17. 5.3, 5.4a, 5.4b, 5.5 hoà ở mức tốt nhất, nên không phân biệt được hai arm.
- B thắng ở những thứ 17 tiêu chí không đo: review độc lập bắt một lỗi thật trước khi vào main (A không có lớp này, nhưng tiêu chí ẩn cũng không bắt được lỗi nào ở A), mọi nhánh qua review rồi mới merge, có bộ test e2e và README do crew tự viết, không hỏi người câu nào.
- Chưa phân định: 5.6 chưa chạy; mới một run mỗi arm (mục 7 đòi ít nhất ba); dự án 2k dòng, làm xong trong một ngữ cảnh, đúng vùng mà mục 10 đã đăng ký là A sẽ thắng.
- Kết luận pilot: với dự án một session làm xong trong 10 phút, Mate tốn gấp 12 lần token và 7 lần thời gian cho cùng kết quả nghiệm thu. Đúng như giả định đăng ký trước: Mate không mặc nhiên tiết kiệm token. Nếu Mate có lợi thế thì nó nằm ở vùng chưa đo: dự án dài hơn một ngữ cảnh, bị gián đoạn, nhiều việc song song cần người ít can thiệp.

### Sai khác so với protocol

- Người chạy cả hai run là phiên Claude Code của captain, không phải captain.
- A1: lệnh khởi động agent do captain gõ (classifier chế độ auto chặn phiên chạy agent bỏ hỏi quyền); mọi việc sau đó phiên làm.
- B1: Mate điều phối chạy effort medium (mặc định của mate cho Mate), crew và reviewer high như ghim; target gửi qua POST /api/actions `say` vì Office chưa nối chat; watcher sót "XONG." vì regex đòi XONG ở cuối câu, giờ kết thúc lấy từ `response_at` của lượt cuối, repo không đổi từ 11:59:44.

### Việc tiếp

- Run phục hồi 5.6 (kill -9 giữa chừng) cho cả hai arm.
- Lặp đủ ba run mỗi arm.
- Dự án lớn hơn một ngữ cảnh (candidate 2 ở mục 4) để thử vùng mà B được kỳ vọng thắng.
