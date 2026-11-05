# Linear cho Mate — 2026-10-12

Khảo sát tài liệu và pricing chính thức ngày 2026-10-12; chưa đăng nhập workspace của captain, chưa cài MCP, chưa thử API hoặc tạo issue. Gói khuyến mại Business 6 tháng là thông tin captain cung cấp; số seat, ngày hết hạn, tự gia hạn và AI credits đi kèm cần đối chiếu trong Billing của workspace. Các đề xuất dưới đây là thiết kế workflow, không phải integration đã hoạt động.

## Kết luận

Linear đáng thử làm nơi captain nhìn backlog, ưu tiên và review kết quả, trong khi Mate điều phối Crew local. Điểm hữu ích nhất là issue có trạng thái/quan hệ dependency, project, comment và liên kết PR; UI của người dùng và giao diện API cho agent cùng truy cập một nguồn task. Không cần đưa Linear vào lõi Mate. Sau khi bỏ Beads, một skill/prompt ngoài cùng MCP hoặc script API là đúng hướng.

Linear là dịch vụ cloud, không đáp ứng yêu cầu tracker local như Backlog.md. Đổi lại, nó hợp hơn nếu captain muốn quản lý nhiều project, trao đổi với người khác và mở backlog ở nhiều thiết bị. API/webhooks, MCP access và agent platform đã xuất hiện trong bảng pricing ở các gói, không phải lý do riêng để mua Business. Business đáng giá hơn khi có intake/team/analytics thực tế. [Pricing](https://linear.app/pricing), [MCP](https://linear.app/docs/mcp), [GraphQL](https://linear.app/developers/graphql).

## Những khả năng hữu ích

| Khả năng chính thức | Áp dụng cho Mate |
| --- | --- |
| Issues, projects, cycles, initiatives; trạng thái, priority, labels, relations blocking/blocked | Captain chọn kết quả cần đạt và ưu tiên; Mate chọn task đủ điều kiện, chia nhỏ và đặt acceptance trong issue. Một project Linear có thể đại diện cho sản phẩm; không bắt buộc mapping 1:1 với project Mate. [Pricing](https://linear.app/pricing), [Issue properties](https://linear.app/docs/issue-relations) |
| MCP tìm, tạo và cập nhật issue/project/comment | Mate đọc task, ghi kế hoạch, cập nhật tiến độ và link PR bằng tool của harness. Tên tool và bộ tham số thực tế cần kiểm tra khi kết nối, không hardcode theo tên đoán. [MCP](https://linear.app/docs/mcp) |
| GraphQL, SDK TypeScript, API key hoặc OAuth | Một script/skill ngoài Mate có thể cung cấp thao tác task khi harness không dùng MCP. API endpoint là `https://api.linear.app/graphql`; cần xử lý cả `errors` trong response HTTP 200. [GraphQL](https://linear.app/developers/graphql) |
| GitHub PR linking và status automation | Đưa `Fixes TEAM-123` vào mô tả PR. Linear liên kết PR rồi chuyển trạng thái theo draft/open/review/merge; mặc định PR open → In Progress, merge → Done. Mate vẫn review bằng bằng chứng Crew và tests. Không đóng issue chỉ vì pane idle hoặc Crew báo xong. [GitHub](https://linear.app/docs/github) |
| Business Insights | Theo dõi cycle time, lead time, workload của issue để thấy task mắc lâu, throughput và blocker. Không đo token/model cost của Crew; dữ liệu đó vẫn thuộc dashboard Mate. [Insights](https://linear.app/docs/insights) |
| Business Triage Intelligence | Gợi ý team/project/assignee/labels và issue trùng/quan hệ; hữu ích khi lượng yêu cầu nhiều. Có thể để suggestion để captain duyệt trước khi bật auto-apply. [Triage Intelligence](https://linear.app/docs/triage-intelligence) |
| Business Asks | Nhận request nội bộ qua Slack/email/web form rồi tạo issue; thread phản hồi sync về nguồn. Hữu ích nếu người gửi yêu cầu không trực tiếp dùng Mate. Không cần bật cho workflow solo ban đầu. [Asks](https://linear.app/docs/linear-asks) |
| Business Code Intelligence | Phân tích GitHub repo và trả lời có link file/commit/PR. Có ích cho người viết spec và support; chưa có bằng chứng rằng thay thế được scout Crew của Mate hoặc có API riêng cho Mate tiêu thụ. [Code Intelligence](https://linear.app/docs/code-intelligence) |

## Kết nối từng mức, tránh hứa quá khả năng hiện có

### 1. Tracker ngoài, do prompt/skill điều khiển

Official MCP dùng Streamable HTTP tại `https://mcp.linear.app/mcp`, OAuth 2.1; cũng hỗ trợ API key/bearer token. Endpoint `https://mcp.linear.app/mcp/readonly` chỉ có read tools. Setup trong **Claude Code thông thường** theo Linear:

```sh
claude mcp add --transport http linear-server https://mcp.linear.app/mcp
# Trong Claude Code: /mcp để xác thực
```

Codex có lệnh `codex mcp add linear --url https://mcp.linear.app/mcp`, rồi `codex mcp login linear` nếu cần. Trang Linear còn ghi flag rmcp experimental; phải kiểm tra `codex --help`/phiên bản cài thực tế, không thêm flag lịch sử một cách máy móc. [MCP setup](https://linear.app/docs/mcp).

**Giới hạn của Mate hiện tại:** `internal/harness/claude/claude.go` khởi chạy vai Mate với `--setting-sources project --strict-mcp-config` và không truyền `--mcp-config`. Vì vậy cài global Linear MCP chưa đủ để nó xuất hiện trong Mate Claude mặc định. Chưa chạy harness thật để xác minh Codex trong Mate. Bước triển khai phù hợp là thêm cấu hình MCP chung cho harness, cho user truyền cấu hình server tuỳ chọn; không thêm client, credential hoặc task logic Linear vào core. Trước đó có thể dùng script GraphQL ngoài qua Bash và skill riêng, với credential ngoài prompt/repo.

Prompt workflow đề xuất cho `.mate/WORKSPACE.md` hoặc skill do user sở hữu:

> Dùng Linear workspace X, team Y, project Z làm tracker. Chỉ xử lý issue captain giao hoặc có label mate-ready. Mate là dispatcher duy nhất; Crew không tự lấy issue khác. Đọc issue, comments và blockers trước khi spawn. Ghi issue ID trong brief cùng acceptance criteria. Ghi comment lúc bắt đầu, bị blocker và bàn giao; luôn kèm bằng chứng/link PR. Crew state độc lập với Linear status. Khi Crew xong, Mate review và chạy quy trình delivery; issue chỉ Done sau acceptance và merge theo policy. Không tự claim task đã có owner hoặc đang chạy. API lỗi thì ghi lại issue ID và báo trạng thái sync chưa xác nhận, không coi write là thành công.

Label, status và identity ở đây phải map vào ID thực tế của workspace. Chưa xác minh thao tác claim atomic/unassigned-only; một dispatcher là policy chống trùng, không phải cam kết locking do Linear cung cấp.

### 2. Mate thành agent xuất hiện ngay trong Linear

Linear có Agent API: người dùng @mention/delegate issue, agent nhận `AgentSessionEvent` và ghi progress/thought/action/elicitation/response/error, plan checklist và external URLs/PR. Human vẫn là assignee, agent là delegate. App agent không tính là billable user. [Getting started](https://linear.app/developers/agents), [Agent interaction](https://linear.app/developers/agent-interaction).

Đây **không tự có khi bật MCP**. Cần một integration riêng: OAuth app (`actor=app`, scope `app:mentionable`/`app:assignable`), webhook receiver có thể được Linear truy cập, xác minh event, deduplicate, queue và bridge tới Mate local; xử lý stop/retry/input/lifecycle. Receiver phải trả lời trong 5 giây và session mới cần activity/external URL trong 10 giây để không bị coi là unresponsive. Agent API vẫn ghi **Developer Preview**, có thể đổi trước GA. Không nên bắt đầu từ mức này cho pilot.

### 3. AI chạy bên Linear

Linear Agent/Loops/coding sessions có thể tự investigate, cập nhật task và viết PR. Loops chạy lịch hoặc event. Đây là một executor khác; nếu cùng tự nhận task `mate-ready`, sẽ dễ đụng Mate. Pilot nên để Mate/Crew thực thi và dùng Linear làm tracker; nếu thử coding sessions, tách task và ownership rõ ràng. [Loops](https://linear.app/docs/loops), [AI credits](https://linear.app/docs/ai-credits).

## Business 6 tháng và đường ra

- Giá public đang hiển thị **Business $16/user/month billed yearly**, Basic $10/user/month billed yearly; phí theo unsuspended users của một workspace. Business thêm unlimited teams, private teams/guests, Triage Intelligence, Code Intelligence, Insights, Asks và support integrations. Giá monthly và điều khoản khuyến mại cụ thể chưa xác minh. [Pricing](https://linear.app/pricing), [Billing](https://linear.app/docs/billing-and-plans).
- Coding sessions/Loops dùng **AI credits riêng, opt-in**, không được suy ra là miễn phí cùng Business. Docs ghi coding sessions tính provider model tokens + sandbox $0.25/20 phút; Loops điển hình $0.07–$0.20/run chưa coding. Credit promotional có ngày hết hạn riêng; số credit workspace này chưa biết. Tài liệu hiện có chênh lệch: pricing headline đặt Loops trong Business, docs Loops/AI credits nói paid plans/Basic+; kiểm tra entitlement trong workspace nếu muốn dùng. [AI credits](https://linear.app/docs/ai-credits), [Loops](https://linear.app/docs/loops).
- Free hiện **2 teams/250 issues**, Basic **5 teams/unlimited issues**. Khi cancel, docs nói không xoá dữ liệu, nhưng trên 250 issues không tạo issue mới được. Không có bằng chứng về hành vi tự kết thúc promo 6 tháng hay private teams/guest sau downgrade; kiểm tra Billing và terms grant trước tháng thứ sáu. [Pricing](https://linear.app/pricing), [Billing cancellation](https://linear.app/docs/billing-and-plans).
- Export workspace issue CSV có ID/title/description/status/priority/project/assignee/labels/cycle/timestamps/parent/milestone, v.v. CSV **không có attachment files**, danh sách field issue không chứa comments. Không coi CSV là backup đầy đủ. Muốn chuyển tracker nên xuất issue/project, lưu comments và attachments qua API/đường tải phù hợp và kiểm thử phục hồi. Docs hỗ trợ API export nhưng chưa xác minh code export đầy đủ ở nghiên cứu này. [Exporting data](https://linear.app/docs/exporting-data).

## Pilot đề xuất

1. Một team, một project, 10–20 issue thật trong hai tuần; workflow Backlog → Ready → In Progress → In Review → Done, label `mate-ready`, relations blocking/blocked, acceptance rõ.
2. Captain chọn/ưu tiên, Mate dispatcher duy nhất. Gắn issue ID vào brief/PR; một issue có thể có nhiều Crew, không đồng nhất issue status với Crew state.
3. Kết nối MCP sau khi có cấu hình harness chung, hoặc dùng skill + API script ngoài. Kiểm chứng read/write/comment, claim policy, blocker và PR link bằng một task nhỏ.
4. Đo captain có đọc backlog dễ hơn không, bao nhiêu task bị stale/trùng và thời gian request → merge. Chỉ bật Asks/Triage nếu có nhu cầu; Insights có dữ liệu sau vài tuần.
5. Trước hết hạn, thử export và quyết định Free/Basic/Business theo lượng issue và tính năng thật sự dùng. Không cần Business để Mate đọc/ghi tracker thông thường.
