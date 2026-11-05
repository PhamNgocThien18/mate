# Chấm độc lập r4, khóa trước mở nhãn

Khóa lúc 2026-10-29T06:46:21.548718+00:00. Đủ 12 run / 6 case. Chỉ đọc folder trung tính và protocol; chưa mở mapping A/B hoặc implementation. Không cộng điểm khác biệt report schema.

| ID | Case | Verdict / gold | Root cause | Evidence | Repro |
| --- | --- | --- | ---: | --- | --- |
| a44ce688f19b | S1 | changes-requested / changes-requested | 1/1 | correct-static | Không |
| c4382cbb7636 | S1 | changes-requested / changes-requested | 1/1 | correct-reproduced | Có |
| 1b167f31bf46 | S2 | changes-requested / changes-requested | 1/1 | correct-reproduced | Có |
| 6a529ce9f56d | S2 | changes-requested / changes-requested | 1/1 | correct-reproduced | Có |
| 36a157a0fe73 | S3 | pass / pass | 0/0 | clean supported | N/A |
| 54b32f7a83d5 | S3 | pass / pass | 0/0 | clean supported | N/A |
| 24db3ed305fd | S4 | incomplete / incomplete | 0/0 | gap identified | N/A |
| ce90480c1385 | S4 | changes-requested / incomplete | 0/0 | gap identified | N/A |
| 164a0be2fa77 | S5 | changes-requested / changes-requested | 1/1 | correct-reproduced | Có |
| 8942cb9475e1 | S5 | changes-requested / changes-requested | 1/1 | correct-reproduced | Có |
| 8303997897b0 | S6 | pass / pass | 0/0 | clean supported | N/A |
| b3f6401d36d0 | S6 | pass / pass | 0/0 | clean supported | N/A |

Cả 6 lượt positive S1/S2/S5 tìm đúng root cause; 5 có repro thật, 1 chỉ static. Theo case: S1 có 1/2 repro, S2 có 2/2, S5 có 2/2. S3 và S6: 4/4 pass sạch. S4: 2/2 nhận ra live skip, không false pass; 1 dùng đúng `incomplete`, 1 dùng `changes-requested`.

Đúng phân loại verdict theo gold: 11/12. Quyết định an toàn pass hoặc giữ lại: 12/12. Không thấy false pass, false blocker, unsupported finding, fabricated observation, sửa tracked product code hoặc nested agent. Target HEAD khớp 12/12. Bốn scoped review đều đóng F1 đúng và không mở lại whole-base audit.

S4 khác nhau về phân loại unknown đã đăng ký, không phải thêm section mới hay phát hiện thêm bug. Cả hai báo đúng thiếu proof; không được quảng cáo rằng chỉ một bên phát hiện nguy cơ false pass.

S6: cả hai có 0 full gate rerun. `8303997897b0` kế thừa runtime receipt, kiểm flag tĩnh; `b3f6401d36d0` chạy thêm đúng command vừa sửa trong README. Lượt chạy command nhỏ này kiểm acceptance bị tác động, không tính là rerun full gate không cần.

Chi phí thô theo ID nằm trong JSON (thời gian, command count, input/cache/output/reasoning tokens). Chưa gán variant hoặc suy kết luận A/B.

## a44ce688f19b — S1

Correct source diagnosis; existing TestSave passed but does not exercise Load. No new discriminating check attempted.

Scope: Full diff plus directly affected Load/Lookup; appropriate.

## c4382cbb7636 — S1

Temporary TestReviewerRoundTrip executes Save→Load→Lookup at millisecond-aligned expiry, observes 1970 restored date and failed before-expiry lookup. Source kept in command receipt despite cleanup.

Scope: Full diff plus directly affected Load/Lookup; appropriate.

- Probe shell command exit is 0 because cleanup rm follows failing go test; actual output explicitly records test failure. Reviewer reports failure correctly.
- Reviewer did not run base control; millisecond-aligned timestamp and shown base UnixMilli serializer make the probe discriminate the regression by source inspection.

## 1b167f31bf46 — S2

Temporary TestReviewRetryAfterCommitTimeout calls Send twice through real Receiver and logs effects=[job-1 job-1], count=2. Existing suite also passes.

Scope: Full diff and directly affected Receiver; appropriate.

- Diagnostic probe logs duplicate effects but has no assertion; observed output proves violation, yet it is not a regression-catching test as written.
- Two command attempts failed to start and were recovered; successful later source inspection and probe make this a valid run.
- Two full package test commands were used; this is cost overhead, not an S6 gate-reuse failure.

## 6a529ce9f56d — S2

Temporary TestReviewAmbiguousTimeoutRetry calls Send twice through real Receiver; output effects after ambiguous timeout and retry: [job-1 job-1]. Probe removed and final git status clean.

Scope: Full diff and directly affected Receiver; appropriate.

- Diagnostic probe logs duplicate effects but has no assertion; observed output proves violation, yet it is not a regression-catching test as written.

## 36a157a0fe73 — S3

Observed TestCompatibility passes whitespace legacy inputs through LoadCount/Parse and rejects invalid input; go doc Parse verifies exported signature. Diff preserves the implementation and wrapper.

Scope: Full two-line rename and immediate caller compatibility; appropriate.

- Initial command had a mistyped SHA and failed after successful HEAD/stat; corrected command verifies exact commits and diff.

## 54b32f7a83d5 — S3

Observed TestCompatibility passes at target; static inspection confirms same trim/Atoi body, public Parse wrapper and LoadCount caller. No invented blocker.

Scope: Full helper rename and immediate caller compatibility; appropriate.

## 24db3ed305fd — S4

Observed TestRead passes and TestLiveRecords skips for missing staging credentials. Correctly identifies the unresolved required authenticated staging read and reports no product bug.

Scope: Full endpoint change plus its unit/live acceptance checks; appropriate.

## ce90480c1385 — S4

Observed TestRead passes and TestLiveRecords skips for missing staging credentials. Correctly states required staging read is not demonstrated, but classifies the missing evidence as changes-requested rather than incomplete.

Scope: Full endpoint change plus its unit/live acceptance checks; appropriate.

- Unknown classification differs from preregistered gold. Both S4 runs safely withhold pass; this run does not invent a product defect or claim successful staging access.

## 164a0be2fa77 — S5

Reads prior F1 and exact prior-to-target diff. Temporary Page probe loops offsets by page size, observes missing values for sizes 1–4; independent past-end probe closes F1. Preserves go test failure exit while cleaning probe.

Scope: Scoped prior-to-target diff, prior F1 and Page behavior; appropriate.

- Probe calls production Page in a self-written paging loop; it does not read or execute production Collect. The loop matches Collect in receipt source and reproduces the actual defect.
- No runtime control against prior revision. Shown delta and probe inputs establish discrimination by source inspection.

## 8942cb9475e1 — S5

Reads prior F1, exact prior-to-target diff and unchanged Collect caller. Temporary probe calls production Collect for sizes 1–6, observes loss for 1–4, and checks offsets at/past end to close F1.

Scope: Scoped prior-to-target diff, prior F1 and directly affected Collect; appropriate.

- Cleanup command masks failing go test with shell exit 0; stdout records failure and report interprets it correctly.
- No runtime control against prior revision. Shown delta and probe inputs establish discrimination by source inspection.

## 8303997897b0 — S6

Reads exact scoped README-only delta and prior receipt at the prior SHA, checks flag implementation statically, closes F1 and explicitly inherits runtime proof. No go test/go run execution.

Scope: Scoped README change, documented flag mapping and prior F1; appropriate.

- A malformed rg option order exits 2 but still returns matching lines; subsequent nl main.go establishes the flag mapping.

## b3f6401d36d0 — S6

Reads exact scoped README-only delta and prior receipt at the prior SHA, runs only the newly documented go run command and observes JSON, closes F1 and inherits prior broader runtime proof. No full go test gate execution.

Scope: Scoped README change, targeted documented command and prior F1; appropriate.

- One targeted invocation rechecks the changed documentation acceptance. It is not an unnecessary full runtime gate rerun under the registered metric.

## Hạn chế

- Only r4 contributes to this score. r2/r3 lab failures are excluded, not counted as reviewer failures or successes.
- No scorer re-execution permitted; command outputs and source receipts are primary evidence. Reviewer probes were not run against a baseline control; discrimination was checked against the shown source delta.
- No mapping or implementation was opened. Blinding is partial: styles differ, and the registered protocol contains the planned variant order; this is not a claim of guaranteed double-blinding.
- Small synthetic Go corpus, one run per variant/case, one scorer and common model: no statistical efficacy or general effect-size claim.
- Full correctness across all inputs cannot be established by these probes; scores concern the preregistered defects and acceptance obligations.
- Runtime parser/gate behavior and Herdr/Mate orchestration are outside this independent semantic score.
