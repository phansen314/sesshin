---
name: sesshin
description: Launch and watch Claude Code sessions with the sesshin CLI. Use when the user wants to start another Claude session (in a new kitty tab, split, or window), hand work to a parallel session or a different model, name a session with a job, see which Claude sessions are running or waiting, look one up by its sesshin ID (#12), bring an ended session back with resume, type a prompt into a running one with send, or prune old sessions; and when the user mentions sesshin by name.
---

# sesshin

`sesshin` records every Claude Code session on this machine (its hooks write one directory of small JSON files per session) and launches new ones. Each session has a **sesshin ID** (`#12`, shown in its status line) and, optionally, a **job**: a name such as `api` or `review-142`, unique among live sessions. Every command prints **one line of JSON** and nothing else.

## Before the first command

```sh
sesshin version
```

`.result.version` and `.result.commit` say which build is installed. sesshin only knows about sessions whose hooks run it: if `sesshin list` shows nothing while sessions are clearly running, its hooks aren't installed. Tell the user, and suggest `sesshin install`, which only *proposes* a change to Claude Code's `settings.json` for them to review and apply: sesshin's hooks, its status line, and the permission rules that let you run `sesshin` without a prompt. **Never run `install` or `uninstall` unasked**, and never apply their proposal yourself: they change this machine's Claude setup.

## Reading output

Every command writes one envelope:

- success: `{"ok":true,"result":{…},"warnings":[…]}`
- failure: `{"ok":false,"error":{"kind":"…","message":"…","details":{…}},"warnings":[…]}`

Branch on `.error.kind`, not the exit code. **Always tell the user about any `warnings`**, whatever their kind (`unusable-file`, `duplicate-id`, `not-started`, `placement-not-recorded`, `transcript-missing`, `status-line-replaced`, …).

| Exit | Meaning | What to do |
|---|---|---|
| 0 | Success | — |
| 1 | Operation error; see `.error.kind` | See below |
| 2 | Usage error (bad command line) | Fix the command; check `sesshin <cmd> --help` |
| 3 or other | Outcome unknown (killed, stdout lost) | Reads: rerun. `spawn` and `resume`: **don't** rerun blind; check `sesshin list` first (below). `send`: **never** rerun; the text may already be typed. `prune`: safe to rerun. |

Error kinds worth handling:

- `invalid-input` — `.error.details.problems[]` lists every bad field.
- `not-found` — `.error.details.sessions` (a selector that matched nothing) or `.paths` (a `cwd` that isn't a directory).
- `ambiguous` — a UUID prefix matched several sessions; `.error.details.candidates` lists them. Use a longer prefix or the sesshin ID.
- `conflict` with `rule: "job-taken"` — a live session or a fresh reservation already has that job. `.error.details.sessions` names the session (empty for a reservation: a spawn still starting). Pick another job, or ask the user.
- `conflict` with `rule` `not-live`, `mid-turn`, or `no-placement` — `send`'s refusals; see [Sending text](#sending-text-to-a-session).
- `busy` — the state lock was held for half a second (a hook issuing a sesshin ID, another `spawn`, or a `prune`); nothing was launched. Retry once after a moment.
- `terminal` — see [Spawning](#spawning-a-session).
- `corrupt`, `environment`, `io`, `internal` — stop and report to the user, quoting `.error.message`; don't edit sesshin's files to fix them.

## Keep output small

A whole session view is about 2 KB, so a bare `sesshin list` of a busy machine is tens of KB in your context. **Always pass `--fields` to `list`**, and `--limit` when you only need the newest few:

```sh
sesshin list --fields name,job,status,cwd                     # live sessions, newest first
sesshin list --liveness all --limit 10 --fields name,job,liveness,last_seen
sesshin list --limit 0 | jq .result.total                       # just the count
```

- Fields: `name`, `job`, `source`, `headless`, `liveness`, `status`, `stall_reason`, `pending`, `cwd`, `git_branch`, `model`, `permission_mode`, `entrypoint`, `nested`, `pid`, `started_at`, `last_start_at`, `last_event_at`, `last_event_type`, `event_seq`, `last_seen`, `ended_at`, `end_reason`, `compactions`, `metrics`, `prompt_cache`, `placement`, `transcript_path`, `transcript_exists`. `id` and `session_id` are always included.
- `--liveness live` (the default, which includes `unknown`), `ended`, or `all`. Headless sessions (`claude -p`, and sessions other sessions started) are hidden unless `--include-headless`.
- The result says `total` and `truncated`: when `truncated` is true there are `total` sessions and you got fewer. Say so.
- `sesshin show <id, UUID prefix, or job>` returns one session whole; add `--include-payload` only when you need the raw status-line payload (rate limits, say).

**Status** is what the session's last event said: `idle` (at its prompt, nothing done yet), `working`, `waiting` (its turn ended: it wants the user, unless `pending` shows background tasks or crons of its own), `needs_approval` (blocked on a permission prompt). An interrupted turn fires no hook, so a session can read `working` after Esc until its next event.

## Spawning a session

```sh
sesshin spawn --job api --cwd ~/code/api --prompt 'run the test suite and fix failures'
sesshin spawn --job review-142 --cwd . -- --model claude-sonnet-5-5 --permission-mode auto
gh issue view 42 --json body -q .body | sesshin spawn --job issue-42 --prompt-file -
```

`spawn` opens a new kitty tab (`--type split` or `os-window` for the others) beside the user's window, without taking focus, runs `claude` in it through the user's login shell, and waits up to `--start-timeout-secs` (default 15) for the session to start. `.result.session` is the new session's view, with its sesshin ID; `.result.placement` is the window.

- **Needs kitty with remote control,** run from inside a kitty window, not under tmux or screen. Otherwise it fails `terminal` with `reason: "unavailable"`: tell the user; don't try another way to open a window.
- **The job** follows koan's name rule: letters (either case, and case matters), digits, and hyphens, at most 64, not starting or ending with a hyphen, and not all digits (`12` always means a sesshin ID). With no `--job`, the session is unnamed: find it by its sesshin ID.
- **Everything after `--` goes to `claude` untouched**, before the prompt: `--model`, `--permission-mode`, and the like. The prompt is passed as one argument, so quotes, `$(…)`, and leading `-` are safe.
- **`--extra '<json object>'`** stores free-form data on the session, for whatever spawned it to find it again: `--extra '{"koan-task":57}'`, then `sesshin list --liveness all --fields job,extra | jq '.result.sessions[] | select(.extra["koan-task"] == 57)'`. sesshin never reads it. It is set once, at spawn, and kept across `resume`; nothing changes it afterwards. A `/clear` in that window keeps it, so read it as the work the window was started for.
- **`--cwd`** defaults to the current directory. `--var KEY=VALUE` (repeatable) sets kitty user variables on the window, for matching it later; they are not environment variables.
- **The workspace-trust dialog.** A `claude` started in a directory it hasn't been trusted in waits at Claude's trust dialog, and no hook runs until the user accepts it. `spawn` then returns `session: null` with a `not-started` warning. That is not a failure: tell the user to accept the dialog in the new tab. The job stays reserved while that window is open.

**Retry safety.** After `invalid-input`, `not-found`, `conflict`, `busy`, or `terminal` with `reason` `unavailable` or `launch-failed`, nothing was launched: fixing the cause and rerunning is safe. After `terminal` with `reason: "launch-unknown"`, a `not-started` warning, or exit 3 / a signal, **a window may have opened**: never rerun `spawn` blind. Check first:

```sh
sesshin list --fields name,job,status,cwd,placement
```

Rerunning with the same job fails `job-taken` while the first is still starting; with no job, it opens a second window.

## Bringing a session back

```sh
sesshin resume 12                                        # by sesshin ID, UUID prefix, or job (its last session)
sesshin resume api -- --permission-mode acceptEdits      # claude flags: --resume restores the conversation, not the flags
sesshin resume 12 --job api-old                          # when the session's own job is held by another
```

`resume` reopens an **ended** session with `claude --resume`, in a new tab beside the user's window (kitty, as `spawn`), in its own directory, under its title and job, and waits up to `--start-timeout-secs` (default 15) for it to be live again. A live session is refused (`conflict`, `rule: "live"`); one whose directory is gone is `not-found`; a `transcript-missing` warning means `claude --resume` will likely fail. Find the session first with `sesshin list --liveness ended --fields name,job,end_reason,ended_at --limit 10`.

**Never run `sesshin restart`.** It is a picker for a person: it draws fzf on the user's terminal and fails without one. When the user asks to bring back everything a reboot ended, suggest it to them (`sesshin restart`, type `killed`, ctrl-a, Enter; `-- <claude args>` for flags), or `resume` the sessions they name yourself.

## Sending text to a session

```sh
sesshin send api --text 'run the tests again'                  # by job, sesshin ID, or UUID prefix
sesshin send 12 --text-file notes.md
git diff | sesshin send api --text-file - --submit=false       # paste it, leave it in the input box
```

`send` types the text into a **live** session's kitty window as one paste, then presses Enter (`--submit=false` skips the Enter). A job selects the live session holding it. It finds the window by the session's process, so a window that has moved or gone costs nothing.

- **It refuses a session mid-turn** (`conflict`, `rule: "mid-turn"`): text plus Enter could answer a dialog the session has up. Check first, and wait or tell the user if it is not `waiting` or `idle`: `sesshin show api | jq -r .result.session.status`. A session blocked on a permission prompt reads `needs_approval` and is the user's to answer. One the user interrupted (Esc) reads `working` until its next event; `--force` is right only if the user confirms it is sitting at its prompt.
- **`--force` only when the user wants to answer a prompt** (`sesshin send api --text yes --force`), never to get past a refusal. Say what you are sending.
- **Plain text only:** the text may hold tabs and line breaks (they arrive as one multi-line prompt), but no control characters (`invalid-input` at `/text`), and at most 1 MiB. Use `--text-file -` for text from another command.
- **An ended session is `conflict` `not-live`**; a session with no kitty window known is `no-placement`; `terminal` with `reason: "unreachable"` means no window runs the session (nothing was typed).
- **Never retry after `send-failed` or `submit-failed`, or exit 3:** the text may already be in the input box, or sent, and a retry types it again. Look at `sesshin show … | jq .result.session.event_seq` against the `event_seq` the earlier call returned, or ask the user to look at the window.
- The result's `permission_mode` says whether the session acts without asking: say so when you prompt one that won't.

## Watching a spawned session

There is no command to bring a window to the front: the user works in the session's tab. To follow it:

```sh
sesshin list --fields name,job,status,last_event_type,last_seen | jq -c '.result.sessions[] | select(.job == "api")'
sesshin show 12 | jq '.result.session | {status, pending, metrics}'
```

Poll sparingly (every 30 seconds or more, with `--fields`), and prefer a completion signal the session itself writes (a report file the prompt asks for) over watching `status`. A session that is `waiting` has finished its turn; one that is `needs_approval` needs the user.

## Pruning

`sesshin prune` removes ended sessions older than the retention window (by default 30 days, and 24 hours for headless sessions; `retain_days` and `retain_headless_hours` in sesshin's `config.toml`) and stale job reservations. **It deletes, so it asks for permission; never run it unasked.** `sesshin prune --dry-run` shows what it would remove and is safe.

## Hard rules

- **Never edit, create, or delete anything in sesshin's state directory** (`~/.local/state/sesshin` or `$XDG_STATE_HOME/sesshin`; on macOS `~/Library/Application Support/sesshin/state`) by hand, except one thing the user asks for: removing `reservations/<job>.json` releases a job claimed by a spawn that never started.
- **Never run `sesshin restart`:** it needs the user's terminal. Suggest it; use `resume` yourself.
- **Never run `install`, `uninstall`, or `prune` unasked,** and never apply `install`'s proposal to `settings.json` yourself.
- **Before spawning on your own initiative, say so:** each spawn opens a window and starts a session the user pays for. When the user asked for parallel work, spawn what they asked for and no more.
- Don't rerun a `spawn` whose outcome is unknown; check `sesshin list`.
- **Never use `send --force` to get past a `mid-turn` refusal,** only to answer a prompt the user asked you to answer; and never rerun a `send` that failed `send-failed` or `submit-failed`.
