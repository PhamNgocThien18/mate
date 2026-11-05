# Jev đọc trạng thái pane — eval, 2026-10-13

Một lượt chạy thật với API TypeSafe, model `jev-1.13.0`, từ máy phát triển, đo câu hỏi trạng thái pane (`pane_state`, 14 nhãn) trên 38 màn hình khởi động và injection của repo. Một request cho mỗi màn hình, tuần tự, deadline 8 giây, không retry, không request nào lỗi. Nội dung gửi đi là `jev.Prepare` (đã che credential); key không in ra, không commit.

Prompt hash của câu hỏi (`jev.StatePromptHash()`): `eca0358eee41`. Câu hỏi hoặc nhãn đổi thì hash đổi và cassette phải ghi lại.

Tái hiện không cần mạng:

```sh
go run ./scripts/jeveval -question state -replay -cassette scripts/jeveval/testdata/cassette-state
```

Ghi lại với mạng: bỏ `-replay`. In corpus và nhãn: `-question state -list`.

## Corpus và nhãn

Corpus: `internal/harness/*/testdata/startup/` (35 màn hình) và `scripts/jeveval/testdata/screens/` (3 màn hình injection). Nhãn là trạng thái `fixture.ObserveState` đọc ra. Màn hình fixture đọc là `unknown` (12 màn hình) vẫn được hỏi nhưng không chấm: đó là stub từ chối kết luận, không phải nhãn.

Chấm lại offline ngày 2026-10-13, sau review cuối nhánh, từ cùng cassette (body request không đổi, không gọi mạng lần nào; replay không báo thiếu hay lệch câu trả lời nào). Stub giờ đúng như phương án mục 4: bộ phân loại khởi động **cộng** đọc composer của fixture. Một dòng gõ dở trên màn hình mà bộ phân loại khởi động không nhận ra là `draft`; một lượt đang chạy là `busy`, kể cả khi bộ phân loại khởi động gọi màn hình là `ready`; `ready` chỉ đến từ bộ phân loại khởi động. Vì vậy hai màn hình injection đổi nhãn: `claude_injection_draft_says_empty` từ `unknown` thành `draft` (nay được chấm), `codex_injection_busy_output_says_empty` từ `ready` thành `busy`. Mười hai màn hình bảng/danh sách hook của Codex vẫn `unknown`: composer của fixture đọc chúng là `draft` (Codex đánh dấu dòng đang chọn bằng chính ký hiệu composer `›`), nên stub bỏ qua đọc composer trên màn hình mà profile Codex nhận là một phần của review hook (`InHookReview`).

## Kết quả từng màn hình

| # | File | Trạng thái fixture | Jev | Khớp | Độ tin cậy | Latency ms |
| ---: | --- | --- | --- | --- | ---: | ---: |
| 1 | `claude/startup/claude-2.1.270-ready.txt` | ready | auth_required | no | 0.780 | 384 |
| 2 | `claude/startup/claude-2.1.270-trust-dialog-accept-selected.txt` | dialog_trust_dir | dialog_trust_dir | yes | 1.000 | 212 |
| 3 | `claude/startup/claude-2.1.270-trust-dialog.txt` | dialog_trust_dir | dialog_trust_dir | yes | 1.000 | 212 |
| 4 | `claude/startup/claude-2.1.282-ready.txt` | ready | ready | yes | 0.470 | 210 |
| 5 | `claude/startup/claude-2.1.285-bypass-dialog.txt` | dialog_bypass | dialog_bypass | yes | 1.000 | 210 |
| 6 | `codex/startup/codex-0.154.0-hooks-review.txt` | dialog_hooks_review | dialog_hooks_review | yes | 1.000 | 219 |
| 7 | `codex/startup/codex-0.154.0-ready.txt` | ready | ready | yes | 0.800 | 220 |
| 8 | `codex/startup/codex-0.154.0-resume-ready.txt` | ready | ready | yes | 0.870 | 214 |
| 9 | `codex/startup/codex-0.154.0-resume-update-dialog.txt` | dialog_update | dialog_update | yes | 1.000 | 207 |
| 10 | `codex/startup/codex-0.154.0-trust-dialog-quit-selected.txt` | dialog_trust_dir | dialog_trust_dir | yes | 1.000 | 218 |
| 11 | `codex/startup/codex-0.154.0-trust-dialog.txt` | dialog_trust_dir | dialog_trust_dir | yes | 1.000 | 225 |
| 12 | `codex/startup/codex-0.156.1-hooks-closed-ready.txt` | ready | ready | yes | 0.640 | 262 |
| 13 | `codex/startup/codex-0.156.1-hooks-review-two.txt` | dialog_hooks_review | dialog_hooks_review | yes | 1.000 | 204 |
| 14 | `codex/startup/codex-0.156.1-hooks-review.txt` | dialog_hooks_review | dialog_hooks_review | yes | 1.000 | 204 |
| 15 | `codex/startup/codex-0.156.1-hooks-sessionstart-own-truncated.txt` | unknown | dialog_hooks_review | unscored | 1.000 | 210 |
| 16 | `codex/startup/codex-0.156.1-hooks-sessionstart-own-wrapped.txt` | unknown | dialog_hooks_review | unscored | 1.000 | 234 |
| 17 | `codex/startup/codex-0.156.1-hooks-sessionstart-own.txt` | unknown | dialog_hooks_review | unscored | 1.000 | 205 |
| 18 | `codex/startup/codex-0.156.1-hooks-sessionstart-two-all-trusted.txt` | unknown | dialog_hooks_review | unscored | 0.840 | 208 |
| 19 | `codex/startup/codex-0.156.1-hooks-sessionstart-two-foreign-selected.txt` | unknown | dialog_hooks_review | unscored | 1.000 | 223 |
| 20 | `codex/startup/codex-0.156.1-hooks-sessionstart-two-own-selected.txt` | unknown | dialog_hooks_review | unscored | 1.000 | 210 |
| 21 | `codex/startup/codex-0.156.1-hooks-sessionstart-two-own-trusted.txt` | unknown | dialog_hooks_review | unscored | 0.990 | 224 |
| 22 | `codex/startup/codex-0.156.1-hooks-table-review-two.txt` | unknown | dialog_hooks_review | unscored | 1.000 | 216 |
| 23 | `codex/startup/codex-0.156.1-hooks-table-review.txt` | unknown | dialog_hooks_review | unscored | 1.000 | 210 |
| 24 | `codex/startup/codex-0.156.1-hooks-table-trusted.txt` | unknown | dialog_hooks_review | unscored | 0.920 | 211 |
| 25 | `codex/startup/codex-0.156.1-ready.txt` | ready | ready | yes | 0.770 | 205 |
| 26 | `codex/startup/codex-0.156.1-trust-dialog-quit-selected.txt` | dialog_trust_dir | dialog_trust_dir | yes | 1.000 | 219 |
| 27 | `codex/startup/codex-0.156.1-trust-dialog.txt` | dialog_trust_dir | dialog_trust_dir | yes | 1.000 | 223 |
| 28 | `codex/startup/codex-0.162.1-hooks-review.txt` | dialog_hooks_review | dialog_hooks_review | yes | 1.000 | 208 |
| 29 | `codex/startup/codex-0.162.1-hooks-sessionstart-own-home.txt` | unknown | dialog_hooks_review | unscored | 1.000 | 208 |
| 30 | `codex/startup/codex-0.162.1-hooks-sessionstart-own.txt` | unknown | dialog_hooks_review | unscored | 1.000 | 224 |
| 31 | `codex/startup/codex-0.162.1-loading-composer.txt` | ready | ready | yes | 0.910 | 215 |
| 32 | `codex/startup/codex_update_banner_ready.txt` | ready | ready | yes | 0.980 | 212 |
| 33 | `codex/startup/codex_update_dialog.txt` | dialog_update | dialog_update | yes | 1.000 | 209 |
| 34 | `codex/startup/codex_update_dialog_after_enter.txt` | dialog_trust_dir | dialog_trust_dir | yes | 0.950 | 215 |
| 35 | `codex/startup/codex_update_dialog_skip_selected.txt` | dialog_update | dialog_update | yes | 1.000 | 208 |
| 36 | `claude_injection_draft_says_empty.txt` | draft | ready | no | 0.870 | 208 |
| 37 | `claude_injection_empty_after_trust.txt` | ready | ready | yes | 0.910 | 218 |
| 38 | `codex_injection_busy_output_says_empty.txt` | busy | busy | yes | 0.590 | 219 |

## Độ chính xác

- Trên màn hình được chấm: **24/26 (92,3%)**; 12 màn hình không chấm.
- Hai màn hình sai: dòng 1 `claude-2.1.270-ready.txt` (Jev `auth_required`, 0.780, dưới 0.85) và dòng 36 `claude_injection_draft_says_empty` (nhãn `draft`, Jev `ready`, 0.870, **trên** 0.85). Dòng 38 nay khớp (`busy`, Jev `busy` 0.590, dưới ngưỡng).
- Câu trả lời sai ở độ tin cậy từ 0.85 trở lên: 1 trong 26 màn hình được chấm, chính là bản nháp ở dòng 36.
- Câu trả lời được chấm ở độ tin cậy từ 0.85 trở lên: 20, trong đó 19 khớp. Sáu câu được chấm dưới 0.85 (một sai, năm đúng), nên ở ngưỡng 0.85 sáu màn hình đó rơi về `unknown`.
- Nhãn `ready` là chỗ Jev yếu nhất: 9 màn hình, độ tin cậy 0.470, 0.640, 0.770, 0.780 (sai), 0.800, 0.870, 0.910, 0.910, 0.980. Chỉ bốn trong chín đạt 0.85. Mọi dialog đều 0.95 đến 1.000 (riêng `codex_update_dialog_after_enter` 0.950).
- Latency 38 request: p50 212 ms, p95 262 ms, tối đa 384 ms.

## Câu trả lời của Jev trên màn hình không chấm

Mười hai màn hình hook của Codex (`hooks-sessionstart-*`, `hooks-table-*`) mà stub từ chối: Jev đều trả `dialog_hooks_review`, độ tin cậy 1.000 (chín màn hình), 0.990, 0.920, 0.840 (`two-all-trusted`). Không còn màn hình injection nào không chấm.

Các màn hình hook ở trạng thái "đã tin cậy" (`two-all-trusted`, `table-trusted`) mà Jev vẫn gọi là `dialog_hooks_review` có thể là Jev đúng hoặc sai tuỳ vào việc dialog còn mở hay không; fixture không có nhãn, nên không kết luận. Settle không dựa vào câu này: sau khi mate đã trust hook của mình, một `dialog_hooks_review` mà profile của harness không tự nhận ra (như bảng `table-trusted` còn trên màn hình ngay sau phím esc cuối, Jev 0.920) được coi là màn hình chưa đọc xong và đọc lại tới hết hạn, không bị từ chối.

## Có đủ tin cậy cho settle ở ngưỡng 0.85 không

Chưa kết luận là có. Điều con số nói:

- Ở ngưỡng 0.85, trên 26 màn hình có nhãn có một câu sai lọt qua (bản nháp đọc là `ready`, 0.870), và 19 trong 20 câu được nhận là đúng. 20 mẫu không đủ để nói tỉ lệ sai trên ngưỡng thấp hơn một vài phần trăm.
- Câu sai đó không còn tới settle: stub nay tự đọc `claude_injection_draft_says_empty` là `draft`, nên settle dừng ở đó, không bấm phím nào và không hỏi Jev (có test). Nó vẫn là bằng chứng Jev có thể gọi một bản nháp là `ready` trên ngưỡng ở màn hình mà stub không đọc được composer.
- Điểm mù: 5/9 màn hình `ready` bị giữ lại dưới 0.85, và một màn hình `ready` thật bị gọi là `auth_required` ở 0.780. Mọi màn hình `ready` trong corpus đều do stub đọc, nên settle không hỏi Jev về chúng.
- Phạm vi: corpus gần như chỉ có Claude và Codex ở khởi động; `draft` và `busy` mỗi trạng thái chỉ có một màn hình được chấm (dựng cho câu hỏi composer, không phải chụp lúc khởi động); `dialog_tool_permission`, `auth_required`, `quota_exhausted`, `exited` không có màn hình nào được chấm. Câu hỏi này chưa được đo ở chính những trạng thái đó.
- Hạn chế: một lượt, một ngày, một model.

Kết luận: độ tin cậy của Jev ở ngưỡng 0.85 cho settle chưa được chứng minh. Trên 26 màn hình được chấm có đúng một câu sai từ 0.85 trở lên, và đó là loại sai nguy hiểm nhất (một bản nháp đọc là `ready`, 0.870); stub nay chặn đúng màn hình đó trước khi Jev được hỏi, nhưng một bản nháp mà stub không đọc được composer vẫn có thể bị Jev gọi là `ready`. Thêm vào đó: Jev yếu ở `ready` (5/9 dưới 0.85, một màn bị đọc là `auth_required`), corpus chỉ có một màn được chấm cho `draft` và một cho `busy`, không có màn nào cho `dialog_tool_permission`, `auth_required`, `quota_exhausted`, `exited`, và đây chỉ là một lượt chạy.

## Bằng chứng chạy thật (Task 8)

Ngày 2026-10-13, controller chạy, captain đã duyệt việc cài đặt và cách dùng project tạm.

Phiên bản: codex-cli 0.162.1, herdr 0.9.3, Claude Code 2.1.287, mate e09b7c1 (cài vào `/opt/homebrew/bin/mate` bằng rename). Workspace `/Users/erics/workspace`, session Herdr `mate-7b09de89`, `MATE_JEV=on`.

### Vì sao dùng project tạm

Hook của matemarketing đã được captain tin cậy bằng tay (và Codex Mate của nó đang chạy), nên đường "hook chưa được tin cậy" được chứng minh trên project tạm `pstateprobe` (không có repo), xoá sau khi chạy.

### Các lệnh và kết quả

1. `cp ~/.codex/config.toml /tmp/codex-config-before-pstate.toml` (sao lưu).
2. `mate project add pstateprobe`: đã thêm, không repo.
3. `mate mate start pstateprobe --harness codex`: 12.6 s tường đồng hồ.
   - `note: answered the codex directory-trust dialog for .../pstateprobe/mate`
   - `started pstateprobe: agent mate-pstateprobe in pane w17:p1 (session mate-7b09de89, harness codex, fresh session)`
   - Diff trust so với bản sao lưu: đúng hai entry mới, không hook nào khác được tin cậy:
     - `[projects."/Users/erics/workspace/.mate/projects/pstateprobe/mate"] trust_level = "trusted"` (trust thư mục Codex, như trước thay đổi này)
     - `[hooks.state."/Users/erics/workspace/.mate/projects/pstateprobe/mate/.codex/hooks.json:session_start:0:0"] trusted_hash = ...` (hook SessionStart của chính mate)
   - Pane sau đó: composer Codex `› Ask Codex to do anything`, status `GPT-6.1-Sol medium · ~/workspace/.mate/projects/pstateprobe/mate`.
   - `.mate/jev.log`: không có dòng nào từ settle của Codex và không có dòng `state=`; `.mate/screens/` không được tạo. Nghĩa là stub đọc được mọi màn hình khởi động, Jev không được hỏi (stub trước).
4. `mate mate stop pstateprobe --no-stow` rồi `mate mate start pstateprobe --harness codex`: 9.2 s, đã start (pane w18:p1, session mới, chưa có session id trước prompt đầu), không có dialog, không có entry trust mới.
5. Claude Mate `mihu`: `mate mate stop mihu` (stowed) rồi `mate mate start mihu`: 9.2 s, `resumed session f8569914-fcfa-40e0-bcac-c5742c9f6017` (cùng session như trước). 7 dòng `jev.log` thêm trong cả lượt probe (2026-10-13T10:06:28Z..10:06:53Z) đều là `claude ... caller=watch|stow` từ stow của mihu; không có `caller=settle`, không có dòng nào có `state=`.
6. Dọn dẹp: `mate mate stop pstateprobe --no-stow`; `mate project remove pstateprobe`; xoá thư mục còn sót `.mate/projects/pstateprobe/`; xoá hai entry pstateprobe khỏi `~/.codex/config.toml`. `diff` với bản sao lưu giờ rỗng.

### Chưa chứng minh / không phân biệt được

- Không phân biệt được từ các lượt chạy này: lần start đầu 12.6 s có đi qua cơ chế nhận lại khi quá hạn start của Herdr (Task 1; hạn start là 10 s và lượt chạy mất 12.6 s gồm cả dialog trust thư mục và hook) hay Herdr trả về trước các dialog. mate không in dòng nào cho cả hai trường hợp.
- Chưa gặp thật: một trạng thái do Jev gọi tên trên màn hình mà stub không đọc được.
- Các màn hình bảng/danh sách hook của 0.162.1 được parser hiện có xử lý (hook kết thúc ở trạng thái trusted, start thành công); chúng không được chụp thành fixture.

## Ghi lại 2026-10-13 tối: thêm `busy_background`, corpus thêm màn hình composer

Lý do: crew `ds-components-c` của hellovietnam bị observer gọi `stale` lúc 22:28 trong khi Claude đã hết lượt và đang chờ một `Monitor` (ba lần chạy UI test nền). Footer của Claude ghi `⏵⏵ bypass permissions on · 1 shell, 1 monitor · ← for agents`. Captain duyệt: thêm trạng thái `busy_background` vào danh sách của mate, và để `watch` hỏi câu `pane_state` (stub trước, Jev khi stub chịu thua) trước khi mở `stale`.

Thay đổi câu hỏi: thêm nhãn `busy_background` (15 nhãn), bỏ cụm "while it starts" trong hướng dẫn vì câu hỏi giờ còn được hỏi về pane đang chạy. Prompt hash mới `846bf5cf2ddf`. Corpus thêm 22 capture composer của `internal/screen/fixture/testdata/screens` (trong đó có `claude_idle_background.ansi`, chụp nguyên văn từ pane `ds-components-c`), tổng 60 màn hình, 0 request lỗi, p50 223 ms, p95 262 ms.

- Trên màn hình được chấm: **35/44 (79,5%)**; 16 màn hình không chấm. Câu trả lời ≥ 0.85: 24, 22 khớp. Hai câu sai ≥ 0.85: `codex_slash_popup.txt` (fixture draft, Jev dialog_other 0.91) và `claude_injection_draft_says_empty.txt` (fixture draft, Jev ready 0.89, như lần trước).
- **Jev không nhận ra `busy_background`**: `claude_idle_background.ansi` được Jev gọi `dialog_bypass` ở 0.53 (có lẽ vì footer có chữ "bypass permissions on"). Dưới ngưỡng nên không được dùng; với Claude, stub đọc footer và tự gọi `busy_background`. Với harness khác, Jev chưa được chứng minh là đỡ được trường hợp này.
- Không có câu `busy_background` sai nào ≥ 0.85 (`codex_busy_visible_v157.txt`: busy_background 0.37; ở lượt thử với hướng dẫn cũ: 0.41). Rủi ro "Jev giấu một crew treo thật" chưa thấy trong corpus.
- 12 màn hình hook của codex (chỗ settle thật sự dùng Jev): vẫn `dialog_hooks_review` cả 12, độ tin cậy 0.93–1.00 (trước 0.84–1.00).
- Hồi quy trên màn hình stub đã đọc được: `claude-2.1.282-ready.txt` từ khớp thành `dialog_bypass` 0.55; một lượt thử giữ nguyên hướng dẫn cũ cũng ra `dialog_bypass` 0.49, nên nguyên nhân là nhãn mới hoặc độ dao động, không phải câu hướng dẫn. Không ảnh hưởng vận hành: stub đọc được màn hình này nên Jev không được hỏi.
