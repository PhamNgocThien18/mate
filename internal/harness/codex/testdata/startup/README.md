# Codex startup screens

Reproduced 2026-11-06 on Ubuntu WSL2 with Codex 0.167.0, using a PTY,
an isolated `CODEX_HOME` (auth.json copied, no operator config), and a scratch
git repository. Launched with MATE's `--dangerously-bypass-approvals-and-sandbox`
and `-c check_for_update_on_startup=false`, plus `--no-daemon --no-alt-screen`.
No model prompt was submitted. Herdr 0.9.5 is installed, but this capture is
transcribed from the PTY's rendered text, not a Herdr read:

- `codex-0.167.0-trust-dialog.txt`: option 2 is now **Back to Agent Command
  Center**, and the footer is `enter continue · esc back`. The existing
  0.156.1 recognizer rejects both changed labels. The question and option 1
  remain unchanged. Negative selections and malformed variants are synthesized
  in `TestStartupTrustDialogV167`.

Claude Code's are in `internal/harness/claude/testdata/startup`.

Captured 2026-09-14 through `herdr agent read <name> --source recent-unwrapped --lines 40`
against Herdr 0.8.2 panes in an isolated lab session, with isolated `CODEX_HOME` /
`CLAUDE_CONFIG_DIR` (the operator's real account files were never written).
The scratch repository paths inside the captures are the lab's, not a real project's.

- `codex-0.154.0-trust-dialog.txt` - codex-cli 0.154.0 directory-trust dialog on a
  standalone `git init` repository with no `projects` entry. Option 1 is highlighted by
  default.
- `codex-0.154.0-ready.txt` - the same pane a few seconds after `1`, `enter`.

Synthesized from the captures above (the highlight marker moved by hand, nothing else
changed) so the selection check has a negative and a positive case:

- `codex-0.154.0-trust-dialog-quit-selected.txt` - the marker on **2. No, quit**.

Captured 2026-09-18 the same way, against codex-cli 0.154.0 with 0.155.0 published,
in a throwaway `/private/tmp` directory codex had never seen. These are one launch
read four times, in order:

- `codex_update_dialog.txt` - the release-update prompt as drawn. It comes up before
  the directory-trust dialog and blocks the composer; the highlight opens on
  **1. Update now**, which would run `npm install -g @openai/codex` under the agent.
- `codex_update_dialog_skip_selected.txt` - the same pane after two `down` presses,
  with the highlight on **3. Skip until next version**.
- `codex_update_dialog_after_enter.txt` - what Enter on that option produced: the
  directory-trust dialog, for the same directory.
- `codex_update_banner_ready.txt` - the composer a few seconds after `1`, `enter`.
  The update notice is still in the scrollback as a box banner, so this screen
  carries "Update available!" without the dialog's shape and must classify as ready.

Answering the prompt with **3. Skip until next version** writes
`dismissed_version` into `$CODEX_HOME/version.json`, which is why the prompt does
not come back until the next release. mate does not rely on that: the launch passes
`-c check_for_update_on_startup=false` so the prompt is not drawn at all, and the
recogniser stays as defence for the launches that flag does not cover.

Captured 2026-09-24 (task 35) the same way, against codex-cli 0.154.0 with 0.156.1
published, in Herdr 0.8.2 lab panes under the scratchpad:

- `codex-0.154.0-resume-update-dialog.txt` - `codex resume <id>` launched **without**
  `-c check_for_update_on_startup=false`, in a directory codex already trusted, with
  the session's history above it. It is the same release-update prompt a fresh
  launch draws; with the flag, which every mate resume carries, it is not drawn.
- `codex-0.154.0-resume-ready.txt` - the same pane after `down`, `enter` (**2. Skip**,
  so the lab wrote nothing to the operator's `version.json`) and one more question.
  The replayed conversation puts `›` prompt lines above the composer, so this is the
  screen that proves the composer rule reads the last one.
- `codex-0.154.0-hooks-review.txt` - "Hooks need review", drawn after the
  directory-trust dialog when the effective hooks (a lab `CODEX_HOME/hooks.json` and
  the cwd's `.codex/hooks.json`) have no persisted trust. Task 37 gave the Codex Mate a
  hook of its own, and this layout is recognised with the 0.156.1 one below.

The installed codex moved from 0.154.0 to 0.156.1 during the same session (not by
mate: every lab update prompt was answered **2. Skip**), and the first live Codex
Mate start after it died on a trust dialog of a new shape. Captured the same way, in
a scratchpad directory codex had never seen:

- `codex-0.156.1-trust-dialog.txt` - "Folder access", the path, a paragraph opening
  "Trust this folder?", options **1. Trust and continue** / **2. Quit**, footer
  `enter continue · esc quit`. The highlight opens on option 1, and pressing `1`
  leaves the screen byte for byte the same (it selects, it does not confirm), so the
  0.154.0 answer (`1`, re-read, `enter`) is still right.
- `codex-0.156.1-ready.txt` - the composer a few seconds after that `enter`.
- `codex-0.156.1-trust-dialog-quit-selected.txt` - synthesized: the marker moved to
  **2. Quit**, nothing else changed.

Captured 2026-09-24 (task 37) the same way, against codex-cli 0.156.1 in Herdr 0.8.2 lab
panes, with a lab `CODEX_HOME` (the operator's auth copied, `features.hooks = true`)
and a lab Mate directory whose `.codex/hooks.json` holds one SessionStart hook,
`'/usr/local/bin/mate' hook mate-session` (a stand-in path; the tests put the real
path and command in its place). Each file is one read, 1.5 s after the key named:

- `codex-0.156.1-hooks-review.txt` - the dialog after the directory-trust dialog:
  "Hooks need review", "1 hook is new or changed.", options **1. Review hooks** (the
  highlight opens here), **2. Trust all and continue**, **3. Continue without
  trusting (hooks won't run)**, footer `enter confirm · esc skip`. The count names no
  hook, which is why the settle never answers **2**.
- `codex-0.156.1-hooks-table-review.txt` - `enter` on option 1: the hook-event table,
  "⚠ 1 hook needs review before it can run.", SessionStart `1 0 1` selected, footer
  `t trust all · enter review · esc close`.
- `codex-0.156.1-hooks-sessionstart-own.txt` - `enter` on SessionStart: the event's
  list (`› [!] Hook 1 · new`) and the selected hook's Event, Source (`Project config -
  <path>`, wrapped at the pane width), Command, Mode, Timeout and Trust, footer
  `t trust · esc back`.
- `codex-0.156.1-hooks-closed-ready.txt` - the composer after `esc` out of the review.

A second launch added an untrusted hook in the lab `CODEX_HOME/hooks.json`, so the
review lists two:

- `codex-0.156.1-hooks-review-two.txt` - the dialog: "2 hooks are new or changed."
- `codex-0.156.1-hooks-table-review-two.txt` - the table: SessionStart `2 0 2`.
- `codex-0.156.1-hooks-sessionstart-two-foreign-selected.txt` - the list with the
  operator-side hook (`User config - <CODEX_HOME>/hooks.json`) first and selected.
- `codex-0.156.1-hooks-sessionstart-two-own-selected.txt` - after `down`: mate's.
- `codex-0.156.1-hooks-sessionstart-two-own-trusted.txt` - after `t`: `[x] Hook 2`,
  `Trust Trusted`, footer `space/enter toggle · esc back`; `t` trusts the selected
  hook only.
- `codex-0.156.1-hooks-sessionstart-two-all-trusted.txt` - after `up`, `t`: nothing
  left to review, header "Turn hooks on or off. Your changes are saved automatically."
- `codex-0.156.1-hooks-table-trusted.txt` - `esc`: the table without a Review column,
  footer `enter details · esc close`.

- `codex-0.156.1-hooks-sessionstart-own-wrapped.txt` - assembled from the screen tail
  a live `TestLiveCodexMateRecallHook` refusal quoted (the Event to Trust block and
  the footer are verbatim; the banner above is the list capture's): the Source path
  wrapped at `/` with the `/` not drawn (`…/.mate` then `projects/…`), the Command
  wrapped after `mate-`, and a `Context   limit: 32000 approximate tokens` row the
  lab captures without `additionalContextLimit` do not have.
- `codex-0.156.1-hooks-sessionstart-own-truncated.txt` - the whole screen of a live
  `TestLiveSpawnMateResumeRemembersCodex` refusal (2026-09-24, TMPDIR a long Claude
  scratchpad path): Codex cut the 194-character command at a word boundary and drew
  `…` in place of ` --harness codex`. The cut fell at the same word with a 179- and a
  178-character prefix, so it is a word boundary near 180 characters, not a fixed
  count.

Trusting writes `[hooks.state."<hooks.json>:session_start:0:0"] trusted_hash` into
`$CODEX_HOME/config.toml`; the next launch with the same hook bytes draws no review.

Captured 2026-10-13 from a real `mate mate start matemarketing --harness codex`
refusal against codex-cli 0.162.1, with the workspace under the operator's home:

- `codex-0.162.1-hooks-sessionstart-own-home.txt` - Codex draws the Source with
  the home directory as `~` (`~/workspace/...`), not the absolute path in
  hooks.json. The lines from the hook list down are the refusal's verbatim last
  12 screen lines; the banner and event title above them are copied from
  `codex-0.162.1-hooks-sessionstart-own.txt`, which the refusal did not print.
  Every earlier capture ran under `/private/tmp`, so none showed the `~`.

Captured 2026-10-13 with `herdr agent read mate-matemarketing --source visible`
(Herdr 0.9.3, codex-cli 0.162.1) during a real
`mate mate start matemarketing --harness codex`, hook not yet trusted:

- `codex-0.162.1-loading-composer.txt` - what Codex draws first: banner, cwd,
  the logo and an empty composer (`› Ask Codex to do anything`). It looks
  ready, but 1-6 s later Codex replaces it with the hook review dialog.
- `codex-0.162.1-hooks-review.txt` - that dialog, the same wording and footer
  as 0.156.1's.

Kept by the observer chain on a real `mate mate start matemarketing --harness codex`
(2026-10-14, codex-cli 0.162.1): the stub could not name it and Jev called it
`dialog_other` at 1.00, three starts in a row. The text is what Jev was sent
(`.mate/screens/codex/7a50115e7710.txt`, header removed), so the first line's
indent was trimmed:

- `codex-0.162.1-model-prompt.txt` - "Meet GPT-6 Sol", a paragraph, options
  **1. Try new model** (highlighted) / **2. Use existing model**, footer
  `enter/esc confirm · ⌃c quit`. Recognised as `model_prompt` so the settle refuses
  it by name; mate never picks a model.
