# Jev: đọc màn hình harness lạ

Jev (TypeSafe, `jev-1.13.0`) là chốt chặn cuối khi một harness đổi màn hình. Stub của harness (fixture) đọc mọi màn hình trước; màn hình stub đọc được không bao giờ hỏi Jev. Jev có hai đường, đều tắt mặc định:

- Action **Explain notice (Jev)** trong console, chỉ chạy khi người dùng chọn.
- Bộ quan sát pane (mục "Bộ quan sát"), chỉ hỏi Jev về màn hình stub chịu thua.

Mate headless không có màn hình, nên Jev không đọc nó.

## Bật

Lưu API key trong một file ngoài repo, chỉ user đọc được (`chmod 600`). Không đặt key trong workspace, command line hoặc file commit.

Jev bật theo từng workspace, trong `.mate/.env` của workspace đó; mate không đọc biến môi trường của process, nên workspace khác không bị bật theo:

```sh
# <workspace>/.mate/.env
MATE_JEV=on
MATE_JEV_API_KEY_FILE=~/.config/mate/jev-trial-api-key
```

`MATE_JEV_API_KEY_FILE` chứa **đường dẫn**, không chứa key; `~/` được mở rộng, đường dẫn tương đối tính từ gốc workspace. Giá trị `MATE_JEV`:

| `MATE_JEV` | Action Explain notice | Bộ quan sát pane |
| --- | --- | --- |
| `on`, `observer` (`true`, `1`, `yes`) | bật | chain: stub trước, Jev khi stub chịu thua |
| không đặt (có key), `fixture` | bật | chỉ stub, không request nào cho pane |
| `off` (`false`, `0`, `no`) | tắt | chỉ stub, không request nào tới Jev |
| không đặt, không có key | tắt, im lặng | chỉ stub |
| giá trị khác | tắt, báo lỗi | chỉ stub |

`.env` sai cú pháp, thiếu key, file key không đọc được hay `MATE_JEV_THRESHOLD` sai thì Jev tắt và mate chạy tiếp với stub: console báo `Jev disabled: …` trên status line, lệnh khác có đọc pane in một dòng ra stderr. Mở lại console để đọc lại `.env`.

## Action Explain notice

Trên hàng Mate hoặc Crew có binding active, bấm `a` rồi `e`. Kết quả mở trong sheet cuộn được; `Esc` đóng. Action chỉ gọi API khi được chọn, không gọi theo mỗi lần refresh.

Jev phân loại một ảnh chụp text thành một trong bảy nhãn:

| Nhãn | Ý nghĩa |
| --- | --- |
| `quota_warning` | Gần giới hạn, chưa có bằng chứng request đang bị chặn |
| `quota_exhausted` | Notice nói request bị chặn do giới hạn hiện tại |
| `auth_required` | Cần đăng nhập hoặc credential |
| `permission_required` | Có yêu cầu xác nhận quyền hoặc trust |
| `update_notice` | Thông báo cập nhật phần mềm |
| `none` | Không nhận ra notice hiện tại |
| `unknown` | Notice lạ, thiếu hoặc mâu thuẫn thông tin |

Sheet ghi thời điểm capture và confidence từ model. Confidence không phải độ chính xác đã đo; mọi nhãn đều là gợi ý. Màn hình có thể đã đổi khi kết quả tới. `none` không có nghĩa task đã xong, composer an toàn để gửi hay mọi lỗi đã hết.

## Dữ liệu và giới hạn

- Mỗi lần hỏi gửi tối đa 40 dòng cuối, đọc qua adapter Herdr, và tối đa 8 KiB UTF-8 text tới TypeSafe. Nguồn đọc có thể là recent output, không đảm bảo là viewport hiện tại. API key được dùng cho header xác thực.
- Loại ANSI/control sequence, che key của chính client và một số mẫu credential phổ biến. Bộ quan sát giữ thêm đánh dấu chữ mờ (`⟨faint⟩…⟨/faint⟩`). Đây là redaction tốt nhất có thể theo pattern, **không bảo đảm loại hết dữ liệu riêng tư**. Phần terminal gửi đi vẫn có thể chứa prompt, đường dẫn, code hoặc output. Chỉ bật cho nội dung bạn đồng ý gửi tới TypeSafe.
- Model `jev-1.13.0` cố định, endpoint HTTPS cố định; không theo redirect, không tự retry; deadline 8 giây. Response lỗi, nhãn ngoài enum hoặc phân phối xác suất sai bị từ chối.
- Action không ghi màn hình, response hoặc key vào state/log của mate. Result chỉ nằm trong sheet hiện tại. Action không cache, không chạy nền, không ghi nhãn vào timeline/inbox, không tham gia composer classifier, sender, receipts, incidents, quota dispatch hoặc task state. Nó không tự nhấn Enter, cấp quyền hay đóng dialog.

## Bộ quan sát

Với `MATE_JEV=on` hoặc `observer` cùng một file key đọc được, mọi lần mate đọc pane của Crew hay Mate có màn hình đi qua chain:

- **Stub trước.** Stub đọc được composer, hoặc nhận ra một màn hình khởi động đã đo, thì đó là toàn bộ kết quả: không request, không dòng log.
- **Gửi không bao giờ hỏi Jev.** Mate chỉ gõ vào composer stub đọc là rỗng, và câu trả lời của Jev không đổi được điều đó.
- **Màn hình stub chịu thua** (watch của console, stow, settle khởi động, `mate state`) mới được hỏi Jev. Jev chỉ được gọi tên dialog và notice. Composer vẫn là `unknown` của stub, nên không có gì được gõ hay xác nhận theo lời Jev. Jev lỗi, hay confidence dưới `MATE_JEV_THRESHOLD` (mặc định `0.85`, số trong `[0, 1]`), thì dùng nguyên đọc của stub.

**Trạng thái pane.** Bước settle của `mate start`, `crew spawn`, `crew relaunch`, và kiểm tra `stale` của watch, hỏi một câu riêng, `pane_state`, nhãn là các trạng thái pane của mate (`ready`, `busy`, `busy_background`, `draft`, các `dialog_*`, `auth_required`, `quota_exhausted`, `exited`, `unknown`…). Stub đọc được thì không có request. Chỉ khi stub ra `unknown` mới hỏi Jev; dưới ngưỡng hay lỗi thì vẫn là `unknown` và settle đọc lại. Jev chỉ gọi tên trạng thái, bảng của mate quyết định hành động: phím chỉ bấm vào dialog mà profile của harness tự nhận ra và tự thấy highlight. Trust, update hay dialog khác mà chỉ Jev nhận ra thì settle từ chối, ghi rõ nguồn là Jev, và không bấm gì. Hook review chỉ đi qua `ReviewOwn` của harness: nó tự đọc bảng hook và chỉ trust đúng hook mate cài, không bao giờ "Trust all".

**Nhịp hỏi.** Watch của console hỏi trong nền, tối đa một request đang bay mỗi crew, và chỉ hỏi về màn hình đã đứng yên qua một vòng poll. Pane đổi mỗi vòng (đang vẽ spinner) chỉ được stub đọc. Câu trả lời tới khi màn hình đã đổi thì bị bỏ. Một câu trả lời được dùng lại 60 giây cho cùng harness và cùng màn hình. Sau 3 request lỗi liên tiếp, chain ngừng hỏi Jev 60 giây (lý do `jev: circuit open`), rồi thử lại một request: thành công thì đóng, lỗi thì chờ thêm 60 giây.

**`.mate/jev.log`.** Mỗi request một dòng: thời điểm, harness, 12 ký tự hash màn hình, latency, nguồn được dùng, nhãn và confidence của Jev, `fixture=` (composer stub đọc), `caller=` (`watch`, `settle`, `stow`, `-` khi không ghi, như `mate state`), `used=` (câu trả lời của Jev có trong kết quả chain trả về không), lý do fallback; dòng của câu `pane_state` có thêm `state=`. Breaker ghi một dòng `breaker open` khi mở và `breaker closed` khi đóng. Log không có nội dung màn hình hay key. `used=yes` là chain đã dùng câu trả lời; watch vẫn có thể bỏ nó nếu pane đổi trong lúc chờ, và log không thấy việc đó.

**`.mate/screens/`.** Mỗi màn hình stub không đọc được, dù Jev được hỏi hay không (kể cả lúc gửi hay lúc circuit mở), được giữ ở `.mate/screens/<harness>/<hash12>.txt`, quyền 0600, tối đa một lần mỗi phút cho cùng màn hình. File có bảy dòng header (`first_seen`, `last_seen`, `seen`, `harness`, `caller`, `stub`, `jev`) rồi đúng phần text đã hoặc sẽ gửi Jev. Cùng hash thì chỉ cập nhật `last_seen` và `seen`. Mate không bao giờ đọc lại thư mục này để phân loại; nó để captain viết stub mới. Thư mục chỉ có khi chain bật, nằm trong `.mate/`, không ra khỏi máy.

## Kiểm thử

```sh
make check
make test-race
MATE_LIVE=1 MATE_JEV_API_KEY_FILE="$HOME/.config/mate/jev-trial-api-key" \
  go test ./internal/notice -run TestLiveJevNotices -v -count=1
```

Live test gửi các fixture **giả lập** trong `internal/notice/testdata/notices.json`, không đọc pane/workspace của người dùng. Biến môi trường `MATE_JEV_API_KEY_FILE` ở đây chỉ dành cho live test; mate chỉ đọc `.mate/.env`. Kết quả đầu tiên: [evidence 2026-09-27](archive/evidence/jev-notices-2026-09-27.md). Đây không phải live acceptance của toàn bộ luồng Herdr → console → API.

`TestLiveJevObserverDrift` (`./internal/screen/jev`, cùng hai biến) gửi lại 90 màn hình của corpus và báo bao nhiêu câu trả lời còn khớp cassette; tốn 90 request.

Bộ quan sát không có live acceptance trong `make check`: unit test chạy từ cassette, không gọi mạng. Để đo một ngày chạy thật với chain bật:

```sh
go run ./scripts/jeveval -log <workspace>/.mate/jev.log
```

in số request, latency p50/p95, số lần fallback theo lý do, số lần Jev và stub đọc composer khác nhau, số câu trả lời được dùng / không dùng, số request theo caller và số lần circuit mở.

Hợp đồng request/response theo [TypeSafe API reference](https://docs.typesafe.ai/api); hạn chế model theo [Jev jaggedness](https://docs.typesafe.ai/model-jaggedness/jev-1.13).
