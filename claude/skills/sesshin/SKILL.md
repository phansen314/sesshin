---
name: sesshin
description: Launch and watch Claude Code sessions with the sesshin CLI. Use when the user wants to start another Claude session (in a new kitty or iTerm2 tab, split, or window), hand work to a parallel session or a different model, name a session with a job, see which Claude sessions are running or waiting, look one up by its sesshin ID (#12), bring an ended session back with resume, type a prompt into a running one with send, bring a session's window to the front with focus, tag a session with what it is working on (update, including `update self`), or prune old sessions; and when the user mentions sesshin by name.
---

# sesshin

`sesshin` records every Claude Code session on this machine (its hooks write one directory of small JSON files per session) and launches new ones. Each session has a **sesshin ID** (`#12`, shown in its status line) and, optionally, a **job**: a name such as `api` or `review-142`, unique among live sessions. Every command prints **one line of JSON** on stdout and nothing else there (a failure or warnings also leave a one-line note on stderr; read the JSON).

## Before the first command

```sh
sesshin version
```

`.result.version` says which build is installed (`.result.commit` is `null` for a build from `go install …@version`). sesshin only knows about sessions whose hooks run it: if `sesshin list` shows nothing while sessions are clearly running, its hooks aren't installed. Tell the user, and suggest `sesshin install`, which only *proposes* a change to Claude Code's `settings.json` for them to review and apply: sesshin's hooks, its status line, and the permission rules that let you run `sesshin` without a prompt. **Never run `install` or `uninstall` unasked**, and never apply their proposal yourself: they change this machine's Claude setup.

## Reading output

Every command writes one envelope:

- success: `{"ok":true,"result":{…},"warnings":[…]}`
- failure: `{"ok":false,"error":{"kind":"…","message":"…","details":{…}},"warnings":[…]}`

Branch on `.error.kind`, not the exit code. **Always tell the user about any `warnings`**, whatever their kind (`unusable-file`, `duplicate-id`, `not-started`, `placement-not-recorded`, `transcript-missing`, `status-line-replaced`, …).

**`migration-pending`** means sesshin was upgraded and its files haven't been converted yet: sessions may show no ID and miss events until `sesshin migrate` runs. Tell the user, and offer to run it (`sesshin migrate --dry-run` shows what it would change; `migrate` is safe to run alongside running sessions, and to rerun). **`migration-ahead`** means this `sesshin` is older than the data a newer one wrote: tell the user to upgrade the binaries; never try to fix the files.

| Exit | Meaning | What to do |
|---|---|---|
| 0 | Success | — |
| 1 | Operation error; see `.error.kind` | See below |
| 2 | Usage error (bad command line) | Fix the command; check `sesshin <cmd> --help` |
| 3 or other | Outcome unknown (killed, stdout lost) | Reads: rerun. `spawn` and `resume`: **don't** rerun blind; check `sesshin list` first (below). `send`: **never** rerun; the text may already be typed. `prune`, `update`, `focus`, `migrate`: safe to rerun. |

Error kinds worth handling:

- `invalid-input` — `.error.details.problems[]` lists every bad field.
- `not-found` — `.error.details.selectors` (a selector that matched nothing) or `.paths` (a `cwd` that isn't a directory).
- `ambiguous` — a UUID prefix matched several sessions; `.error.details.candidates` lists them. Use a longer prefix or the sesshin ID.
- `conflict` with `rule: "job-taken"` — a live session or a fresh reservation already has that job. `.error.details.sessions` names the session (empty for a reservation: a spawn still starting). Pick another job, or ask the user.
- `conflict` with `rule: "other-format"` — `resume` under a job, and the session's `sesshin.json` is in another format: run `sesshin migrate` first (or upgrade sesshin, when it is newer), or resume with no job.
- `conflict` with `rule` `not-live`, `mid-turn`, or `no-placement` — `send`'s refusals (`focus` refuses `not-live` and `no-placement` too); see [Sending text](#sending-text-to-a-session).
- `conflict` with `rule` `extra-too-large` — `update`'s `extra` would pass its size limit; send less.
- `unsupported-format` — `migrate` found the state migrated past this binary: tell the user to upgrade sesshin, as for `migration-ahead`.
- `busy` — another process held a lock past sesshin's wait (`.error.details.lock`: `state` or `session`); nothing was launched or changed (`migrate`: sessions already converted stay converted). Retry once after a moment.
- `terminal` — see [Spawning](#spawning-a-session).
- `corrupt`, `environment`, `io`, `internal` — stop and report to the user, quoting `.error.message`; don't edit sesshin's files to fix them.

## Keep output small

A whole session view is about 2 KB, so a bare `sesshin list` of a busy machine is tens of KB in your context. **Always pass `--fields` to `list`**, and `--limit` when you only need the newest few:

```sh
sesshin list --fields name,job,status,cwd                     # live sessions, newest first
sesshin list --liveness all --limit 10 --fields name,job,liveness,last_seen
sesshin list --limit 0 | jq .result.total                       # just the count
```

- Fields: `name`, `job`, `source`, `extra`, `headless`, `liveness`, `status`, `stall_reason`, `pending`, `attention`, `cwd`, `git_branch`, `model`, `permission_mode`, `entrypoint`, `nested`, `pid`, `pid_started_at`, `started_at`, `last_start_at`, `last_event_at`, `last_event_type`, `event_seq`, `last_seen`, `ended_at`, `end_reason`, `compactions`, `metrics`, `prompt_cache`, `placement`, `transcript_path`, `transcript_exists`. `id` and `session_id` are always included.
- A **chain** is the sessions one Claude process ran: `/clear` and `/new` end a session and start the next in the same process, so they share `pid` and `pid_started_at` (compare the two together; a pid alone is reused). To see what your window did before its last `/new`:

```sh
if me=$(sesshin show self | jq -ce 'select(.ok) | .result.session | [.pid, .pid_started_at]'); then
  sesshin list --liveness all --fields pid,pid_started_at,last_start_at,last_event_at,extra \
    | jq --argjson me "$me" '[.result.sessions[] | select([.pid, .pid_started_at] == $me)] | sort_by([.last_start_at, .last_event_at])'
else
  echo 'not inside a sesshin session' >&2
fi
```

- `--liveness live` (the default, which includes `unknown`), `ended`, or `all`. Headless sessions (`claude -p`, and sessions other sessions started) are hidden unless `--include-headless`.
- The result says `total` and `truncated`: when `truncated` is true there are `total` sessions and you got fewer. Say so.
- `sesshin show <id, UUID prefix, or job>` returns one session whole (`sesshin show self`, from inside a session, returns your own); add `--include-payload` only when you need the raw status-line payload (rate limits, say).

**Status** is what the session's last event said: `idle` (at its prompt, nothing done yet), `working`, `waiting` (its turn ended: it wants the user, unless `pending` shows background tasks or crons of its own), `needs_approval` (blocked on a permission prompt). An interrupted turn fires no hook, so a session can read `working` after Esc until its next event.

**Attention** sums that up as what the session wants from the user: `blocked` (a permission prompt, `AskUserQuestion`, or plan approval, shown about 6 seconds after it appears), `stalled` (its turn died of an API error), `your_turn` (finished, waiting), `self_waking` (paused on its own background tasks or a scheduled wakeup), `idle`, `working`, or `unknown`; `null` once ended. `sesshin list --fields name,job,attention` answers "which sessions need me?"

## Spawning a session

```sh
sesshin spawn --job api --cwd ~/code/api --prompt 'run the test suite and fix failures'
sesshin spawn --job review-142 --cwd . -- --model claude-sonnet-5-5 --permission-mode auto
gh issue view 42 --json body -q .body | sesshin spawn --job issue-42 --prompt-file -
```

`spawn` opens a new tab (in kitty or iTerm2) (`--type split` or `os-window` for the others) beside the user's window, without taking focus (in iTerm2, an `os-window` can bring iTerm2 in front of another app, and a tab flickers to the new tab and back), runs `claude` in it through the user's login shell, and waits up to `--start-timeout-secs` (default 15) for the session to start. `.result.session` is the new session's view, with its sesshin ID; `.result.placement` is the window.

- **Needs kitty with remote control, or iTerm2 on macOS,** run from inside a window of either, not under tmux or screen. In iTerm2, macOS must allow the app `sesshin` runs under to control it (System Settings → Privacy & Security → Automation); a refusal is `launch-failed` and says so. Otherwise it fails `terminal` with `reason: "unavailable"` (`unsupported` if the terminal can't launch, which kitty and iTerm2 can): tell the user; don't try another way to open a window.
- **The job** is letters (either case; `API` and `api` are the same job when holding it), digits, and hyphens, at most 64, not starting or ending with a hyphen, and not all digits (`12` always means a sesshin ID). With no `--job`, the session is unnamed: find it by its sesshin ID.
- **Everything after `--` goes to `claude` untouched**, before the prompt: `--model`, `--permission-mode`, and the like. The prompt is passed as one argument, so quotes, `$(…)`, and leading `-` are safe.
- **`--extra '<json object>'`** stores free-form data on the session, for whatever spawned it to find it again: `--extra '{"ticket":"auth-3"}'`, then `sesshin list --liveness all --fields job,extra | jq '.result.sessions[] | select(.extra.ticket == "auth-3")'`. sesshin never reads it. It belongs to that one session, is kept across `resume`, and is never inherited: a `/clear` or `/new` in the window starts the next session at `{}` (see Tagging a session). The job, not `extra`, is what names the window.
- **`--cwd`** defaults to the current directory. `--var KEY=VALUE` (repeatable) sets user variables on the window (kitty's `--var`, iTerm2's `user.<name>`), for matching it later; they are not environment variables.
- **The workspace-trust dialog.** A `claude` started in a directory it hasn't been trusted in waits at Claude's trust dialog, and no hook runs until the user accepts it. `spawn` then returns `session: null` with a `not-started` warning. That is not a failure: tell the user to accept the dialog in the new tab. The job stays reserved while that window is open.

**Retry safety.** After `invalid-input`, `not-found`, `conflict`, `busy`, or `terminal` with `reason` `unavailable`, `unsupported`, or `launch-failed`, nothing was launched: fixing the cause and rerunning is safe. After `terminal` with `reason: "launch-unknown"`, a `not-started` warning, or exit 3 / a signal, **a window may have opened**: never rerun `spawn` blind. Check first:

```sh
sesshin list --fields name,job,status,cwd,placement
```

Rerunning with the same job fails `job-taken` while the first is still starting; with no job, it opens a second window.

## Tagging a session

`extra` says what one session is working on, and only explicit acts set it: `spawn --extra`, and `sesshin update`. A window that works through several tasks with `/new` between them has one session per task, each starting at `{}`.

- **When you pick up new work after `/new`**, tag your own session, setting every key that still applies: `sesshin update self --extra-merge '{"ticket":"auth-4"}'`. `self` is the session the command runs in.
- **Tag another session** by ID, UUID prefix, or job: `sesshin update 12 --extra-merge '{"note":"waiting on review"}'`, `--extra-remove note` (repeatable) to delete a key, `--extra-replace-all '{}'` to clear it. Merges are shallow: a key's value is replaced whole.
- **`conflict` `no-sesshin-file`** means the session is too new to change yet (or, rarely, its sesshin ID is still pending). `.error.details.file` and the message say whether to retry after its next prompt or `resume` it first; `file: "other-format"` means its file waits for `sesshin migrate` (or a newer sesshin): tell the user, as for `migration-pending`. `update` is safe to retry.

## Bringing a session back

```sh
sesshin resume 12                                        # by sesshin ID, UUID prefix, or job (its last session)
sesshin resume api -- --permission-mode acceptEdits      # claude flags: --resume restores the conversation, not the flags
sesshin resume 12 --job api-old                          # when the session's own job is held by another
```

`resume` reopens an **ended** session with `claude --resume`, in a new tab beside the user's window (as `spawn`), in its own directory, under its title and job, and waits up to `--start-timeout-secs` (default 15) for it to be live again. A live session is refused (`conflict`, `rule: "live"`); one whose directory is gone is `not-found`; a `transcript-missing` warning means `claude --resume` will likely fail. Find the session first with `sesshin list --liveness ended --fields name,job,end_reason,ended_at --limit 10`.

**Never run `sesshin restart`.** It is a picker for a person: it draws fzf on the user's terminal and fails without one. When the user asks to bring back everything a reboot ended, suggest it to them (`sesshin restart`, type `killed`, ctrl-a, Enter; `-- <claude args>` for flags), or `resume` the sessions they name yourself.

**Never run `sesshin jump`** either: it is a picker for a person too, and it moves their window. When the user wants to find which session needs them (one blocked on a dialog, or finished and waiting), suggest it (a good key binding: `map kitty_mod+j launch --type=overlay /path/to/sesshin jump` in `kitty.conf`, with sesshin's full path: kitty's `PATH` may not have it). `sesshin jump` lists the live sessions with the ones that want them first, and its preview pane shows, for the session under the cursor, what it wants, what going there costs (its prompt cache and context), where it is, and what it is tagged with, before they focus it. Both pickers show each session's `extra`, so the user can type a tag (`auth-3`) to find the session that worked it, live in `jump` or ended in `restart`: one more reason to tag a session with `update`. To take them to a session they name, use `focus`.

## Sending text to a session

```sh
sesshin send api --text 'run the tests again'                  # by job, sesshin ID, or UUID prefix
sesshin send 12 --text-file notes.md
git diff | sesshin send api --text-file - --submit=false       # paste it, leave it in the input box
```

`send` types the text into a **live** session's window as one paste, then presses Enter (`--submit=false` skips the Enter). A job selects the live session holding it. It finds the window by the session's process, so a window that has moved or gone costs nothing.

- **It refuses a session mid-turn** (`conflict`, `rule: "mid-turn"`): text plus Enter could answer a dialog the session has up. Check first, and wait or tell the user if it is not `waiting` or `idle`: `sesshin show api | jq -r .result.session.status`. A session blocked on a permission prompt reads `needs_approval` and is the user's to answer. One the user interrupted (Esc) reads `working` until its next event; `--force` is right only if the user confirms it is sitting at its prompt.
- **`--force` only when the user wants to answer a prompt** (`sesshin send api --text yes --force`), never to get past a refusal. Say what you are sending.
- **Plain text only:** the text may hold tabs and line breaks (they arrive as one multi-line prompt), but no control characters (`invalid-input` at `/text`), and at most 1 MiB. Use `--text-file -` for text from another command.
- **An ended session is `conflict` `not-live`**; a session with no window known is `no-placement`; `terminal` with `reason: "unreachable"` means no window runs the session (nothing was typed). `reason: "unsupported"` means the session's terminal can't do it (nothing was done); it can't happen with kitty or iTerm2, which have every ability `send` needs.
- **Never retry after `send-failed` or `submit-failed`, or exit 3:** the text may already be in the input box, or sent, and a retry types it again. Look at `sesshin show … | jq .result.session.event_seq` against the `event_seq` the earlier call returned, or ask the user to look at the window.
- The result's `permission_mode` says whether the session acts without asking: say so when you prompt one that won't.

## Watching a spawned session

The user works in the session's tab. `sesshin focus api` (a job, sesshin ID, or UUID prefix) brings a live session's window to the front, with its tab and OS window; it changes what the user is looking at, so run it only when they ask to be taken there. `verified: false` in the result means no window was found running the session, and the stored window was focused, which may be another. `conflict` `not-live` or `no-placement` and `terminal` `focus-failed` (or `unsupported`, which kitty never gives) are its refusals; it is safe to retry. To follow a session without moving the user:

```sh
sesshin list --fields name,job,status,last_event_type,last_seen | jq -c '.result.sessions[] | select(.job == "api")'
sesshin show 12 | jq '.result.session | {status, pending, metrics}'
```

Poll sparingly (every 30 seconds or more, with `--fields`), and prefer a completion signal the session itself writes (a report file the prompt asks for) over watching `status`. A session that is `waiting` has finished its turn, which is not the same as finishing the work: it may have stopped on a question, a partial result, or an error, so read what it said before you act on it. One that is `needs_approval` needs the user.

## Pruning

`sesshin prune` removes ended sessions older than the retention window (by default 30 days, and 24 hours for headless sessions; `retain_days` and `retain_headless_hours` in sesshin's `config.toml`) and stale job reservations. **It deletes, so it asks for permission; never run it unasked.** `sesshin prune --dry-run` shows what it would remove and is safe.

## Hard rules

- **Never edit, create, or delete anything in sesshin's state directory** (`~/.local/state/sesshin` or `$XDG_STATE_HOME/sesshin`; on macOS `~/Library/Application Support/sesshin/state`) by hand, except one thing the user asks for: removing `reservations/<job>_*.json` (job lowercased) releases a job claimed by a spawn that never started.
- **Never run `sesshin restart` or `sesshin jump`:** they need the user's terminal. Suggest them; use `resume` and `focus` yourself.
- **Never run `install`, `uninstall`, `prune`, or `migrate` unasked** (offer `migrate`; the user decides), and never apply `install`'s proposal to `settings.json` yourself.
- **Before spawning on your own initiative, say so:** each spawn opens a window and starts a session the user pays for. When the user asked for parallel work, spawn what they asked for and no more.
- Don't rerun a `spawn` whose outcome is unknown; check `sesshin list`.
- **Never use `send --force` to get past a `mid-turn` refusal,** only to answer a prompt the user asked you to answer; and never rerun a `send` that failed `send-failed` or `submit-failed`.
