# Local task management cho Mate: Backlog.md

Ngày khảo sát: 2026-10-12. Nguồn: tài liệu và source chính thức của [MrLesk/Backlog.md](https://github.com/MrLesk/Backlog.md), HEAD `92c79f84de5e8f1f6844ec5316f7b663da9fd4b9`; `package.json` khai báo phiên bản 1.53.0. Đây là khảo sát tài liệu/source, chưa cài hay chạy smoke test trên máy captain.

## Đề xuất

Chọn **Backlog.md** nếu muốn task manager local cho agent với giao diện dễ đọc hơn terminal: `backlog browser` mở Kanban web, kéo thả trạng thái, tạo/sửa task bằng form và sửa checklist acceptance criteria. Server mặc định nghe `127.0.0.1`; CLI và web dùng cùng file Markdown. Đánh giá “dễ đọc hơn” là nhận định từ giao diện được upstream minh hoạ, chưa phải đánh giá trực tiếp trên dữ liệu của captain. [README: Web Interface](https://github.com/MrLesk/Backlog.md/blob/92c79f84de5e8f1f6844ec5316f7b663da9fd4b9/README.md#web-interface)

Upstream mô tả local-first, không account, không telemetry; dữ liệu ở `backlog/`, `.backlog/` hoặc đường dẫn project-relative được cấu hình. Git là tuỳ chọn. Agent dùng CLI, output `--plain`/`--json` hoặc MCP; không cần tích hợp riêng trong Mate. [README: Getting started / AI agents](https://github.com/MrLesk/Backlog.md/blob/92c79f84de5e8f1f6844ec5316f7b663da9fd4b9/README.md), [CLI reference](https://github.com/MrLesk/Backlog.md/blob/92c79f84de5e8f1f6844ec5316f7b663da9fd4b9/CLI-INSTRUCTIONS.md)

## Setup đề nghị

Đặt tracker tại thư mục project của workspace, ngoài `.mate/` và ngoài repo/worktree của Crew. Một tracker dùng chung cho mọi repo của project. Thay đường dẫn và tên trước khi chạy:

```sh
npm i -g backlog.md
# macOS có thể dùng: brew install backlog-md

cd /absolute/path/to/workspace/my-project
backlog init "My project" --no-git
backlog instructions overview
backlog browser
```

Trong wizard có thể chọn CLI instructions hoặc không tích hợp AI nếu chỉ muốn tự viết prompt Mate. CLI instructions sinh lời nhắc đọc `backlog instructions overview` trong instruction file của project; Mate có cwd riêng nên cần hướng dẫn riêng của Mate trỏ đến tracker này. Không mặc định cho rằng AGENTS.md do tool sinh ở thư mục tracker sẽ tự được Mate đọc. Các lệnh install, `--no-git`, browser và lựa chọn CLI instructions được xác nhận trong [README](https://github.com/MrLesk/Backlog.md/blob/92c79f84de5e8f1f6844ec5316f7b663da9fd4b9/README.md) và [CLI reference: Project Setup](https://github.com/MrLesk/Backlog.md/blob/92c79f84de5e8f1f6844ec5316f7b663da9fd4b9/CLI-INSTRUCTIONS.md#project-setup).

`--no-git` tạo filesystem-only project và cấu hình `checkActiveBranches=false`, `remoteOperations=false`, `autoCommit=false`; không cần Git để thao tác task. Nếu khởi tạo trong repo Git thì remote operations mặc định bật; muốn offline phải tắt bằng `backlog config set remoteOperations false`. Cài npm/brew vẫn cần tải package ban đầu. [README: Configuration](https://github.com/MrLesk/Backlog.md/blob/92c79f84de5e8f1f6844ec5316f7b663da9fd4b9/README.md#configuration), [Advanced config](https://github.com/MrLesk/Backlog.md/blob/92c79f84de5e8f1f6844ec5316f7b663da9fd4b9/ADVANCED-CONFIG.md)

## Prompt mẫu cho Mate

Đây là policy của người dùng, không phải workflow hard-code trong Mate:

```text
Task manager của project là Backlog.md, tracker tại
/absolute/path/to/workspace/my-project.
Mọi lệnh backlog phải chạy với cwd đúng thư mục đó; không tạo tracker
trong .mate, repo con hoặc Crew worktree.
Đọc `backlog instructions overview` trước khi quản lý task.
Bạn là người duy nhất phân công task; Crew báo kết quả để bạn cập nhật.
Ghi task ID vào brief, đặt assignee và In Progress trước khi dispatch.
Chỉ đặt Done sau khi review và captain chấp nhận hoặc thay đổi đã landed.
Khi cập nhật báo conflict/lock, đọc lại task trước khi quyết định thử lại.
```

Ví dụ lệnh đã được tài liệu CLI xác nhận; ID thực tế lấy từ kết quả create:

```sh
backlog task create "Add login" --ac "Login succeeds with valid credentials"
backlog task list --json
backlog task TASK-1 --plain
backlog task edit TASK-1 -a @crew-k3 -s "In Progress"
backlog task edit TASK-1 -s "Done"
```

Default prefix mới là `TASK`; tên status có thể được người dùng cấu hình, nên agent phải đọc workflow/config trước khi áp dụng ví dụ. [README: Storage and prefixes](https://github.com/MrLesk/Backlog.md/blob/92c79f84de5e8f1f6844ec5316f7b663da9fd4b9/README.md), [CLI reference: Task Management](https://github.com/MrLesk/Backlog.md/blob/92c79f84de5e8f1f6844ec5316f7b663da9fd4b9/CLI-INSTRUCTIONS.md#task-management)

## Giới hạn cần biết

Source có test khoá mutation theo task, chống mất cập nhật âm thầm, từ chối process cạnh tranh và trả HTTP 409 khi contention. Test mô tả thao tác thua không tự chờ, merge hay retry. Đây là bằng chứng contract trong source upstream, chưa phải test đã chạy ở đây. Khoá ghi **không đồng nghĩa** với atomic claim “chỉ gán nếu chưa ai nhận”: không có bằng chứng cho contract đó trong các nguồn đã khảo sát. Vì vậy prompt mẫu để Mate làm một người phân công duy nhất, tránh nhiều Crew cùng tự claim. [Atomic task edit tests](https://github.com/MrLesk/Backlog.md/blob/92c79f84de5e8f1f6844ec5316f7b663da9fd4b9/src/test/atomic-task-edit.test.ts)

Markdown local thuận tiện để đọc, sao lưu, version control, nhưng filesystem-only setup không tự sync sang máy khác và không tự commit. Không có migration Beads được kiểm chứng trong lượt khảo sát này; giữ nguyên dữ liệu `.beads/`, chuyển task là công việc riêng có kiểm tra. Đề xuất Backlog.md là lựa chọn của người dùng, không phải dependency hay default của lõi Mate.
