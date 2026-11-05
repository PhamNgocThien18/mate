# `mate migrate` trên workspace thật, 2026-10-08

Workspace `/Users/erics/workspace` (4 project, 1 project có 2 repo), binary build từ `main` 515f0b1. Backup `cp -a` trước khi chạy (108 MB). Mọi dòng dưới là output thật.

## Trước

- `workspace.yaml` không có `layout:`; repo `hellovietnam` và `hellovietnambackend` ngang hàng `.mate/`.
- 2 crew mở (`e0-skeleton`, `e0-skeleton-review`, cùng repo `hellovietnam`), 4 Mate idle trong Herdr 0.9.3 session `mate-7b09de89`.
- Tracker Beads ở `.mate/projects/hellovietnam/.beads`.

## Dừng crew và Mate (hai lệnh mở được layout cũ)

- `mate crew stop hellovietnam e0-skeleton-review`: dừng, worktree và branch gỡ (đã land).
- `mate crew stop hellovietnam e0-skeleton`: từ chối `state_conflict: branch mate/e0-skeleton is 1 commit(s) ahead of main`. `mate merge` bị `ErrLayoutOld` chặn (ngõ cụt còn sót: `merge` không nằm trong hai lệnh được mở layout cũ). Land bằng `git merge --ff-only` trong repo rồi `crew stop` lần hai: dừng.
- `mate mate stop <p>` ×4: dừng, session_id giữ; hellovietnam không stow vì composer có text chưa gửi.

## Migrate

```
$ mate migrate --dry-run
would move /Users/erics/workspace/hellovietnam -> /Users/erics/workspace/hellovietnam/hellovietnam
would move /Users/erics/workspace/hellovietnambackend -> /Users/erics/workspace/hellovietnam/hellovietnambackend
would write workspace layout 2

$ time mate migrate
moved /Users/erics/workspace/hellovietnam -> /Users/erics/workspace/hellovietnam/hellovietnam
moved /Users/erics/workspace/hellovietnambackend -> /Users/erics/workspace/hellovietnam/hellovietnambackend
repaired 0 worktree(s)
rewrote 19 brief(s)
workspace layout 2
0.217 total
```

`migrate.log`: 5 dòng, ca trùng tên đi qua `hellovietnam.migrating-fbcdca03` (staged → renamed → kết thúc), repo thứ hai renamed → kết thúc.

## Sau

- `layout: 2`; `project.yaml` `path: hellovietnam/hellovietnam`; ba project không repo có thư mục rỗng.
- `git status` sạch ở cả hai repo; `git worktree list` chỉ còn checkout chính (hai worktree crew đã gỡ khi stop).
- 17 brief nhắc đường dẫn mới; 2 chỗ còn đường dẫn cũ là trong `handback.md` của crew `monorepo` (lịch sử lệnh đã chạy, đúng là không sửa).
- `mv .mate/projects/hellovietnam/.beads hellovietnam/.beads` bằng tay; `mate tool beads hellovietnam -- ready` đọc được, lock mới `.mate/projects/hellovietnam/locks/beads.lock`. `bd` in `warning: beads.role not configured (GH#2950)`, là của bd, chưa xử lý.
- `mate mate start <p>` ×4: resume đúng session cũ.

## Chưa làm

- `TestLiveMigrateThenSpawn` với `MATE_LIVE=1` chưa chạy.
- Hai crew đã đóng trước khi migrate, nên bước "gắn lại worktree crew đã đóng" ra `repaired 0`; chưa có bằng chứng live cho ca có worktree.
