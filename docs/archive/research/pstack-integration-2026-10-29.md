# Tích hợp pstack vào mate

Ngày khảo sát: 2026-10-29. Đây là nghiên cứu source và thiết kế tích hợp, chưa phải kết quả chạy pstack trong Mate/Crew.

## Kết luận từ upstream

Nên thử một phần nhỏ các thực hành kiểm chứng của pstack trong workflow Crew. Không nên nhập nguyên `poteto-mode` hoặc đưa một bộ điều phối pstack chạy bên trong Mate. Giá trị đáng thử là tìm giả định quyết định tính đúng, chứng minh bằng lệnh/repro trên artifact thật, và giữ finding có bằng chứng. Lợi ích này có thể lấy mà không nhận thêm lifecycle, model routing và quy tắc merge của pstack.

Pstack là bộ quy trình kỹ thuật dựa trên skill, không phải riêng công cụ stacked PR. Upstream có 58 skill, gồm 24 principle; 23 playbook; hai subagent; một số script hỗ trợ. `poteto-mode` chọn playbook, gọi các skill nghiên cứu, thiết kế, triển khai, review, rồi xử lý PR. Đây là mô tả và số đếm từ source, chưa phải bằng chứng rằng workflow cải thiện chất lượng hoặc chi phí của mate. [README][readme], [poteto-mode][mode].

## Snapshot và cách khảo sát

- Repo: `cursor/plugins`, thư mục `pstack/`.
- Commit toàn repo: [`1ba91fc25c0977741dd0e267849e6234b037ed0a`](https://github.com/cursor/plugins/commit/1ba91fc25c0977741dd0e267849e6234b037ed0a), main được đọc ngày 2026-10-29.
- Manifest ghi phiên bản **0.15.36**, tác giả Lauren Tan, license MIT. [Manifest][manifest], [LICENSE][license].
- Đọc source qua GitHub API/gh-axi và snapshot archive của commit, giải nén vào thư mục tạm; không cài pstack, không chạy script vận hành, không đổi cấu hình harness. Không chạy suite upstream hay live harness.
- Pstack xuất hiện trong repo này từ [commit ngày 2026-05-23](https://github.com/cursor/plugins/commit/24bd6eb895d63a6e76fabfea2cb344384d177d79). Có nhiều thay đổi trong tháng 10. Đặc biệt [thay đổi ngày 2026-10-27](https://github.com/cursor/plugins/commit/c742dd61706fbacc1b95f1a20b7b988cf096ce6c) **xoá orch CLI/scripts** và chuyển Orchestrate sang công cụ native của agent. Không thiết kế tích hợp dựa trên orch cũ.

## Cơ chế hiện tại

| Phần | Hành vi trong source | Ý nghĩa với mate |
| --- | --- | --- |
| `poteto-mode` | Chat nhận skill sở hữu playbook, todo và mọi lần spawn; nontrivial change gọi `how`; code qua function gọi `architect`; delegate dùng `poteto-agent`. | Là một bộ điều phối có ý kiến riêng, không phải thư viện thao tác trung tính. |
| Feature/Bug fix/Refactoring | Có grounding, delegation, kiểm chứng runtime, commit, mở PR. Feature cho phép agent bị cấm spawn tự viết diff với review tách biệt. | Ngoại lệ no-spawn chỉ giải quyết một bước; chưa loại được toàn bộ fan-out trong `how`, `architect`, review và đường PR. |
| `arena` | N thử nghiệm riêng, worktree/output riêng, judge khác model family, chọn nền và ghép phần tốt, kiểm chứng lại. | Hữu ích khi lựa chọn thiết kế thật sự đáng chi phí; cần Mate sở hữu các Crew tương ứng. |
| `interrogate` | Mỗi adversary phải chạy repro trên code thật; lead chạy lại từng finding, bỏ finding không chứng minh được. | Phần tiêu chuẩn bằng chứng đáng giữ; không cần mang nguyên cơ chế nhiều agent vào reviewer Crew. |
| `orchestrate` | Coordinator không viết code; Task workers, Project tracker `notes.md`, Project store, PR/CI subscriptions khi harness có; một owner/PR; verifier khác family; khoảng 10 worker/coordinator, tối đa ba tầng. | Trùng vai Mate nhưng giả định một runtime và hệ state khác. |
| `autopilot-full`/`shipping` | Owner có quyền merge sau verifier; gh mặc định, Origin tuỳ có; kiểm patch/head, rebase/retarget, merge từ dưới lên. | Không phải skill chỉ đọc hay bổ sung chất lượng; thay đổi ownership và quyền lifecycle. |
| `multi-phase-plan` | Plan theo skeleton, `check-plan.mjs`; unit/live/perf, 10 live lanes/PR, review ảnh/video cho tương tác. | Quá nặng làm default cho các task nhỏ; cần chọn bằng chứng theo loại thay đổi. |

Nguồn: [mode][mode], [feature][feature], [arena][arena], [interrogate][interrogate], [orchestrate][orchestrate], [autopilot-full][autopilot], [shipping][shipping], [multi-phase-plan][plan].

Pstack có ghi nhận rõ rằng green CI không thay thế runtime proof, author không tự tạo verdict độc lập, một lần đổi patch/head có thể làm verdict cũ hết hiệu lực. Các quy tắc này hữu ích, nhưng không phải mọi thao tác phải rerun toàn bộ suite. Autopilot hiện đã phân biệt full swarm cho thay đổi lớn, một verifier cho sửa nhỏ/rebase và không thêm verifier cho wording-only. Đó là một lý do nên lấy tiêu chuẩn bằng chứng cùng quy tắc tránh lặp gate. [Autopilot][autopilot], [shipping][shipping].

## Portability và các phụ thuộc

**Cursor là bản nguồn. Claude Code có adapter upstream chính thức trong repo.** Thư mục `harness/pstack/claude-code/` được generate từ source; generator hiện chỉ tạo tree Claude Code. Chưa thấy tree hay adapter Codex/pi trong snapshot đã kiểm tra. Điều này không có nghĩa leaf skill không dùng được; nghĩa là khả năng tương thích toàn workflow chưa được upstream thể hiện. [Harness tree][harness], [generator][generator].

Adapter Claude Code chuyển `Task` thành `Agent`, cloud environment thành worktree, path `.cursor` thành `.claude`, và model ngoài Claude thành model cha. Resume thành spawn mới với kết quả cũ trong brief. Skill đọc trực tiếp file skill khác vì Skill tool từ chối nhiều skill. Các bước Cursor-only như Automations/Bugbot được bỏ qua với ghi chú. Đây là giới hạn có tài liệu, không phải chức năng tương đương đã được đo trong mate. Đặc biệt, fallback sang model cha có thể làm mất tính đa dạng model mà arena/review muốn có; không được gọi kết quả ấy là cross-family review. [Adapter Claude Code][claude].

Các phụ thuộc chỉ xuất hiện khi gọi tính năng tương ứng:

- `deslop`, `control-cli`, `control-ui` thuộc plugin `cursor-team-kit`; `create-skill` và `/loop` là chức năng Cursor được các playbook tham chiếu. Pstack không bundle chúng. [README][readme].
- Git/gh hoặc Origin phục vụ PR; Graphite không bắt buộc. `watch-pr` dùng Bun và commander 14.0.0, bootstrap có thể tự `bun install --frozen-lockfile`. Không coi watcher là executable không có side effect cài dependency. [Shipping][shipping], [watch-pr entry][watch], [bootstrap][bootstrap], [tools package][tool-package].
- `check-plan.mjs` chạy Node; `worktree-audit.sh` gọi Git, gh/Origin và đọc transcript; cleanup playbook còn có lệnh xoá worktree/cache/simulator. Không mang cleanup sang Mate chỉ vì cùng dùng git worktree. [Plan][plan], [cleanup][cleanup], [audit script][audit].
- `statechart` là compiler TypeScript cho TLA+/TLC, có launcher chọn Node 22.18–22.x, Node ≥23.6, Bun ≥1.1.5 hoặc Deno ≥2.0. TLC lấy từ `TLA2TOOLS`/`JAVA`, hoặc có thể cài qua mise; render SVG tham chiếu Pele. Đây là nhánh công cụ riêng, không phải dependency cần có để dùng checklist bằng chứng. [Launcher][state-launch], [runtime][state-run], [render][state-render].
- Benny là pack tự động hoá triage/repro/fix issue từ Slack, dormant và Cursor-only; không phải một slash skill thông thường. [README][readme].

MIT cho phép dùng/sửa/phân phối với việc giữ copyright và license notice theo văn bản license. Nếu vendor hay sửa skill, lưu notice cùng nguồn gốc và commit. [LICENSE][license].

## Chi phí context và dependency closure

Đếm byte trên các file UTF-8 của snapshot nguồn, không phải token, không phải lượng context harness thực nạp:

| Tập file | Số file | Byte |
| --- | ---: | ---: |
| `skills/*/SKILL.md` | 58 | 223,086 |
| `poteto-mode/playbooks/*.md` | 23 | 76,578 |
| Markdown dưới các `references/` | 29 | 111,049 |
| Toàn `skills/`, gồm script/test/fixture | 535 | 3,425,742 |

Riêng `poteto-mode/SKILL.md` là 21,670 byte. Harness thường có thể tải skill khi cần; không nên suy rằng tất cả 223 KB tự động đi vào mọi turn. Tuy vậy nhập toàn bộ tạo thêm catalog, các đường gọi skill lồng nhau và maintenance burden. Có thể tái lập số đếm từ cây source ở [commit đã pin][tree].

| Ứng viên | Byte của SKILL.md | Phụ thuộc semantic trong source |
| --- | ---: | --- |
| `blast-radius` | 3,856 | Gọi bước của `why`, viết qua `unslop`, optional `arena` cho thay đổi lớn. |
| `interrogate` | 2,314 | `in-your-own-words`; các pha của `arena`; nhiều model; worktree riêng; rerun bằng chứng. |
| `architect` | 5,469 | `how`, có thể `why`; bắt buộc `arena`, ≥2 thiết kế khác cấu trúc; 3 file reference; có đường implement. |
| `how` | 2,651 | Explorer/explainer và reference prompts; không đơn thuần checklist grep. |
| `why` | 8,903 | Nhiều nhóm nguồn và reference riêng; discovery MCP lúc chạy. |
| `arena` | 5,598 | Model rule, Task, output riêng, judge và nhiều principle. |

Nguồn: [blast-radius][blast], [interrogate][interrogate], [architect][architect], [how][how], [why][why], [arena][arena]. Vì vậy “copy hai SKILL.md” không tương đương tích hợp hai skill upstream. Phiên bản một Crew cần viết lại scope và gọi nó là adaptation có chủ đích.

## Chọn phần nào để thử

**Pilot phù hợp nhất:** một scout/reviewer Crew nhận một diff cụ thể và brief hẹp, dùng hai practice đã giản lược:

1. Từ `blast-radius`: tìm một hoặc hai giả định mà tính an toàn của diff dựa vào; đọc caller/consumer ngoài diff; chứng minh giả định bằng probe/repro vào code thật. Nếu chưa chứng minh được thì ghi rõ unproven.
2. Từ `interrogate` và `prove-it-works`: finding ghi command, expected, actual, artifact, head SHA; chỉ nâng thành finding đã xác nhận khi run tái hiện được. Findings từ đọc code và rủi ro vật chất chưa chạy được vẫn phải giữ dưới dạng unverified/gap; không giả làm runtime proof và không âm thầm loại bỏ. Không suy `no verified findings` thành pass khi còn acceptance chưa kiểm. Tách quyền review khỏi quyền sửa/merge.

Không cần nhiều reviewer ngay vòng đầu. Giữ report tại đường dẫn Crew được giao, không tự tạo tracker, state store, worker fleet hay worktree ngoài workflow Mate. Khi cần quyền ghi repro trong scout, brief phải cho phép rõ vùng scratch và vẫn cấm sửa product code. Tiêu chuẩn này là đề xuất cho mate, không phải hành vi đã có của upstream. [Blast radius][blast], [interrogate][interrogate], [prove-it-works][proof].

**Để sau:** sketch interface theo `architect` khi có quyết định thiết kế thật; `benchmark-checklist` cho task perf; formal verification/statechart cho một invariant cụ thể như delivery/retry nếu có đủ khả năng nối model về code. Formal verification upstream cũng yêu cầu nêu bounds, chứng minh check có thể fail và không coi model pass là toàn hệ thống pass. [Architect][architect], [benchmark checklist][benchmark], [formal verification][formal].

**Loại khỏi pilot:** `poteto-mode` nguyên bản, `poteto-agent`, arena/swarm fan-out trong Crew, orchestrate, autonomous/autopilot, shipping/babysit, cleanup, setup model rule toàn máy, recall/reflect/correct tự sửa policy, Benny. Các phần này thay đổi quyền, state, lifecycle hoặc chi phí và cần quyết định riêng. Không cần mang rule văn phong/`no-comments` vào cùng thử nghiệm chất lượng kỹ thuật.

## Cách giữ nguồn gốc mà không thêm package manager

Một adaptation nên ghi nguồn repo, commit, version, danh sách file gốc, checksum và những thay đổi có chủ đích: một agent, không spawn, không merge, chỉ vùng scratch được phép, output theo handback hiện tại. Đưa MIT notice cạnh nguồn đã vendor. Chỉ update qua diff được review và chạy lại bộ thử đã chọn; không tự lấy `main` lúc spawn. Đây là đề xuất provenance nhỏ, không phải registry hoặc installer mới.

CI upstream có validate manifest, kiểm generated Claude tree không lệch và chạy các test script TypeScript; nguồn cũng có test cho watcher/plan. Tuy nhiên existence của test/CI không chứng minh hiệu quả của prompt, và lệnh CI hiện không đồng nghĩa chạy mọi test ở mọi thư mục. Khảo sát này không chạy chúng. Cần pilot thực tế để kết luận chất lượng, token và số vòng sửa của mate. [Workflow][ci], [tools package][tool-package].

## Đối chiếu với mate đang có

Đã đọc `docs/mvp.md`, source và template của working tree ngày 2026-10-29: HEAD `d5e2b041992545e7ce9155c1f945cf584a81e9fc`, branch `integ/con-102-103`, có thay đổi chưa commit từ trước. Các nhận xét dưới đây mô tả working tree này, không khẳng định mọi thay đổi đã merge hoặc mọi phiên đang chạy đã nhận chúng.

| Nhu cầu | mate hiện có | Phần bổ sung hợp lý |
| --- | --- | --- |
| Hiểu yêu cầu, chọn ship/scout, giao việc | `new-task`: phân loại unknown, scope/acceptance, một repo/Crew, chọn profile, kiểm brief. | Không thêm một vòng intake của poteto-mode; chỉ thêm câu hỏi kỹ thuật có thể làm thay đổi kết luận. |
| Điều phối và isolation | Mate sở hữu Crew/worktree; Crew không được delegate hay làm supervisor. | Kỹ thuật khảo sát, probe và review chạy bên trong một Crew. Nếu thật sự cần nhiều người xem, Mate tạo reviewer tương ứng. |
| Kiểm chứng delivery | Crew phải chạy `verify:`; review độc lập dùng exact base/head, acceptance table, verdict `pass`, `changes-requested`, `incomplete`. | Làm rõ giả định quyết định tính đúng và probe rẻ nhất để bác bỏ nó; kiểm chính xác command nào bao phủ hành vi nào. |
| Profile/model | `crew dispatch` đọc rule, size, preference, rotation/quota. | Tiếp tục dùng profile đó; không hardcode model slug từ Cursor hoặc thêm `setup-pstack` toàn máy. |
| Memory và học sau task | `task-learning` có record gắn revision, giới hạn 40 dòng/3 theme, usage và prediction pointers. | Tái dùng record này để theo dõi pilot; không dựng thêm recall/reflect store. |

Nguồn local: [spec](../../mvp.md), [new-task](../../../assets/mate/skills/new-task/SKILL.md.tmpl), [Crew brief](../../../assets/crew/brief.md.tmpl), [review brief/check](../../../cmd/mate/review.go), [review-delivery](../../../assets/mate/skills/review-delivery/SKILL.md.tmpl), [task-learning](../../../assets/mate/skills/task-learning/SKILL.md.tmpl).

**Không thể cài trực tiếp qua plugin hiện tại.** Có bốn vướng mắc cụ thể:

1. Manifest mate là `mate-plugin.yaml`, trong khi upstream dùng `plugin.json` và metadata riêng của harness. Thêm một manifest wrapper chỉ giải quyết định dạng.
2. `resolveMatePlugins` chỉ được gọi cho Mate; Crew không nhận workspace plugins. Trong khi các practice đang muốn thử cần source code và test ở worktree của Crew.
3. `loadSkill` đọc một file; `WritePluginSkills` chỉ ghi `SKILL.md` vào `plugin-<instance>-<name>/`. Không copy `references/`, `playbooks/`, script hay subagent definitions. Những liên kết tương đối và cách gọi skill upstream sẽ không tự giữ được sau đổi layout/tên.
4. Load khác nhau giữa harness: Claude Crew giữ project skill nhưng bỏ user-level skill/plugin; pi khởi động với `--no-skills`, chỉ cấp đường dẫn skill rõ ràng cho vai Mate; Codex Mate được manual trỏ đọc file. Không suy rằng copy vào `.claude/skills` là đủ cho mọi Crew.

Nguồn: [plugin docs](../../plugins.md), [resolver](../../../internal/spawn/plugins.go), [manifest loader](../../../internal/plugin/manifest.go), [skill writer](../../../internal/mateassets/plugin_skills.go), [Claude launch](../../../internal/harness/claude/claude.go), [pi launch](../../../internal/harness/pi/pi.go), [Codex profile](../../../internal/harness/codex/codex.go). `docs/plugins.md` còn một đoạn pilot cũ nói Codex/pi bị từ chối MCP; phần Codex/pi phía dưới và source mới đã hỗ trợ chúng. Kết luận ở đây dựa trên đường chạy hiện tại, không dùng đoạn cũ ấy để kết luận thiếu MCP.

**Giá trị gia tăng cần chứng minh.** mate đã yêu cầu nhiều hành vi mà pstack khuyến khích. Audit learning ngày 2026-10-28 ghi nhận việc truyền kiến thức qua brief và handback có thật, nhưng vẫn có báo cáo sai coverage, lặp gate và thiếu số đo closure; chưa chứng minh tiết kiệm token/thời gian. Đây là bằng chứng lịch sử trong report hiện có, không phải một lần audit runtime mới của nghiên cứu này. [Learning audit](mihuv2-learning-workflow-audit-2026-10-28.md), [gate audit](mihuv2-gate-audit-2026-10-28.md).

Vì vậy kỳ vọng hợp lý là giảm bỏ sót assumption/coverage gap. Không nên hứa rằng nhiều skill hay nhiều model sẽ tự tăng chất lượng. Một cách chọn gate đúng scope đã có số đo riêng trong [benchmark 2026-10-28](mihuv2-gate-benchmark-2026-10-28.md); đó cũng là lời nhắc rằng sửa công cụ có thể hiệu quả hơn thêm chỉ dẫn. Số đo đó không phải hiệu quả của pstack.

Về kích thước, working tree hiện có 8 template skill built-in, tổng **61,887 byte**, manual **19,466 byte** (đếm kích thước file, chưa render, không phải token). [CON-69](https://linear.app/controlplane/issue/CON-69/nghien-cuu-rut-gon-bo-skill-cua-mate-cho-hop-workflow-mate-crews-bo) ghi lại đợt rút gọn bộ skill trước đây. Đưa thêm toàn bộ 58 skill sẽ đi ngược mục tiêu giảm tải nếu không có số đo cho thấy đáng giá. Không cộng các byte này thành chi phí context mỗi turn.

## Hướng triển khai đề xuất

Các bước dưới đây là kế hoạch, chưa triển khai hoặc tạo issue mới.

### Bước 1: một pilot không đổi Go/runtime

- Chọn **một project**, một loại task có rủi ro ở caller, dữ liệu hoặc lifecycle; task sửa chữ làm đối chứng nhẹ. Mặc định vẫn là workflow hiện tại.
- Viết hai tài liệu/skill tự chứa lấy ý từ `blast-radius` và `interrogate`, đặt trong workflow của repository được thử, ví dụ `docs/harness/quality/`. Giữ nguồn gốc/MIT; cắt các lời gọi `arena`, `why`, `unslop` và chuyển phần thực sự cần thành chỉ dẫn trực tiếp. Đây là bản adaptation, không gọi là hỗ trợ đầy đủ pstack.
- Brief của task được chọn trỏ đường dẫn trong worktree và yêu cầu Crew đọc trực tiếp. Nếu cần áp dụng có điều kiện cho reviewer tự sinh, dùng `CREW.md` **của project** với trigger hẹp và chỉ dẫn đọc path đó. Cơ chế này đã nối nội dung vào brief, kể cả scout/reviewer; không phụ thuộc slash-command discovery. Xác nhận một lượt thật trên mỗi harness muốn hỗ trợ trước khi tuyên bố tương thích. [Spawn/read rules](../../../internal/spawn/crew.go), [brief rendering](../../../internal/mateassets/render.go).
- Không nạp cả bộ vào `.mate/plugins.yaml`: instance mới hiện áp dụng toàn workspace cho các Mate, khác phạm vi pilot một project/Crew.
- Mate vẫn nhận yêu cầu, giữ scope, giao việc, quyết escalation và landing. Crew thực thi/probe trong worktree của mình; reviewer chỉ review, không sửa product hay merge. Probe của scout chỉ ghi scratch được brief cho phép. Giữ handback/report/verdict chuẩn hiện có.
- Bằng chứng phải nêu base/head thực tế, command, expected/actual và phạm vi bao phủ. Probe phải chạy code của target SHA. Không chạy lại full gate chỉ để có báo cáo mới khi đã có evidence đúng revision và project cho phép tái dùng; vẫn thực hiện các check độc lập hoặc gate mà hợp đồng yêu cầu. Phần không chạy được phải hiện thành gap, không được chuyển thành pass.

Luồng dự kiến: **captain → Mate chốt brief và chọn practice → Crew thực thi → reviewer kiểm assumption/acceptance trên target SHA → Mate nhận verdict và xử lý delivery**. Pstack đóng góp cách làm ở hai bước giữa; trạng thái và quyền vẫn theo mate.

### Bước 2: đo tác dụng trước khi mở rộng

Đề xuất một vòng nhỏ **6 cặp bài** trên diff/SHA cố định: bug có repro, đổi contract/caller, đổi state/lifecycle, sửa nhỏ an toàn và các trường hợp thiếu coverage. Mỗi cặp chạy workflow hiện tại và bản adaptation bằng phiên mới, cùng model/effort, brief, tool và tài nguyên tương đương. Đảo thứ tự chạy; người chấm đọc kết quả ẩn nhãn và đối chiếu tiêu chí đã định trước. Bao gồm diff không lỗi để đo báo nhầm. Đây là kế hoạch thử, chưa có kết quả; mẫu nhỏ chỉ cho tín hiệu định hướng.

Sau đó mới thử vài task thật, tính chi phí toàn task, gồm implementer, reviewer và phần điều phối có thể quy về task; thiếu phần nào ghi unknown. Dùng `mate usage --why` và artifact sẵn có. Tổng token, cache-read, output, thời gian chạy gate, thời gian chờ và wall time là các số khác nhau; không dùng một số thay cho tất cả. [Usage implementation](../../../cmd/mate/usage_why.go), [learning procedure](../../../assets/mate/skills/task-learning/SKILL.md.tmpl).

| Chỉ số | Cách đánh giá |
| --- | --- |
| Lỗi thật bắt được, lỗi đã biết bị bỏ sót | Ground truth của diff/repro và người review, không đếm số bullet. |
| Báo nhầm và khẳng định vượt bằng chứng | Probe đúng target, coverage thật; ghi riêng unverified với confirmed. |
| Số vòng sửa, scope drift | Có tránh sửa lại/làm quá yêu cầu hay chỉ sinh thêm thủ tục. |
| Chi phí | Token theo bucket, thời gian cả vòng, số lần gate/probe trùng lặp cùng revision. |
| Tuân thủ vai trò | Không nested spawn, tự merge, đổi model ngoài dispatch hoặc né `needs-decision`. |

Chỉ mở rộng khi có lợi ích kiểm chứng cụ thể, không làm giảm acceptance và chi phí tăng nằm trong ngân sách đã chốt cho bài thử. Nếu chỉ đổi văn phong report, thêm finding nhiễu hoặc tăng lượt đọc/gate mà không bắt thêm lỗi hữu ích, bỏ adaptation hoặc giữ riêng cho task rủi ro cao. Không đặt ngưỡng tiết kiệm phần trăm khi chưa có baseline.

### Bước 3: chỉ sau pilot mới làm support chung cho Crew

[CON-70](https://linear.app/controlplane/issue/CON-70/crew-claude-chi-nap-djung-tool-va-skill-no-can-cat-context-nen-63k) đã nêu nhu cầu chọn đúng tool/skill theo task, hiện ở Backlog khi đọc ngày khảo sát. Đây là chỗ hợp lý để nối tiếp nếu pilot chứng minh cần phân phối skill qua nhiều project; chưa cần một registry pstack riêng.

Khi đó, seam nên nằm ở **chọn và cấp tài nguyên cho Crew lúc spawn**, dùng module hiện có; không tạo scheduler hoặc state machine pstack song song. Cần quyết định rõ ba việc: skill nào/role nào được cấp, file resource nào đi cùng, revision nào được ghi vào launch metadata. Selection đi từ cấu hình/brief đã chốt; adapter từng harness nhận đường dẫn/launch config phù hợp. Giữ workflow kỹ thuật trong package/repo harness để có thể thay đổi mà không sửa mọi nơi trong core.

Phạm vi code có thể liên quan: `internal/plugin` nếu dùng package chung, `internal/spawn/crew.go`, `internal/mateassets`, các adapter `internal/harness/*`, profile/dispatch và metadata. Đây là vị trí khảo sát tiếp, không phải đề xuất sửa tất cả ngay. Ưu tiên tái dùng cơ chế đang có, tránh thêm interface chỉ bọc một danh sách skill cố định.

Nghiệm thu khi có code: `make check`; resource và path còn hợp lệ trong worktree; không ghi đè skill người dùng; task không chọn không nhận skill; resume dùng đúng revision; một vòng implement/review/handoff live trên mỗi harness được công bố hỗ trợ. Test render/argv và suite xanh nhưng skip `TestLive*` không chứng minh agent đã đọc, tuân thủ hay làm tốt hơn. Ghi nhận chính xác live run nào đã chạy.

**Quyết định đề xuất:** làm Bước 1–2 như một thử nghiệm có thể bỏ dễ dàng; hoãn Bước 3 cho đến khi có lợi ích rõ. Không fork toàn bộ pstack và không xây khả năng chạy plugin Cursor nói chung chỉ để đạt mục tiêu này.

## Danh mục snapshot

34 skill không phải principle, nhóm theo công dụng để đọc nhanh (cách nhóm của nghiên cứu):

- Điều hướng/cá nhân hoá: `poteto-mode`, `poteto-help`, `setup-pstack`, `automate-me`, `figure-it-out`.
- Hiểu việc và giải thích: `how`, `why`, `recall`, `ketchup`, `ketchup-video`, `teach`, `in-your-own-words`, `sharpen`, `bro`.
- Thiết kế/review/xác minh: `architect`, `arena`, `swarm`, `interrogate`, `blast-radius`, `tdd`, `benchmark-checklist`, `create-verification-skill`, `maintain-verification-skill`, `create-formal-verification-skill`, `maintain-formal-verification-skill`, `statechart`.
- Ghi nhận và chất lượng: `reflect`, `correct`, `show-me-your-work`, `no-comments`, `typescript-best-practices`, `technical-writing`, `unslop`.
- Tích hợp UI: `make-bot-ui`.

24 principle được liệt kê đầy đủ trong [README][readme]. Hai agent là `poteto-agent` và `comment-sicko`. Ba nhóm script nằm trong `poteto-mode`, `show-me-your-work`, `statechart`; nhóm đầu chứa `check-plan`, `watch-pr`, `worktree-audit`, không còn `orch`.

23 playbook: investigation, bug-fix, perf-issue, hillclimb, runtime-forensics, trace-forensics, feature, refactoring, prototype, visual-parity, authoring-a-skill, eval, babysit, shipping, autonomous-run, orchestrate, autopilot-full, autopilot-stack, session-pickup, pause-safely, multi-phase-plan, worktree-cleanup, opening-a-pr. [Cây source][tree].

[tree]: https://github.com/cursor/plugins/tree/1ba91fc25c0977741dd0e267849e6234b037ed0a/pstack
[readme]: https://github.com/cursor/plugins/blob/1ba91fc25c0977741dd0e267849e6234b037ed0a/pstack/README.md
[manifest]: https://github.com/cursor/plugins/blob/1ba91fc25c0977741dd0e267849e6234b037ed0a/pstack/plugin.json
[license]: https://github.com/cursor/plugins/blob/1ba91fc25c0977741dd0e267849e6234b037ed0a/pstack/LICENSE
[mode]: https://github.com/cursor/plugins/blob/1ba91fc25c0977741dd0e267849e6234b037ed0a/pstack/skills/poteto-mode/SKILL.md
[feature]: https://github.com/cursor/plugins/blob/1ba91fc25c0977741dd0e267849e6234b037ed0a/pstack/skills/poteto-mode/playbooks/feature.md
[arena]: https://github.com/cursor/plugins/blob/1ba91fc25c0977741dd0e267849e6234b037ed0a/pstack/skills/arena/SKILL.md
[interrogate]: https://github.com/cursor/plugins/blob/1ba91fc25c0977741dd0e267849e6234b037ed0a/pstack/skills/interrogate/SKILL.md
[orchestrate]: https://github.com/cursor/plugins/blob/1ba91fc25c0977741dd0e267849e6234b037ed0a/pstack/skills/poteto-mode/playbooks/orchestrate.md
[autopilot]: https://github.com/cursor/plugins/blob/1ba91fc25c0977741dd0e267849e6234b037ed0a/pstack/skills/poteto-mode/playbooks/autopilot-full.md
[shipping]: https://github.com/cursor/plugins/blob/1ba91fc25c0977741dd0e267849e6234b037ed0a/pstack/skills/poteto-mode/playbooks/shipping.md
[plan]: https://github.com/cursor/plugins/blob/1ba91fc25c0977741dd0e267849e6234b037ed0a/pstack/skills/poteto-mode/playbooks/multi-phase-plan.md
[harness]: https://github.com/cursor/plugins/blob/1ba91fc25c0977741dd0e267849e6234b037ed0a/harness/pstack/README.md
[generator]: https://github.com/cursor/plugins/blob/1ba91fc25c0977741dd0e267849e6234b037ed0a/scripts/generate-pstack-harness-trees.mjs
[claude]: https://github.com/cursor/plugins/blob/1ba91fc25c0977741dd0e267849e6234b037ed0a/pstack/docs/harness/claude-code.md
[watch]: https://github.com/cursor/plugins/blob/1ba91fc25c0977741dd0e267849e6234b037ed0a/pstack/skills/poteto-mode/scripts/watch-pr/watch-pr
[bootstrap]: https://github.com/cursor/plugins/blob/1ba91fc25c0977741dd0e267849e6234b037ed0a/pstack/skills/poteto-mode/scripts/bootstrap.ts
[tool-package]: https://github.com/cursor/plugins/blob/1ba91fc25c0977741dd0e267849e6234b037ed0a/pstack/skills/poteto-mode/scripts/package.json
[cleanup]: https://github.com/cursor/plugins/blob/1ba91fc25c0977741dd0e267849e6234b037ed0a/pstack/skills/poteto-mode/playbooks/worktree-cleanup.md
[audit]: https://github.com/cursor/plugins/blob/1ba91fc25c0977741dd0e267849e6234b037ed0a/pstack/skills/poteto-mode/scripts/worktree-audit.sh
[state-launch]: https://github.com/cursor/plugins/blob/1ba91fc25c0977741dd0e267849e6234b037ed0a/pstack/skills/statechart/scripts/statechart
[state-run]: https://github.com/cursor/plugins/blob/1ba91fc25c0977741dd0e267849e6234b037ed0a/pstack/skills/statechart/scripts/run.ts
[state-render]: https://github.com/cursor/plugins/blob/1ba91fc25c0977741dd0e267849e6234b037ed0a/pstack/skills/statechart/references/render.md
[blast]: https://github.com/cursor/plugins/blob/1ba91fc25c0977741dd0e267849e6234b037ed0a/pstack/skills/blast-radius/SKILL.md
[architect]: https://github.com/cursor/plugins/blob/1ba91fc25c0977741dd0e267849e6234b037ed0a/pstack/skills/architect/SKILL.md
[how]: https://github.com/cursor/plugins/blob/1ba91fc25c0977741dd0e267849e6234b037ed0a/pstack/skills/how/SKILL.md
[why]: https://github.com/cursor/plugins/blob/1ba91fc25c0977741dd0e267849e6234b037ed0a/pstack/skills/why/SKILL.md
[proof]: https://github.com/cursor/plugins/blob/1ba91fc25c0977741dd0e267849e6234b037ed0a/pstack/skills/principle-prove-it-works/SKILL.md
[benchmark]: https://github.com/cursor/plugins/blob/1ba91fc25c0977741dd0e267849e6234b037ed0a/pstack/skills/benchmark-checklist/SKILL.md
[formal]: https://github.com/cursor/plugins/blob/1ba91fc25c0977741dd0e267849e6234b037ed0a/pstack/skills/create-formal-verification-skill/SKILL.md
[ci]: https://github.com/cursor/plugins/blob/1ba91fc25c0977741dd0e267849e6234b037ed0a/.github/workflows/validate-plugins.yml
