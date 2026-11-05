# Mate

Ngôn ngữ dùng để điều phối công việc trong một workspace.

Mỗi thuật ngữ có dòng _Trong code_ chỉ một file hoặc identifier dùng từ đó, để tìm nhanh trong code. Bản đồ kiến trúc nằm ở [docs/architecture.md](docs/architecture.md).

## Language

### Người và agent

**Captain**:
Người điều hành workspace: giao việc cho Mate, trả lời câu hỏi và duyệt kết quả.
_Avoid_: user, operator
_Trong code_: `brief.Deciders`; `store.SourceUser` là dòng của captain trong `sent.log`

**Mate**:
Agent điều phối của một Project: nhận việc từ captain, giao cho Crew và xét kết quả Crew bàn giao. Mỗi Project có đúng một Mate.
_Trong code_: `harness.RoleMate`; `internal/spawn` khởi động và dừng Mate

**Crew**:
Agent thực thi một phần việc do Mate giao. Vòng đời Crew tách khỏi trạng thái việc mà công cụ theo dõi task của project ghi.

**Jev**:
Mô hình ngoài (TypeSafe) mà mate hỏi để gọi tên điều một pane đang hiện, khi bộ đọc fixture không đọc ra. Lời Jev chỉ là gợi ý, không tự cho phép hành động nào.
_Trong code_: `internal/screen/jev`; `notice.Model`

### Việc và bàn giao

**Project**:
Một đơn vị công việc sở hữu các repo, một Mate và kế hoạch thực hiện.

**Ship**:
Loại task mặc định: Crew đổi repo và giao thay đổi đó.
_Trong code_: `brief.Ship`; `store.WorkflowRoleShip`

**Scout**:
Loại task mà Crew trả kiến thức trong một report, không đổi repo.
_Trong code_: `brief.Scout`; `spawn.CrewIsScout`

**Hand-back**:
Lúc Crew dừng ở trạng thái chờ Mate và giao kết quả cho Mate xét, kèm bản Crew tự kể các kiểm tra nghiệm thu.
_Avoid_: handoff, báo cáo xong
_Trong code_: `crewstate.StateWaitMate`; `store.Workspace.CrewHandback`

**Lane**:
Tài nguyên máy mà các Crew của một repo tranh nhau, do captain đặt tên (ví dụ chỗ build iOS). Crew giữ lane khi vừa spawn hoặc đang làm.
_Trong code_: `store.RepoConfig.Lane`; `spawn.MetaLane`; `cmd/mate/lanes.go`

**Work** (bảng trực):
Bảng việc Mate công bố cho captain: việc captain giao và việc Mate đề xuất, mỗi dòng đang làm, đang chờ hay đã xong. Là thông báo, không phải câu hỏi.
_Avoid_: duty (code không dùng từ này)
_Trong code_: `matehost.Work`; lệnh `mate work`

### Trao đổi giữa captain, Mate và Crew

**News**:
Một câu hỏi Mate mở cho captain và chờ captain trả lời. Điều captain chỉ cần biết thì không là news.
_Avoid_: notification, thông báo
_Trong code_: `matehost.News`; lệnh `mate news`

**Topic**:
Một news nhìn như một cuộc nói chuyện nhỏ giữa captain và Mate; mỗi topic đúng một câu hỏi. Captain có thể mở phiên topic, một bản sao chỉ đọc của phiên Mate, để bàn trước khi trả lời.
_Trong code_: `matehost.TopicsDir`; `internal/matehost/session.go`

**Box**:
Hộp thư của một Project: dòng trạng thái của Crew, các dòng đã gửi vào agent và sự cố observer thấy, xếp theo thời gian.
_Trong code_: `internal/box`; `query.BoxView`

**Outbox**:
Hàng đợi mọi dòng mate tự gõ vào Mate (assign, digest, stow, tin PR), giao lần lượt từng dòng khi Mate rảnh.
_Trong code_: `internal/outbox`; `store.OutboxItem`

**Wake**:
Lần đọc Mate làm khi bị đánh thức: mọi việc đang mở, các dòng trạng thái mới và trạng thái từng Crew, gom trong một lần.
_Trong code_: `cmdWake` (`cmd/mate/wake.go`); `store.Workspace.WakeCursorFile`

**Stow**:
Lời mate nhắc Mate ghi lại những gì chỉ có trong hội thoại, ngay trước khi Mate bị restart hay đổi phiên.
_Trong code_: `memory.StowLine`; `outbox.Sender.Stow`

**Settle**:
Bước ngay sau khi một agent khởi động: trả lời các hộp thoại khởi động của harness cho tới khi pane sẵn sàng nhận lệnh.
_Avoid_: dùng "settle" cho việc outbox đánh dấu một dòng đã giao
_Trong code_: `spawn.Settlement`; `settleStartupPrompt` (`internal/spawn/settle.go`)

### Công cụ

**Tool**:
Chương trình ngoài mate mà console gắn phím và CLI bọc; dữ liệu của nó nằm trong thư mục project, không phải state của mate.
