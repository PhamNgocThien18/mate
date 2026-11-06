# Kênh nhắn tin giữa các phiên Claude Code — 2026-11-06

Task: [CON-235](https://linear.app/controlplane/issue/CON-235). Phạm vi: chỉ đọc code và thử nghiệm trên workspace tạm dưới `/tmp/con235-*`, không sửa code mate, không đụng workspace thật. Mọi kết luận có lệnh và output kèm theo; chỗ nào chưa chạy thử thì ghi rõ "chưa thử".

Môi trường: macOS, Claude Code CLI `2.1.311` (`/opt/homebrew/bin/claude`), Claude Desktop chạy bản `2.1.317` bên trong app. mate ở `origin/main` `ff984b0`. Token tiêu cho thử nghiệm: 1,1M (haiku; 96% là cache read), đo từ transcript của 4 phiên thử.

## Kết luận ngắn

1. **Kênh là một giao thức đơn giản, process thường gửi và nhận được.** Mỗi phiên ghi `~/.claude/sessions/<pid>.json` (tên, cwd, sessionId, trạng thái) và lắng nghe `/tmp/cc-socks/<pid>.sock`. Tin là JSON một dòng. Một script Python 60 dòng vừa **lên được `ListAgents`** của mọi phiên Claude trên máy (tên `mate-core-probe`), vừa **nhận được** tin `SendMessage`, vừa **gửi được** tin tới crew và được giao ngay. Core của mate làm được cả ba việc này.
2. **Tin không mất.** Crew rảnh: giao ngay, trả lời sau 4 giây. Crew đang chạy tool: Claude giữ trong hàng đợi của nó, chèn vào ngay sau `tool_result` kế tiếp (probe 3: gửi 10:21:33, tool xong 10:21:52, trả lời 10:21:55). Phiên `claude -p` (Mate headless) cũng nhận giữa lượt như vậy.
3. **Điều kiện để giao ngay là khớp permission mode.** Người nhận chạy `--dangerously-skip-permissions` (crew và Mate của mate đều vậy) chỉ nhận ngay khi người gửi khai `from-mode="bypass"` trong phong bì; không khai thì tin bị **giữ** chờ người bấm duyệt, và **không hết hạn** (đã chờ 10 phút, biến `CLAUDE_CODE_USER_DIALOG_TIMEOUT_MS=40000` không tác dụng với prompt cục bộ). Lối thoát sạch: mate đã truyền `--settings <file>` cho crew Claude, chỉ cần thêm `"crossSessionInbound": "accept"` vào file đó là mọi tin đều giao ngay (probe 7 đã chứng minh).
4. **Mate headless không nhận được giữa hai lượt.** `claude -p` đăng ký socket lúc chạy và xoá khi thoát; giữa hai tin không có gì lắng nghe. Người nhận thay cho Mate phải là **core**: core đăng ký làm peer tên `mate` của workspace, nhận tin rồi đẩy vào `Host.Deliver`/news. Crew Claude trả lời Mate bằng `SendMessage` tới địa chỉ core.
5. **Ghi sổ được.** Tin đến nằm trong transcript của người nhận (bản ghi `queue-operation` có nguyên văn, và `origin.kind="peer"` với `msg_id`, `verifiedPeerPid`, `fromMode` khi giao lúc rảnh). Tin đi thì core tự ghi vì chính core gửi. Token tính như mọi lượt khác.
6. **Phiên Claude Desktop cũng ở trong kênh này**, với `cwd` là thư mục project và tên là tiêu đề cuộc chat. Đó là cách mate "nhìn được phiên nào cùng một project" mà captain hỏi: chỉ cần đọc thư mục `~/.claude/sessions/`.

Đề xuất: **dùng**, ở hai chỗ, và chỉ cho harness Claude: (a) core làm peer nhận tin từ crew Claude và từ phiên Desktop; (b) một adapter giao tin cho crew Claude qua socket thay cho gõ phím, sau seam runtime, Codex/pi/grok vẫn gõ vào pane. Chi tiết và danh sách issue dự kiến ở cuối.

## Cơ chế (đọc từ binary và kiểm chứng bằng thử nghiệm)

Tài liệu công khai: <https://code.claude.com/docs/en/cross-session-messaging.md> mô tả `ListAgents`, `SendMessage`, thư mục socket `/tmp/cc-socks-<uid>` (máy này dùng `/tmp/cc-socks`, binary chấp nhận cả hai), setting `crossSessionInbound` (`accept|hold|refuse`), `notify_when_idle`, và nói `claude -p` có lắng nghe nhưng không gửi. Tài liệu **không** mô tả giao thức socket, không nói process ngoài Claude có gửi được không, không nhắc Claude Desktop. Phần dưới là điều binary làm thật.

### Sổ đăng ký phiên

`~/.claude/sessions/<pid>.json` (mode 644), ví dụ phiên này:

```json
{"pid":23590,"sessionId":"b3f85f00-…","cwd":"/Users/erics/app/mate","startedAt":1793934539053,
 "procStart":"Fri Nov  6 03:08:58 2026","version":"2.1.311","peerProtocol":1,
 "peerFeatures":["notify_idle","reply_across_default_dirs","artifact_yield"],
 "kind":"interactive","entrypoint":"cli","pidDomain":"darwin",
 "messagingSocketPath":"/tmp/cc-socks/23590.sock","name":"mate-39","nameSource":"derived",
 "status":"busy","updatedAt":1793934652857}
```

Kèm `<pid>.<sha256>.key` (mode 600): `{"peerToken":"<32 hex>","procStart":…,"pidDomain":"darwin"}`.

- `entrypoint`: `cli` (terminal), `claude-desktop` (Code tab của Claude Desktop, có thêm `hostSessionId: local_…`), `sdk-cli` (`claude -p`).
- `name`: `nameSource: derived` là `<tên thư mục cwd>-<2 hex>` (`con235-a-05`, `mihuv2-d1`); `--name crew-con235-c` cho `nameSource: user`. Phiên Desktop lấy tiêu đề chat làm tên (`"Mate architecture review"`, `"Phần trang trí mới - Feedback tiếp theo"`).
- `status`: `idle`, `busy`, `waiting` (đang có dialog chờ người), `shell` (có shell nền).
- Kiểm tra sống: `ListAgents` chạy `LC_ALL=C TZ=UTC ps -o lstart= -p <pid>` và so với `procStart`. Phiên giả chỉ hiện ra sau khi ghi đúng chuỗi này (lần đầu dùng `time.ctime()` giờ địa phương thì bị bỏ qua).

### Socket và giao thức

JSON mỗi dòng một đối tượng. Dòng đầu tuỳ chọn là xác thực; macOS không bắt buộc (binary: "peers will send unauthenticated (accepted: auth is optional on this platform)"), Windows bắt buộc. Chính binary in mẫu lệnh này vào log debug:

```sh
{ echo '{"type":"auth","token":"'"$CLAUDE_CODE_MESSAGING_TOKEN"'"}'
  echo '{"type":"user","message":{"role":"user","content":"hello"}}'; } | socat - UNIX-CONNECT:/tmp/cc-socks/<pid>.sock
```

Tin `SendMessage` thật tới peer giả (`/tmp/con235-core.log`):

```json
{"msgV":1,"msg_id":"f3026b4e-…","type":"user","priority":"next","from":"uds:/tmp/cc-socks/23590.sock",
 "message":{"role":"user","content":"<cross-session-message from=\"uds:/tmp/cc-socks/23590.sock\" from-name=\"mate-39\" from-mode=\"bypass\">\nCON-235 probe 4: …\n</cross-session-message>"}}
```

Người nhận lấy pid người gửi từ socket (`getPeerPid`, hiện "verified pid N" trong dialog), tra `~/.claude/sessions/<pid>.json` để có tên, và đọc `from-mode` trong phong bì để quyết định giao hay giữ. Mode không nằm trong sổ đăng ký; người gửi tự khai.

`CLAUDE_CODE_MESSAGING_SOCKET` và `CLAUDE_CODE_MESSAGING_TOKEN` được xuất cho hook và Bash của phiên. `internal/harness/nested_env.go` của mate đã gỡ hai biến này khỏi env của Herdr và crew, nên crew không nhầm socket của phiên cha; việc này không liên quan tới socket của chính crew.

## Trả lời sáu câu hỏi

### 1. Crew do mate bật có hiện trong `ListAgents` không, map về crew thế nào

Có. Crew Claude là một phiên `claude` tương tác thường, nên nó tự đăng ký như mọi phiên. Tên là `derived` từ thư mục worktree, nên `mihuv2-hu-427-split-plan-0a` trong issue chính là crew ở worktree `…/mihuv2-hu-427-split-plan`. Tên agent Herdr `crew-<id>` không xuất hiện ở đây.

Map chắc chắn bằng `sessionId`: mate truyền `--session-id <uuid>` cho crew (`internal/harness/claude/claude.go:446`) và ghi vào `.meta` (`session_id=…`, ví dụ `/Users/erics/workspace/.mate/projects/videofactory/crews/vid-19-props.meta`). Thử `claude -p --session-id 11111111-2222-4333-8444-555555555555` thì sổ đăng ký ghi đúng `sessionId` đó. Map phụ bằng `cwd` == đường dẫn worktree. Nếu muốn tên đẹp, mate có thể thêm `--name crew-<id>` (đã thử, ra `nameSource: user`); biến `CLAUDE_CODE_SESSION_NAME` cũng có trong binary nhưng chưa thử.

### 2. Tin có tới chắc không

Bốn phiên thử: A (bypass, như crew mate), C (`--name crew-con235-c --settings '{"crossSessionInbound":"accept"}'`, mode default), D (mode default, không setting), E (`claude -p`, bypass).

| Probe | Người gửi | Người nhận | Kết quả |
| -- | -- | -- | -- |
| 1 | `SendMessage` từ phiên này (bypass) | A rảnh | Giao ngay. Transcript: `user` record `origin.kind=peer` 03:16:23, trả lời `PROBE1-RECEIVED` 03:16:27 |
| 2 | `nc -U` từ shell, có token, không khai mode | A rảnh | **Giữ**: "The sender did not attest its permission mode and this session bypasses prompts". Dialog Deny/Deliver. Không hết hạn |
| 3 | `SendMessage` | A đang chạy Bash 40 giây | Enqueue 03:21:29, `remove reason=absorbed_mid_turn` 03:21:52 đúng lúc tool trả về, chèn vào `tool_result` dưới dạng `<system-reminder>`, trả lời 03:21:55 |
| 4 | `SendMessage` | peer giả `mate-core-probe` (Python) | Nhận nguyên JSON ở trên |
| 5 | Python tự đăng ký peer `mate-core-sender`, khai `from-mode="bypass"` | A rảnh | **Giao ngay**, A trả lời `PROBE5-RECEIVED`; không dialog |
| 6 | `SendMessage` (bypass) | C (default + accept) | Giao ngay, `PROBE6-RECEIVED` |
| 7 | `nc -U` không khai mode | C (default + accept) | Giao ngay, `PROBE7-RECEIVED` |
| 8 | `SendMessage` (bypass) | D (default, không accept) | **Giữ** "mode-mismatch", còn nguyên sau 10 phút dù `CLAUDE_CODE_USER_DIALOG_TIMEOUT_MS=40000` |
| 9 | `SendMessage` | E `claude -p` đang chạy Bash 45 giây | Nhận giữa lượt như probe 3, in nguyên văn ra stdout |

Hồi báo cho người gửi là phiên Claude: probe 8 sinh hai `[Cross-session delivery notice]` về phiên này, một khi tin bị giữ ("held … NOT delivered: its Claude has not seen it … a Claude Desktop or non-interactive session cannot ask, so there the hold expires undelivered") và một khi phiên D bị tắt sau hơn 10 phút ("not approved before expiry"). Tức là chỉ việc tắt phiên mới sinh "expiry"; chừng nào phiên còn sống thì tin còn treo. Process thường gửi không có kênh nhận hai thông báo này.

Trường hợp "crew đang chờ captain duyệt quyền" chưa thử trực tiếp (crew của mate chạy bypass nên không có dialog quyền). Trạng thái `waiting` do dialog giữ tin (probe 2) thì tin kế tiếp vẫn vào hàng đợi; A sau khi Deny vẫn nhận probe 3 bình thường.

Luật giữ tin (chuỗi trong binary, khớp thử nghiệm): `mode-mismatch` (lớp mode hai bên khác), `no-mode-asserted` (người gửi không khai và người nhận bypass), `explicit-setting` (`crossSessionInbound: hold`), `mode-unknown` (đang khởi động, sẽ giao sau). Setting `accept` ở file truyền qua `--settings` bỏ hết các giữ này; setting trong repo chỉ được siết chặt, không nới.

### 3. Gửi từ đâu

Process không phải Claude gửi được (probe 2, 5, 7). Để được giao ngay cho crew bypass, process đó phải (a) tự ghi `~/.claude/sessions/<pid>.json` + `.key` cho chính pid của nó, vì pid được xác minh qua socket, và (b) bọc nội dung trong `<cross-session-message from=… from-name=… from-mode="bypass">`. Hoặc đơn giản hơn: crew nhận với `crossSessionInbound: accept` thì khỏi cần khai (probe 7).

Giao thức không có tài liệu công khai và không cam kết ổn định (`peerProtocol: 1`, `msgV: 1` là chỗ để đổi). Mọi thứ ở trên đọc từ chuỗi trong binary 2.1.311. Nếu mate dùng, phải có test live đóng dấu phiên bản và đường lùi về gõ phím.

### 4. Chiều ngược lại, khi Mate headless

Crew gửi tới Mate bằng `SendMessage` được (A đã tự trả lời phiên này qua `to: "uds:/tmp/cc-socks/23590.sock"`). Nhưng Mate headless là `claude -p` mỗi tin: phiên E đăng ký lúc chạy, nhận được giữa lượt (probe 9), rồi **xoá sổ đăng ký và socket khi thoát**. Giữa hai lượt không ai nghe, `ListAgents` cũng không thấy Mate.

Cách đúng là core nhận thay: core ghi sổ đăng ký với tên cố định (ví dụ `mate` hoặc `mate-<workspace>`), nghe socket, nhận tin rồi gọi `Host.Deliver` (`internal/matehost/host.go:266`) hoặc đẩy vào news/backlog theo luật hiện có. Peer giả `mate-core-probe` đã chứng minh phần nhận. Phần "trả lời về địa chỉ `from`" của Claude khi đó trỏ tới socket của core, đúng ý.

### 5. Ghi sổ

Phía nhận (transcript crew, mate đã đọc transcript Claude ở `internal/harness/claude/transcript.go`):

- Giao lúc rảnh: bản ghi `user` với `origin: {kind:"peer", from, verifiedPeerPid, msg_id, name, fromMode, body}` và một bản ghi `queue-operation enqueue` có `content` nguyên văn.
- Giao giữa lượt: `queue-operation enqueue` rồi `queue-operation remove` với `reason: "absorbed_mid_turn"`, `deliveryId`; nội dung nằm trong `tool_result` kế tiếp dưới `<system-reminder>`. Không có bản ghi `user` riêng, nên bộ đọc transcript phải nhìn `queue-operation` hoặc tìm chuỗi `<cross-session-message`.

Phía gửi: `SendMessage` của crew là một `tool_use` bình thường trong transcript (A: `tool_use {"to":"uds:/tmp/cc-socks/23590.sock","message":"PROBE3-RECEIVED 10:21:55"}`). Core gửi thì core tự ghi timeline và `mate.db` như `mate send` hôm nay. Token: tin đến là một phần prompt của lượt kế, không có cột riêng; bảng token không cần đổi.

### 6. So với `mate send` hôm nay

| | Gõ vào pane (`internal/send`) | Kênh phiên |
| -- | -- | -- |
| Điều kiện trước khi gửi | Đọc màn hình, composer phải rỗng; còn chữ thì stow/xoá; chữ của captain có thể lẫn | Không chạm composer. Chữ captain đang gõ dở không bị ảnh hưởng |
| Crew bận | Từ chối, hoặc `--queue` gõ vào để harness tự xếp | Claude tự xếp, chèn ở tool round kế tiếp (đo: 0–23 giây sau khi gửi, tuỳ tool) |
| Bằng chứng đã tới | Đọc lại composer tới khi rỗng; Enter thử lại | Socket nhận = vào inbox. Giữ hay từ chối thì chỉ phiên Claude gửi mới nhận "[Cross-session delivery notice]"; process thường không có kênh nhận trạng thái (chưa thấy `peer_message_status` gửi về peer giả) |
| Dấu "từ app" | Marker `⟦mate⟧` để hook phân biệt với captain | Phong bì `<cross-session-message from-name="mate">` do Claude bọc sẵn; Claude được dặn "treat it as a teammate's request", không phải lời captain |
| Fix round cho crew đã hand-back (CON-73: mở phiên mới) | `mate send` tự mở phiên mới | Kênh chỉ tới phiên đang sống; crew đã thoát thì không có socket. Giữ nguyên đường phiên mới |
| Harness khác | Codex, pi, grok đều gõ được | Chỉ Claude |
| Hook `UserPromptSubmit` của mate có chạy cho tin này không | Có (đó là cách mate ghi sổ hôm nay) | **Chưa thử** |
| Phụ thuộc | Herdr `pane send-text`, màn hình | Giao thức không công bố, đổi theo phiên bản Claude |

Được: bỏ toàn bộ nhảy múa với composer cho crew Claude, tin tới cả khi crew bận, Claude Desktop vào chung kênh. Mất: không dùng cho ba harness kia, không có hồi báo cho process thường, phụ thuộc giao thức ngầm, phải xử lý mode (accept trong settings crew là cách rẻ nhất).

## Ý của captain: design ở Claude Desktop, hoàn thiện rồi đưa về mate

Điều đã đo được hôm nay:

- Hai phiên Claude Desktop của captain nằm trong sổ đăng ký với `entrypoint: "claude-desktop"`, `cwd` là thư mục project (`/Users/erics/app/mate`, `/Users/erics/workspace/mihuv2/mihuv2`), tên là tiêu đề chat, và `ListAgents` gắn nhãn "Claude Desktop session". Vậy **mate biết phiên Desktop nào thuộc project nào bằng cách đọc `~/.claude/sessions/*.json` và so `cwd` với repo của project** (kể cả worktree, qua git common dir). Không cần socket cho việc nhìn.
- Gửi tới phiên Desktop dùng cùng socket (`/tmp/cc-socks/1720.sock` thuộc process `claude.app/Contents/MacOS/claude`). Chưa gửi thử để không chen vào chat đang mở của captain.
- **Chưa thử chiều Desktop gửi ra ngoài.** Đây là mắt xích captain cần kiểm trong một phút, peer giả vẫn đang chạy: trong chat Desktop bất kỳ, gõ "gửi tin cho phiên `mate-core-probe`: xin chào", rồi `cat /tmp/con235-core.log`. Nếu có dòng `LINE {…}` thì đường Desktop → core đã thông. Tắt peer giả bằng `pkill -f fakepeer.py`.

Đường đi đề xuất, theo thứ tự rẻ tới đắt:

1. **Văn bản/tin nhắn.** Captain hoàn thiện design trong Desktop rồi bảo Claude ở đó "giao cho mate": phiên Desktop `SendMessage` tới peer `mate` của core kèm đường dẫn file hoặc URL artifact. Core nhận, ghi timeline, mở news hoặc `Deliver` cho Mate. Cần: core làm peer (issue A dưới) và mắt xích Desktop → peer được xác nhận.
2. **File trong repo.** Phiên Desktop đã trust thư mục project (`localAgentModeTrustedFolders` trong `claude_desktop_config.json`), nên design dạng HTML/markdown ghi thẳng vào `docs/design/` của repo là đường bền nhất: Mate và crew đọc như mọi file, không phụ thuộc kênh nào. Tin nhắn ở bước 1 chỉ cần nói "xong, xem `docs/design/x.html`".
3. **Artifact claude.ai.** Tool `Artifact` của Claude Code publish trang lên `claude.ai/artifact/<id>`, `action: read` kéo HTML về, và phiên publish được "watch": khi có bản mới từ nơi khác (Desktop hay claude.ai), lần gọi Artifact kế tiếp báo "a newer version was published". Nhưng watch chỉ sống trong phiên dài; Mate headless mỗi tin một `claude -p` không giữ được watch, và Mate hiện bị giới hạn `--tools Bash,Read,Write,Edit,Glob,Grep,Skill` (không có `Artifact`). Khả thi nếu crew design là phiên tương tác dài và được bật `Artifact`; chưa thử vòng Desktop sửa → phiên terminal nhận, tài liệu cũng chưa mô tả vòng này.

Nên bắt đầu từ 1 + 2. Artifact là lớp thứ ba, làm sau khi captain đã dùng thử Desktop sửa design trên file trong repo.

## Đề xuất và danh sách issue dự kiến (chưa tạo trên Linear)

Dùng kênh, giới hạn ở harness Claude, giữ nguyên trục core: `core.sock` vẫn là cửa duy nhất cho vỏ UI, kênh phiên chỉ là chặng cuối giao tin cho crew Claude và cửa nhận từ phiên ngoài.

| # | Việc | Phụ thuộc | Cỡ |
| -- | -- | -- | -- |
| A | Core đăng ký làm peer: ghi `~/.claude/sessions/<pid>.json` + `.key`, nghe socket, nhận `<cross-session-message>`, ghi timeline, đẩy vào `Host.Deliver`/news. Gỡ đăng ký khi tắt. Test live đóng dấu phiên bản Claude | CON-203 xong (core là process riêng) | 1–2 ngày |
| B | Adapter giao tin cho crew Claude sau seam runtime: gửi qua socket khi sổ đăng ký có `sessionId` của crew, khai `from-mode="bypass"`, ghi `crossSessionInbound: accept` vào file `--settings` của crew; không có socket thì lùi về gõ pane. Codex/pi/grok không đổi | A | 2 ngày |
| C | Mate biết địa chỉ trả lời: brief và AGENTS của Mate/crew nêu peer `mate` thay cho `mate news`/`mate send` ở các chỗ phù hợp; crew hand-back vẫn đi đường cũ | A, B | 0,5 ngày |
| D | Office/City/console hiện "phiên ngoài cùng project" từ `~/.claude/sessions` (Desktop, terminal của captain), chỉ xem | — | 1 ngày |
| E | Bộ đọc transcript nhận diện `queue-operation` và `origin.kind=peer`, ghi vào timeline/`mate.db` như một "tin tới crew" | — | 0,5 ngày |
| F | Thiết kế đường design → mate (issue thiết kế riêng theo phạm vi CON-235): bước 1 + 2 ở trên, sau khi captain xác nhận Desktop → peer | A, kết quả thử của captain | — |

Câu hỏi còn mở trước khi làm B: hook `UserPromptSubmit` của mate có chạy cho tin peer không (nếu không, E thay cho hook ở chỗ ghi sổ); `peer_message_status` có gửi ngược về socket người gửi không (nếu có, core có hồi báo giữ/từ chối).

## Dấu vết thử nghiệm

Công cụ ở `/tmp/con235-tools/`: `ptyrun.py` (chạy `claude` trong pty, gõ phím qua FIFO), `clean-claude.sh` (gỡ `NestedSessionEnv` rồi chạy `claude`), `peersend.sh` (gửi thô bằng `nc -U` với token), `peersend2.py` (tự đăng ký peer rồi gửi phong bì có `from-mode`), `fakepeer.py` (peer giả, log vào `/tmp/con235-core.log`). Màn hình các phiên thử ở `/tmp/con235-{a,c,d}.log`, transcript ở `~/.claude/projects/-private-tmp-con235-*/`. Các phiên thử đã tắt; chỉ `fakepeer.py` còn chạy cho captain thử chiều Desktop.

Lưu ý khi tự gõ vào pty: sau khi Claude bật kitty keyboard protocol, Enter phải gửi là `\x1b[13u`; `\r` chỉ dùng được cho dialog trước đó. Không liên quan tới Herdr (`pane send-text` của Herdr tự lo), ghi lại để khỏi mất thời gian lần sau.
