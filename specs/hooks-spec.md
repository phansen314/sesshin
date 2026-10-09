# sesshin hooks spec

How Claude Code tells sesshin anything. Each hook is `sesshin-hook <verb>`, run by Claude Code with a JSON payload on stdin. `sesshin-hook` is its own binary, installed beside `sesshin`, built from the standard library alone so that every hook starts as fast as a Go process can (see [Hook cost](design-spec.md#hook-cost)). It records what the payload says into the session's files ([State directory layout](design-spec.md#state-directory-layout)) and exits. Hooks are sesshin's only writers of tier-1 facts, and the busiest code sesshin has. The statusline is also the one place inside a session where sesshin shows what it records; [`list`](operations.md#list) and [`show`](operations.md#show) report every session.

Terms follow the design spec's [Terms](design-spec.md#terms).

## The contract

Every hook, on every path:

| | Guarantee | Why |
|---|---|---|
| H1 | **Exits 0.** Including on a panic, a malformed payload, an unusable file, a lock it could not get, and an unknown verb. | Claude Code reads exit 2 as a blocking decision: it refuses the tool call or the end of the turn, and feeds stderr into the conversation. Go's unrecovered panic exits 2 with a stack trace — the blocking code with the worst payload. |
| H2 | **Recovers first.** The panic handler is installed before argv, stdin, `hooks.properties`, or any file is touched. | A panic while reading a setting is still a panic. |
| H3 | **Silent.** Writes nothing to stdout or stderr. The [statusline](#statusline) is the one exception, and writes its lines once, at the end, from a buffer. | Claude Code adds `SessionStart`'s and `UserPromptSubmit`'s stdout to Claude's context, and parses some hooks' stdout as a decision; a torn statusline renders garbage. |
| H4 | **Bounded.** Waits at most `hook_lock_wait_ms` (default 2000, from [`hooks.properties`](design-spec.md#hook-settings)) for each lock, and at most twice that in all: every wait draws on one **lock deadline**, set when the hook starts, at twice `hook_lock_wait_ms` (`session-end`'s: `min(hook_lock_wait_ms, 1000)`). It starts at most one child process (the terminal backend's, in [`terminal-sync`](#terminal-sync)), with a timeout. | A hook that hangs hangs the session. |
| H5 | **No goroutines of sesshin's own,** and no `os.Exit` in the hook package. `terminal-sync`'s child runs through `os/exec` with its output in a buffer; the goroutines the standard library starts for that run no sesshin code. | A panic on another goroutine can't be recovered; `os.Exit` skips the deferred recover. |
| H6 | **Never fails a session's recording over one field.** A missing, wrongly typed, or unguarded field costs that field, never the write. | Payloads are a superset Claude Code varies by event and grows over time (see [Open sets](design-spec.md#open-sets)). |
| H7 | **Logs instead of failing,** to `<state>/hooks.log` (see [Log](#log)). | The only channel a hook has that reaches a person and not Claude. |

These are tested on the built binary, since an unrecovered panic's exit status can't be observed in-process.

## Registration

[`install`](operations.md#install) proposes these entries for Claude Code's `settings.json` ([Locations](design-spec.md#locations)), for you to apply, with `sesshin-hook`'s absolute path, so a hook needs nothing on `PATH` and no shim script (Claude Code still runs the command through `sh -c`):

| Event | Command | Mode | Why this mode |
|---|---|---|---|
| `SessionStart` | `sesshin-hook session-start` | blocking | The [pid lookup](design-spec.md#liveness) needs Claude as a live ancestor; an async hook can be reparented first. |
| `UserPromptSubmit` | `sesshin-hook user-prompt` | async | |
| `UserPromptSubmit` | `sesshin-hook terminal-sync` | async | Starts the terminal backend's process; kept out of `user-prompt` so a slow terminal can't delay the lifecycle write. |
| `PostToolUse` | `sesshin-hook post-tool-use` | async | |
| `PostToolUseFailure` | `sesshin-hook post-tool-use` | async | A session grinding through failing tools is busy, not silent. |
| `Stop` | `sesshin-hook stop` | async | |
| `StopFailure` | `sesshin-hook stop` | async | A turn killed by an API error fires only this; without it the session reads `working` forever. |
| `Notification` | `sesshin-hook notification` | async | Permission prompts and MCP elicitations. |
| `PreCompact` | `sesshin-hook compact` | async | Compaction fires no tool hooks; without these it looks like silence. |
| `PostCompact` | `sesshin-hook compact` | async | |
| `CwdChanged` | `sesshin-hook cwd-changed` | async | |
| `SessionEnd` | `sesshin-hook session-end` | blocking | An async hook can be killed as the session exits, and the death would be lost. Claude Code's exit budget for `SessionEnd` hooks is 1.5 seconds, shared by all of them; a longer hook `timeout` raises it, up to 60 seconds, as sesshin's does (Claude Code's hooks guide, Limitations). |
| *statusLine* | `sesshin-hook statusline` | Claude Code's `statusLine` command | Not a hook event; it renders the status line, and sesshin records the payload on the way. |

Tool events register with an empty matcher (every tool). Every entry has a `timeout` of 10 seconds: above any bound in [H4](#the-contract), so Claude Code's timeout never fires before sesshin's own.

In `settings.json`, each command is its own matcher group, with `"matcher": ""`, appended to the event's array; async entries have `"async": true`, and blocking ones omit the key. The command is run through `sh -c`, so the binary's path is single-quoted when it holds any character outside `A-Za-z0-9/._-`. `statusLine` takes no `timeout`:

```json
{
  "hooks": {
    "SessionStart": [
      { "matcher": "", "hooks": [{ "type": "command", "command": "/home/me/go/bin/sesshin-hook session-start", "timeout": 10 }] }
    ],
    "UserPromptSubmit": [
      { "matcher": "", "hooks": [{ "type": "command", "command": "/home/me/go/bin/sesshin-hook user-prompt", "timeout": 10, "async": true }] },
      { "matcher": "", "hooks": [{ "type": "command", "command": "/home/me/go/bin/sesshin-hook terminal-sync", "timeout": 10, "async": true }] }
    ]
  },
  "statusLine": { "type": "command", "command": "/home/me/go/bin/sesshin-hook statusline" }
}
```

**Proposing `settings.json`.** sesshin never writes `settings.json`. `install` and `uninstall` write a proposed copy of it ([`install`](operations.md#install)), which changes only sesshin's own entries:

- Every other key, entry, and group keeps its place and its value. Key order is preserved, and numbers are written back as they were read; the proposal is indented with two spaces.
- `uninstall` removes a matcher group its removal left with no hooks, and an event key left with no groups, and `hooks` when it is left empty.
- A `hooks` that isn't an object, an event's value that isn't an array, a group that isn't an object or whose `hooks` isn't an array, a `statusLine` that isn't an object, or a `permissions`, `permissions.allow`, or `permissions.ask` of the wrong shape (below), is [`corrupt`](operations.md#error-kinds), with `path` the settings file and `detail` naming the key.

**Permission rules.** `install` also proposes sesshin's permission rules, so an agent can run `sesshin` without a prompt, while the commands that change Claude Code's setup, delete sessions, or bring an ended session back still ask:

| Array | Rules |
|---|---|
| `permissions.allow` | `Bash(sesshin:*)` |
| `permissions.ask` | `Bash(sesshin install:*)`, `Bash(sesshin uninstall:*)`, `Bash(sesshin prune:*)`, `Bash(sesshin resume:*)` |

The `allow` rule covers `spawn` and `send`, by choice: an agent launches and drives sessions without asking, and that includes a `spawn` whose claude arguments skip permissions (`-- --dangerously-skip-permissions`), and a `send` to a session that already skips them. Anyone who wants an approval for those adds `ask` rules for them; `install` never removes a rule, so they stay. No rule is proposed for `jq`: one that allows it lets an agent read any file without a prompt, which is the user's to decide.

- **Added, never moved.** Each rule is appended to its array when that exact string isn't already in it; an array or `permissions` that doesn't exist is created. Nothing else in `permissions` is touched, `deny` and `defaultMode` included: a rule you deny stays denied, since Claude Code applies `deny` first, then `ask`, then `allow`.
- **sesshin's rules are the table's five.** `uninstall` removes every copy of each from the array the table puts it in (a sesshin rule you put in the other array is yours, and stays), then an array its removal emptied, and `permissions` when that leaves it empty. Any other rule is left alone: a `Bash(jq:*)` an earlier `install` proposed stays, since other tools may rely on it.
- A `permissions` that isn't an object, or an `allow` or `ask` that isn't an array, is `corrupt`, as for `hooks`. Items that aren't strings are left where they are.

Claude Code snapshots the hook configuration when a session starts: applying a proposal changes only the sessions started after it.

### Frozen verbs

The ten verbs of the [Registration](#registration) table (`session-start`, `user-prompt`, `terminal-sync`, `post-tool-use`, `stop`, `notification`, `compact`, `cwd-changed`, `session-end`, and `statusline`) are [stable](operations.md#versioning): every `settings.json` an `install` proposed names them, and Claude Code runs whatever it names, whichever `sesshin-hook` is installed now. So until 2.0 no verb is renamed or removed. A release that wants a new name adds it and keeps the old one as an alias, and one that stops registering an event keeps its verb as a no-op that exits `0` without logging. An unknown verb is for the other direction only: a `settings.json` written by a newer `install`, run by an older `sesshin-hook`. A test fails if any 1.0 verb stops dispatching.

## Hook template

Each hook below is specified with the same parts, in this order. An empty part is written `**Part:** none.`

| Part | Content |
|---|---|
| **Summary** | Unlabeled first paragraph: what the hook records, in a sentence or two. |
| **Events** | The Claude Code events it serves, from [Registration](#registration). |
| **Reads** | The payload fields and environment variables it uses, and the guard each passes. |
| **Writes** | The files it writes, the lock it holds for each, and the order of the steps. |
| **Effects** | What changes in the session's files, by field. Lifecycle hooks give their row of the [effects table](#effects-table). |
| **Degraded** | What it does when the payload, a file, or a lock is not as expected. |
| **Cost** | What it does on its hot path, and what it never does there. |

## Shared rules

### Reading the payload

- **One struct, every field optional.** The payload is decoded once; a field that is absent or of the wrong type is empty, and the rest still decode. `.model` is a string on lifecycle payloads and an object (`{id, …}`) on the statusline's; both shapes are read.
- **`session_id` must be a UUID** ([Session UUIDs](design-spec.md#session-uuids)), lowercased before use. Otherwise the hook records nothing and logs: the value would become a path.
- **Strings are scrubbed** before they are stored, each of these replaced by a space: C0 (U+0000–U+001F, line breaks among them), DEL (U+007F), C1 (U+0080–U+009F, NEL among them), U+2028, and U+2029. That is exactly what [`defs.text`](design-spec.md#file-schemas) refuses, so a scrubbed string always passes it: `cwd` and names reach the statusline, where a line break would start a new line of it, and the log, which is one line per entry.
- **Copied enums pass a shape guard,** `^[a-z][a-z0-9_]{0,63}$` (see [Open sets](design-spec.md#open-sets)): `.source`, `.reason`, `.trigger`, `.error`. `.permission_mode` passes `^[A-Za-z][A-Za-z0-9_]{0,63}$`, since Claude Code's modes are camelCase (`acceptEdits`). A value that fails is treated as absent.
- **`CLAUDE_CODE_ENTRYPOINT`** passes `^[a-z][a-z0-9_-]{0,63}$`, since its values have hyphens (`sdk-cli`). One that fails is stored as `null`.
- **`SESSHIN_JOB` and `SESSHIN_TOKEN`** are read from the hook's environment, which is Claude's, by every hook that can create `sesshin.json`. `SESSHIN_JOB` must be a [job name](design-spec.md#reservations) and `SESSHIN_TOKEN` 32 lowercase hex characters; one that isn't is ignored, as if unset, and logged only as the [Log](#log) says.
- **Empty stdin** is not a payload: the hook exits at once, silently. The [statusline](#statusline) still prints its [fallback line](#rendering).
- **A payload cut short, malformed, or followed by anything but whitespace** is recorded from what decoded before the fault, or, for data after the closing brace, from the whole object ([H6](#the-contract)), and logged once by every verb (`payload: <error>`): Claude Code never sends one, so one that arrives is worth seeing.

### Recording an event

Every lifecycle hook records its event the same way. This is the one place `lifecycle.json` is written; the hooks differ only in their [effects](#effects-table).

1. **Lock** the session directory, waiting up to `hook_lock_wait_ms`, within the hook's lock deadline ([H4](#the-contract)). Only a hook that can [adopt](#late-adoption) creates the directory when it doesn't exist, with any missing parents (`<state>`, `sessions/`): directories mode `0700`, and every file sesshin writes `0600`, since they hold paths and costs. With no usable `HOME`, a hook can't find the state directory, and exits 0 without writing or logging. If the wait runs out, log and stop: the event is lost. Once locked, check that the path still names the directory that was locked: a [prune](design-spec.md#retention) may have renamed it aside in between. If it doesn't, unlock and start this step again, once, with what is left of the deadline.
2. **Read** `lifecycle.json`:
   - **Missing:** this is a [late adoption](#late-adoption), or the session's first `SessionStart`. Start a new one.
   - **Corrupt:** start a new one, as if it were missing (see [Format versions](design-spec.md#format-versions)).
   - **In another format:** older, it waits for [`migrate`](design-spec.md#migrations); newer, it is a newer binary's. Either way it is left alone: unlock, and stop, with nothing written. The event is lost. Logged only as the [Log](#log) says.
   - **There, but not readable** (a read error other than its absence: a directory in its place, a permission denied, an I/O error, a file too large): an [outside change](design-spec.md#assumptions). Logged, and nothing is written: replacing a file that exists but can't be read would wipe its `event_seq`, `compactions`, and `session_title`, and a replacement may fail to write just the same. The session records nothing until the file can be read or is removed. sesshin never removes what it didn't create.
3. **Apply** the clocks, then the event's effects:
   - `last_event_at` = now, and `event_seq` + 1. Always, straggler or not.
   - `permission_mode` = the payload's, when it has one. Always.
   - The event's row of the [effects table](#effects-table), unless it is a [straggler](#straggler-guard).
4. **Write** `lifecycle.json` (temp file, rename).
5. **Complete or repair `sesshin.json`**, still under the session lock:
   - **Missing or corrupt:** [create it](#creating-sesshinjson) afresh, with a new ID ([Format versions](design-spec.md#format-versions)).
   - **In another format:** left alone, with no ID issued and no placement replaced, and logged as step 2's is. An older one waits for [`migrate`](design-spec.md#migrations), which keeps its ID.
   - **There, but not readable** (a directory in its place, a permission denied): logged, and left alone, with no ID issued. A replacement would fail to write just the same, after `state.json` had issued its ID, so every hook would use one up.
   - **`id` `null`:** [complete it](#creating-sesshinjson), keeping its placement.
6. **Unlock.**

A write that changes nothing is impossible: every event moves the clocks.

### Straggler guard

Only the events marked *guarded* in the [effects table](#effects-table) are checked for being a [straggler](design-spec.md#straggler-guard): a payload `prompt_id`, non-empty, that equals `ended_prompt_id`. The rest never change the status of an ended turn. A straggler moves the clocks and `permission_mode`, and nothing else.

### Late adoption

A lifecycle hook that finds no `lifecycle.json` under the lock — sesshin was installed mid-session, or `SessionStart`'s write was lost — creates it rather than drop the session for its whole life. The decision is made from the files, under the lock, so two hooks of the same unseen session can't both adopt it.

- `lifecycle.json` is created as a [new record](#a-new-lifecyclejson), with what this payload carries (`cwd`, `transcript_path`, `model`, `permission_mode`), the pid and `nested` found as [`session-start`](#session-start) finds them, and `entrypoint` from the hook's own environment. From an async hook Claude may no longer be an ancestor, leaving `pid` `null`; the statusline's own lookup then supplies it (see [Liveness](design-spec.md#liveness)).
- Its `status` is the event's, or `working` for an event that sets none (a compaction, or a `SessionStart` of source `compact`): the session is demonstrably mid-turn, and `idle`, which claims no turn is under way, would be the unsafe guess.
- `sesshin.json` is then created as for any new session ([Creating `sesshin.json`](#creating-sesshinjson)).

Every adopting hook, whichever its verb, reads `cwd`, `transcript_path`, and `model` from the payload, and `CLAUDE_PID`, `CLAUDE_CODE_ENTRYPOINT`, `SESSHIN_JOB`, `SESSHIN_TOKEN`, `TMUX`, `STY`, and the terminal backend's variables (for kitty, `KITTY_LISTEN_ON` and `KITTY_WINDOW_ID`) from the environment, besides what its own **Reads** names.

#### A new `lifecycle.json`

The first `SessionStart`, a late adoption, and the replacement of a corrupt file all start from the same record, before the event's own effects and clocks are applied:

| Field | Starts as |
|---|---|
| `session_id` | The payload's, lowercased. |
| `started_at`, `last_start_at` | Now. |
| `compactions` | `0`. |
| `event_seq` | `0`, so the event's +1 makes it `1`. |
| `status` | `working`; every event that sets a status overrides it. |
| Every other field | `null`. |

`session-end` and `cwd-changed` do not adopt: a session sesshin never saw that is ending has nothing worth recording, and a `cd` is not worth one. Neither creates a missing session directory, nor does `terminal-sync`; a directory created only to be locked would be left empty, with no `lifecycle.json`, and nothing removes it (see [Files](design-spec.md#files)). The [statusline](#statusline) does not adopt either: it is not a lifecycle event, and a `lifecycle.json` it created would be fabricated.

### Creating `sesshin.json`

Under the session lock, when `sesshin.json` is missing or corrupt, or its `id` is `null`:

**`extra`.** A file written **afresh** (missing or corrupt) starts with `extra` `{}`. The write that adopts a reservation (step 3, rule 2) sets `extra` to the reservation's, whether it creates the file or completes its pending `id`; a file completed without adopting one keeps its `extra`. `update` refuses a file whose `id` is `null`, so nothing has changed `extra` before an adoption, and there is nothing to merge (see [User-owned extra](design-spec.md#user-owned-extra)). No other hook write changes `extra`: every rewrite of `sesshin.json` (replacing the placement, adopting a resumed session's reservation, which only a file with an `id` does, the terminal sync) keeps it as it read it.

1. **Take the state lock,** waiting up to `hook_lock_wait_ms`, within the hook's lock deadline. If the wait runs out, [the ID can't be issued](#when-the-id-cant-be-issued).
2. **Issue the ID,** if `id` is `null`. Read `state.json`. If it is missing or corrupt while `sessions/` holds other sessions, start from the highest `id` in any `sesshin.json` instead of 0 (see [Sesshin IDs](design-spec.md#sesshin-ids)). A rebuild that finds a `sesshin.json` in another format, whose `id` it can't read, issues no ID until `migrate` has run: log `last_id not rebuilt: sesshin.json in another format` (only as the [Log](#log) says), and [the ID can't be issued](#when-the-id-cant-be-issued). If it exists but can't be read (a read error other than its absence: `EIO`, `EACCES`, a directory in its place), it may hold a `last_id` this hook can't see, and a rebuild could reuse IDs: log it, and [the ID can't be issued](#when-the-id-cant-be-issued). If it is in another format, only `migrate` or a newer binary may write it: log it as the [Log](#log) says, and the ID can't be issued. Write `state.json` with `last_id + 1`, flushed, and only then log a rebuild. Its `migration` is kept as read; a first run (no other session) writes this binary's latest [migration](design-spec.md#migrations) step, and a rebuild writes 0.
3. **Decide the job, `source`, and `extra`** by the [Adopt](design-spec.md#reservations) rules, once the ID is issued, when the file has no job yet (a pending file is written with none, and a resumed session's adoption never touches one without an `id`, so this holds unless a file was edited by hand; one that has a job keeps its `job`, `source`, and `extra`):
   1. `nested` is `true` in `lifecycle.json`: no job, and no reservation is opened or removed: the variables were inherited.
   2. `SESSHIN_TOKEN` is set, and the reservation it names is usable: `reservations/<key>_<token>.json` when `SESSHIN_JOB` is set, named by its [key](design-spec.md#reservations), else `reservations/<token>.json`. The hook opens that one file; it never scans. If it is [fresh](design-spec.md#reservations) (by age: a hook never asks the terminal), the job is `SESSHIN_JOB`, or none without one, `source` is `spawn`, and `extra` is the reservation's; the reservation is removed after step 4's write. A stale one is removed after the write too, and delivers nothing: the job is decided by rule 3, or rule 4 without `SESSHIN_JOB`.
   3. `SESSHIN_JOB` is set: the job is `SESSHIN_JOB` when no live session holds it and no fresh reservation of its key (a glob of `reservations/<key>_*.json`) names it, comparing [keys](design-spec.md#reservations): a live `API` holds `api`. This reads every session directory, as [`list`](operations.md#reading-the-sessions) does but with no warnings: `lifecycle.json`, `statusline.json`, and `sesshin.json`, judging [liveness](design-spec.md#liveness) and the job each reports by the readers' rule. A session whose liveness is `unknown` holds its job, but one with no usable `lifecycle.json` is skipped, as `list` skips it, and holds nothing. The hook's own session counts in judging liveness, so that Liveness rule 3 can supersede an earlier session of its process (a `/clear` whose `SessionEnd` was lost), but it never holds the job. If the job is held, log it (`job <name> held by #<id>`, or by a reservation): the session gets none.
   4. Otherwise no job.

   `source` is `spawn` when rule 2 adopted a reservation, else `hook`. `extra` is set as this section's opening paragraph says.
4. **Write `sesshin.json`,** remove the reservation rule 2 matched, if any, then release the state lock. The reservation is removed only once `sesshin.json` names the job, so that at every moment either it or a live session holds the job, and a `spawn` that claims the job after the lock is released never has its new reservation deleted. A removal that fails is logged; the reservation goes stale. Its placement is what the terminal backend recognizes in the environment, or `null` when the backend can't place it ([Placement](design-spec.md#placement); `nested` is read from `lifecycle.json`, so a later async hook needs no lookup of its own). A hook completing a file that already has a placement keeps it, except `session-start`, which replaces it as it does one that has an `id` ([`session-start`](#session-start)).

#### When the ID can't be issued

The state lock's wait ran out, `state.json` can't be read or written or is in another format, or `sessions/` can't be listed for a rebuild, or a rebuild finds a `sesshin.json` in another format. The cause is logged, and then: `sesshin.json` is written with `id` `null`, `job` `null`, `source` `hook`, the placement as in step 4, and `extra` `{}`, if it is missing or corrupt: the job and `extra` are decided only with an ID, by the hook that completes it; one that exists keeps its `null` `id`, and `session-start` replaces its placement as step 4 says, so a resume in another window isn't left with the last window's. Every other hook leaves it as it is. The next lifecycle hook completes it.

### Log

`<state>/hooks.log`: one line per entry, `<timestamp> <verb> <session-uuid or -> <message>`, appended with `O_APPEND` so concurrent hooks never interleave within a line. When the descriptor a hook opened shows the file past 1 MiB, the hook renames it to `hooks.log.1` (replacing the previous one) and starts a new file, but only if the path still names that same file and it is still past the limit. Two hooks that notice at once would otherwise both rename, and the second would move a fresh, nearly empty log over the full one.

**Another format is logged once per session start.** A file in [another format](design-spec.md#format-versions) is left alone by every hook, and only `session-start` logs it: `lifecycle.json`, `sesshin.json`, and `state.json` (`<file> in format <n>, not <m>; left alone`, and `last_id not rebuilt: sesshin.json in another format`), and an ignored `SESSHIN_JOB` or `SESSHIN_TOKEN`. The other verbs stay silent about them, so a busy session's every tool call and prompt doesn't add a line until `migrate` runs.

A hook that has a line to log when `<state>` doesn't exist yet (a malformed `session_id` on a fresh machine, say) creates `<state>`, mode `0700`, and `hooks.log` in it, and nothing else: whether a line is kept never depends on whether a `SessionStart` ran first.

Every message says what failed in one of three shapes, and none names the package that logged it: the verb column already does. `<op> <file>: <error>` is an OS error, with the OS's words for it and without the path (`read lifecycle.json: permission denied`, `write sesshin.json: no space left on device`, `open session directory: …`; a lock is `session lock: …` or `state lock: …`). `<file> unusable: <reason>` is a file that was read and failed its validation (`lifecycle.json unusable: …`), and `<file> in format <n>, not <m>; left alone` one in [another format](design-spec.md#format-versions). What the hook did about it may follow as a line of its own (`sesshin.json written without an id`, `sesshin.json keeps no id`, `sesshin.json not written`). These messages are fixed, and stay as written: `payload: <error>`, `payload not stored: <reason>`, `last_id rebuilt from <N>`, `last_id not rebuilt: sesshin.json in another format`, `statusline step <n> (<name>): panic: <value>`, `unknown verb`, `no verb`, `session_id is missing or not a UUID`.

A bad [`hooks.properties`](design-spec.md#hook-settings) is not logged: every hook would log it on every run, burying the rest. Hooks use the default; `sesshin install` reports it.

## Effects table

What each lifecycle event sets in `lifecycle.json`, beyond the clocks and `permission_mode` that [every event](#recording-an-event) sets. A dash means the field is left as it is.

| Event | Guarded | `status` | `last_event_type` | `ended_prompt_id` | `stall_reason` | `background_tasks`, `session_crons` | Other |
|---|---|---|---|---|---|---|---|
| `SessionStart` (`startup`, `resume`, `clear`, `fork`) | no | `idle` | `start`, or `start:<source>` unless `startup` | `null` | `null` | `null` | `last_start_at`; see [`session-start`](#session-start) |
| `SessionStart` (`compact`, or a source sesshin doesn't know) | no | — | `start:<source>` | — | — | — | `cwd`, `transcript_path`, `model` |
| `UserPromptSubmit` | no | `working` | `prompt` | `null` | `null` | `null` | `session_title`, when non-empty |
| `PostToolUse` | yes | `working` | `tool` | — | `null` | `null` | |
| `PostToolUseFailure` | yes | `working` | `toolfail` | — | `null` | `null` | |
| `Stop` | no | `waiting` | `stop` | payload's | `null` | counts from the payload | |
| `StopFailure` | no | `waiting` | `stopfail` | payload's | `.error`, or `unknown` | counts from the payload | |
| `Notification` `permission_prompt` | yes | `needs_approval` | `notify` | — | `null` | `null` | |
| `Notification` `elicitation_dialog` | yes | `needs_approval` | `elicit` | — | `null` | `null` | |
| `Notification` `elicitation_complete` | yes | `working` | `elicit_done` | — | `null` | `null` | |
| `PreCompact` | no | — | `precompact` | — | — | — | |
| `PostCompact` | no | — | `compact`, or `compact:<trigger>` | — | — | — | `compactions` + 1 |
| `SessionEnd` | no | — | `end`, or `end:<reason>` | — | — | — | `ended_at` = now, `end_reason` |

Notes on the table:

- **`idle` at start.** A session that has just started, resumed, cleared, or forked is at its prompt with no turn behind it. It isn't working, and it hasn't finished a turn for you to read, so `idle` never reads as having finished a turn.
- **A `SessionStart` of source `compact`** follows a compaction, often mid-turn, and a source sesshin doesn't know might too. It proves the session alive and says nothing about whose turn it is, so it leaves the status alone.
- **An interrupted turn records nothing:** the status stays as it was until the next event (see [Status](design-spec.md#status)).
- **Everything that starts or continues a turn clears `stall_reason`.** A prompt, a tool, or a dialog after a failed turn is the session recovering, and ⛔ must go.
- **The pending counts describe the moment a turn ended,** so anything that starts or continues a turn clears them. `null` means no turn has ended since the current one began; `0` means one ended with nothing pending.
- **`UserPromptSubmit` clears `ended_prompt_id`:** a new turn has begun, and nothing in it can be stale.
- **Compactions are never guarded** (see [Straggler guard](design-spec.md#straggler-guard)).
- **Other `Notification` types** (`agent_needs_input`, `auth_success`, …) record nothing, not even the clocks: their relation to sesshin's states isn't established, and a guessed status is the wrong status.
- **`CwdChanged` is not in the table.** A `cd` is not a lifecycle edge; it changes `cwd` without moving the clocks (see [`cwd-changed`](#cwd-changed)).

## Hooks

### session-start

Records that a session started, resumed, was cleared into, forked, or compacted: its process, its directory, and its place in sesshin (ID, job, placement). The only hook that creates a session in the normal course, and the one that captures the pid.

**Events:** `SessionStart`.

**Reads:** `session_id`, `source` (guarded), `cwd`, `transcript_path`, `model`, `permission_mode` (guarded). `SessionStart` carries no `permission_mode`, and no `model` under `claude -p` ([verified](design-spec.md#claude-code-21288)). Environment: `CLAUDE_PID`, `CLAUDE_CODE_ENTRYPOINT`, `SESSHIN_JOB`, `SESSHIN_TOKEN`, `TMUX`, `STY`, and whatever the terminal backend recognizes (for kitty, `KITTY_LISTEN_ON` and `KITTY_WINDOW_ID`).

**Writes,** in this order:

1. Find Claude's process, as in [Liveness](design-spec.md#liveness): `CLAUDE_PID` when it names an ancestor of this hook, else the first `claude` in a walk up the ancestry. Read its start time, and its initial environment: `CLAUDECODE` there means this session was started by another session. Done before any lock: it reads only the process table.
2. [Record the event](#recording-an-event), steps 1–5: lock the session directory, write `lifecycle.json`, and create or complete `sesshin.json` if it is missing, corrupt, or has no `id`. One in another format is left alone, with its placement.
3. If `sesshin.json` already existed and was usable, replace its placement (below), still under the session lock: whether it had an `id`, or had none and step 2 completed it, or [couldn't](#when-the-id-cant-be-issued). Then, only if it had an `id` before this hook, adopt a resumed session's reservation (below).
4. Release the session lock (Recording an event, step 6).

**Adopting a resumed session's reservation.** When `sesshin.json` already existed, was usable, and had an `id` before this hook (a pending file is left to the [Adopt rules](design-spec.md#reservations) at completion, which take a spawn's reservation with its `extra`, or a resume's, with `{}`), `SESSHIN_JOB` and `SESSHIN_TOKEN` are both set, and `reservations/<key>_<token>.json`, named by `SESSHIN_JOB`'s [key](design-spec.md#reservations) and the token, is usable: take the state lock, within the lock deadline, and read the reservation again. If its `token` still matches, remove it, after setting `sesshin.json`'s `job` to `SESSHIN_JOB` if it is [fresh](design-spec.md#reservations). `source` and `extra` are never changed: the session keeps its own `extra`, whatever the reservation holds. This completes a `resume`'s claim, since a resumed session never runs the Adopt rules ([Reservations](design-spec.md#reservations)). A `/clear` (or `/new`) or an in-session `/resume` inherits a token whose reservation is long gone, so it finds no match and takes no lock. A state lock not taken in time is logged, and changes nothing. A `sesshin.json` write that fails is logged, and the reservation is kept. A removal that fails is logged; the job stays set, and the reservation goes stale.

**Effects:**

- `lifecycle.json`: the [effects table](#effects-table) row, and also:
  - `cwd`, `transcript_path` = the payload's, when present.
  - `model` = the payload's, when present.
  - `pid`, `pid_started_at` = step 1's result, `null` when it found no Claude. For source `compact`, only when it found one.
  - `entrypoint` = `CLAUDE_CODE_ENTRYPOINT`, or `null` when unset; `nested` = step 1's answer, `null` when it had none. For source `compact`, only when known.
  - On a new session: [a new record](#a-new-lifecyclejson).
  - For `startup`, `resume`, `clear`, `fork`: `last_start_at` = now, and `ended_at` and `end_reason` = `null` (a resume revives the session). `started_at`, `compactions`, and `event_seq` carry on.
- `sesshin.json`:
  - Created if missing, as in [Creating `sesshin.json`](#creating-sesshinjson), its job decided there.
  - `job` = `SESSHIN_JOB`, for a resumed session that adopts its reservation (above).
  - `extra`: as [Creating `sesshin.json`](#creating-sesshinjson) says; a resumed session keeps its own.
  - `placement` = what the terminal backend recognizes in the environment, or `null`, replacing the old value except for keys only the backend's sync writes (for kitty, `tab_title` and `user_vars`): a resumed session may be in a new window. Those keys are kept as [Placement](design-spec.md#placement)'s Replaced with care says: for the same window, or for source `resume`. `null` when the backend can't place it ([Placement](design-spec.md#placement)).
- **Left as it was:** a field the payload doesn't carry keeps its value. `model`, `cwd`, and `transcript_path` are never cleared by a payload without them, and `permission_mode` is never set by this event, which doesn't carry it. For source `compact`, `entrypoint` follows `pid` and `nested`: it changes only when `CLAUDE_CODE_ENTRYPOINT` is set. A source that is missing, unknown, or fails the shape guard is status-neutral like `compact`; a missing or malformed source is recorded as `start`, with no qualifier. Every source replaces the placement, `compact` included, in a `sesshin.json` whose `id` is `null` as in one that has an `id`, also when step 2 couldn't issue it.

**Degraded:**

- Payload unparseable: whatever decoded is recorded. A missing `cwd` is stored as `null`.
- Claude's process not found: `pid` is `null`; the statusline's lookup supplies it later.
- Session lock not acquired: logged, and nothing is written. The session is [adopted late](#late-adoption) by its next lifecycle hook.
- State lock not acquired within the wait, or `state.json` unreadable: `sesshin.json` is written without an `id` (one that exists keeps its `null`, with this window's placement), logged, and the next lifecycle hook completes it.

**Cost:** a few process-table reads (one or two ancestry steps to `CLAUDE_PID`, its start time and environment), two or three small file writes, with `SESSHIN_JOB` set a read of every session directory and a start-time read per pid under the state lock (Adopt rule 3: a few hundred small files at most, with [retention](design-spec.md#retention)), and lock waits that together stay within the lock deadline: 4 seconds at the default `hook_lock_wait_ms`, 8 at its 4000 ms cap, against a 10-second `timeout`. These read `/proc` on Linux and `sysctl` on macOS, never `ps`.

### user-prompt

Records that a turn began, and captures the session's `/rename` title.

**Events:** `UserPromptSubmit`.

**Reads:** `session_id`, `permission_mode` (guarded), `session_title` (scrubbed).

**Writes:** `lifecycle.json`, under the session lock ([Recording an event](#recording-an-event)).

**Effects:** the [effects table](#effects-table) row. `session_title` = the payload's `session_title` when non-empty; an empty one leaves the stored title alone, since a session never renamed sends none.

**Degraded:** the shared rules.

**Cost:** one locked read-modify-write.

### post-tool-use

Records a tool result as activity: a session busy with tools is never silent.

**Events:** `PostToolUse`, `PostToolUseFailure`.

**Reads:** `session_id`, `hook_event_name`, `prompt_id`, `permission_mode` (guarded).

**Writes:** `lifecycle.json`, under the session lock ([Recording an event](#recording-an-event)). Every result is recorded, a subagent's included: there is no throttle.

**Effects:** the [effects table](#effects-table) row.

**Degraded:** the shared rules. An unreadable `hook_event_name` (missing, of the wrong type, or neither of the two) records as `PostToolUse`: the two rows differ only in `last_event_type`.

**Cost:** the hottest hook: one locked read-modify-write per tool call, about 1.25 ms, in an async hook nobody waits on (see [Hook cost](design-spec.md#hook-cost)). With one lock per session, the [measured](design-spec.md#hook-cost) wait at several times a busy session's rate is 18 µs.

### stop

Records that a turn ended: it wants you, it died of an API error, or it is waiting on its own background work.

**Events:** `Stop`, `StopFailure`.

**Reads:** `session_id`, `hook_event_name`, `prompt_id`, `permission_mode` (guarded), `error` (guarded), `background_tasks` and `session_crons` (arrays, counted; their contents are not read).

**Writes:** `lifecycle.json`, under the session lock ([Recording an event](#recording-an-event)). It never prunes: [`prune`](operations.md#prune) runs only when you run it (see [Retention](design-spec.md#retention)).

**Effects:** the [effects table](#effects-table) row. `background_tasks` and `session_crons` are the arrays' lengths; an array that is absent or not an array counts 0, never `null` (`StopFailure` carries neither, so it always records 0; [verified](design-spec.md#claude-code-21288)): `null` would claim no turn had ended, and 0 is the safe reading: the turn ended with nothing pending (see [Self-waking](design-spec.md#self-waking)).

**Degraded:** the shared rules. A `hook_event_name` that is missing, of the wrong type, or neither `Stop` nor `StopFailure` is unreadable, and records as `Stop`, unless the payload has an `.error`, which only `StopFailure` sends. A `StopFailure` with no usable `.error` still records `stall_reason` = `unknown`: the turn died either way.

**Cost:** one locked read-modify-write, in an async hook nobody waits on.

### notification

Records that the session is blocked on you — a permission prompt or an MCP elicitation — or that an elicitation was answered.

**Events:** `Notification`.

**Reads:** `session_id`, `notification_type`, `prompt_id`, `permission_mode` (guarded).

**Writes:** `lifecycle.json`, under the session lock, for the types in the [effects table](#effects-table). Any other type, `idle_prompt` included, returns before taking a lock or creating anything.

**Effects:** the effects table rows. `permission_prompt` fires only after several seconds without input, so it already means "the prompt is up and you were away".

**Degraded:** an unknown, missing, or wrongly typed `notification_type` is not an error and is not logged: Claude Code sends types sesshin deliberately ignores.

**Cost:** one locked read-modify-write, when it writes.

### compact

Records a compaction: proof the session is alive while no tools fire.

**Events:** `PreCompact`, `PostCompact`.

**Reads:** `session_id`, `hook_event_name`, `trigger` (`manual` or `auto`, guarded), `permission_mode` (guarded).

**Writes:** `lifecycle.json`, under the session lock ([Recording an event](#recording-an-event)).

**Effects:** the [effects table](#effects-table) rows. `compactions` counts on `PostCompact` only, so one compaction counts once.

**Degraded:** an unreadable `hook_event_name` (missing, of the wrong type, or neither of the two) records as `PreCompact`: the clock moves, and the count misses one. Losing the clock would be worse — this hook exists because a compacting session otherwise looks silent.

**Cost:** one locked read-modify-write.

### cwd-changed

Records the session's new working directory.

**Events:** `CwdChanged`.

**Reads:** `session_id`, `new_cwd` (scrubbed). Not `cwd`, which is where it was.

**Writes:** `lifecycle.json`, under the session lock — but not through [Recording an event](#recording-an-event): the clocks don't move.

**Effects:** `cwd` = `new_cwd`. Nothing else.

**Degraded:** an empty `new_cwd` writes nothing, and takes no lock: a stale `cwd` is corrected by the next change, a wrong one is wrong until then. No `lifecycle.json`: nothing written (the next lifecycle event adopts the session). A `lifecycle.json` that is corrupt or unreadable: nothing written, and logged; the next lifecycle event replaces a corrupt one. One in another format: nothing written, and not logged. A `new_cwd` equal to the stored `cwd`: nothing written. Session lock not acquired within the lock deadline: logged, and the change is lost.

**Cost:** one locked read-modify-write.

### session-end

Records the one death a hook reports, and why.

**Events:** `SessionEnd`.

**Reads:** `session_id`, `reason` (guarded), `permission_mode` (guarded).

**Writes:**

`lifecycle.json`, under the session lock ([Recording an event](#recording-an-event)), unless it doesn't exist. Like every lifecycle hook, it then completes or repairs `sesshin.json` (step 5): a session whose `sesshin.json` is missing gets its sesshin ID even as it ends.

**Effects:** the [effects table](#effects-table) row. `end_reason` = `.reason` (`clear`, `resume`, `logout`, `prompt_input_exit`, `other`); a reason that fails the guard costs the qualifier, `end` and `null`, never the write.

**Degraded:** no `lifecycle.json`: nothing is written; sesshin never knew this session. Session lock not acquired within its lock deadline, `min(hook_lock_wait_ms, 1000)` for all its waits together, shorter than the other hooks' so it gives up inside Claude Code's default exit budget: logged; the session reads as ended anyway once its process is gone, or, after a `/clear` or `/resume` in the same process, once the next session starts ([Liveness](design-spec.md#liveness)), just without a reason.

**Cost:** one locked read-modify-write. It runs under Claude Code's exit budget for `SessionEnd` hooks — 1.5 seconds by default, raised by the registration's 10-second `timeout` — and on `/clear` Claude Code waits for it before the new session's `SessionStart`, so it must stay short.

### terminal-sync

Lets the terminal backend record what can only be learned while the session is alive. For kitty: the tab's title and the window's user variables, which can't be asked once the session has ended (see [Placement](design-spec.md#placement)).

**Events:** `UserPromptSubmit` (its own registration, beside [`user-prompt`](#user-prompt)).

**Reads:** `session_id`. Environment: whatever the backend recognizes.

**Writes:** `sesshin.json`, under the session lock, only the backend's own keys in `placement`, and only when they changed. Nothing when `placement` is `null`. In order: recognize the window from the hook's own environment (`KITTY_LISTEN_ON` and `KITTY_WINDOW_ID`; nothing to do when either is missing or invalid, or under `TMUX` or `STY`, as for [placement](design-spec.md#placement)), run `kitten @ ls` with no lock held, then lock and write. The socket and window come from the environment, and the keys are written only when the stored placement is a valid kitty placement naming that same socket and window. A stored placement for another window or socket is left alone: the session moved, and its next `session-start` replaces it. It writes `sesshin.json` only, never `lifecycle.json` ([Two tiers](design-spec.md#two-tiers)). It never adopts and never creates the session directory, or `sesshin.json`. An unusable `sesshin.json` is logged and left as it is.

**Effects:** for kitty, from one `kitten @ ls` on `KITTY_LISTEN_ON`: `placement.tab_title` = the title of the tab holding `KITTY_WINDOW_ID`, and `placement.user_vars` = that window's user variables (`{}` when it has none).

**Degraded:** every failure — no backend recognized, no `kitten`, a timeout (1 second), no such window, no session directory, no `sesshin.json`, an unusable `sesshin.json`, a session lock not taken within the lock deadline — writes nothing, and is silent except for three, which are logged: the timeout, a corrupt (or unreadable) `sesshin.json`, which is left as it is, and a lock wait that ran out. One in another format is left as it is, and not logged ([Log](#log)). What this records is cosmetic.

**Cost:** the one hook that starts another process. It runs async, in its own process, so neither the prompt nor the lifecycle write waits on it; the lock is taken only after `kitten` returns, and only to write.

### statusline

Records the statusline payload and renders Claude Code's status line: sesshin's view inside a session, showing this session's data and nothing about any other session.

**Events:** Claude Code's `statusLine` command, not a hook event. Claude Code runs it as a child process when the UI changes: a new assistant message, a finished `/compact`, a permission-mode change, a prompt-cache expiry, debounced to 300 ms. It doesn't run during a long tool call, and never on a timer: an idle session gets no tick until its prompt cache expires ([verified](design-spec.md#claude-code-21288)).

**Reads:** the whole payload, and the session's `sesshin.json` for its sesshin ID (no lock; a tier-2 value read for display, never written into a tier-1 file, per [Two tiers](design-spec.md#two-tiers)). For rendering: `session_id`, `session_name`, `model.id`, `cwd`, `cost.total_cost_usd`, `cost.total_api_duration_ms`, `context_window.total_input_tokens`, `context_window.context_window_size`, `context_window.used_percentage`, `rate_limits.five_hour` and `rate_limits.seven_day` (`used_percentage`, `resets_at`), `prompt_cache` (`warm`, `caching_observed`, `expires_at`, `recache_tokens_if_cold`). From `lifecycle.json`, `session_title`, when the file can be read.

**Writes:**

1. Read the previous `statusline.json`, for `cost_sample`.
2. Find Claude's process and its start time, as [`session-start`](#session-start) does. Never reused from the previous tick: after a `/clear` and a `claude --resume` elsewhere, the previous tick's process may be alive but no longer this session's.
3. Find `git_branch`: walk up from `cwd` to a `.git` directory or file and read `HEAD`; a `.git` file's `gitdir:` line names the directory holding it. No `git` process.
4. Compute `burn_usd_per_hour` from the previous `cost_sample`, then carry the sample forward or replace it (see [`statusline.json`](design-spec.md#statuslinejson)).
5. Render the status line into a buffer ([Rendering](#rendering)), from the payload and what steps 1–4 found.
6. If the session directory has a usable `lifecycle.json`, write `statusline.json`, with no lock: write the temp file, re-read the stored `received_ns`, and rename only if the stored one isn't newer than this tick's (a stored file that is corrupt, or has none, counts as older, and so does one whose `received_ns` is more than a minute ahead of this tick's: it was written before the wall clock stepped back, and would otherwise freeze the file until the clock caught up; overlapping ticks are about 300 ms apart). Otherwise remove the temp file: a newer tick has already landed. A stored file in [another format](design-spec.md#format-versions) is left alone, and nothing is written, as with a `lifecycle.json` in another format.
7. Print the buffer.

Each of steps 1–4 and 6 recovers its own panic and logs it (`statusline step <n> (<name>): panic: <value>`), so a panic costs that step's result and never the line: after a panic in step 1 there is no previous tick; in step 2, `pid` and `pid_started_at` are `null`; in step 3, `git_branch` is; in step 4, so are `cost_sample` and `burn_usd_per_hour`; in step 6, nothing is written, and a temp file already made is removed. A panic in step 5 discards the buffer and prints the [fallback line](#rendering) instead, and is logged (`statusline step 5 (render): panic: <value>`).

**Effects:** `statusline.json` replaced: `received_at` = now, `received_ns` = the tick's start in Unix nanoseconds, `payload` = the payload verbatim, `git_branch`, `cost_sample`, `burn_usd_per_hour`, `pid`, `pid_started_at`.

**Degraded:** a payload that is unparseable, or can't be stored (invalid UTF-8, nested too deep; see [JSON reading](implementation-spec.md#json-reading)), renders what decoded and writes nothing: a verbatim copy of a bad payload records nothing useful. No `lifecycle.json`: renders, writes nothing (the statusline does not [adopt](#late-adoption)). A `lifecycle.json` that is unusable: the same, and logged when it is corrupt (a missing one, or one in another format, is not; see [Log](#log)). A payload that couldn't be decoded is logged once, by the [shared rule](#reading-the-payload) (`payload: <error>`), and the statusline doesn't log it again; it logs only a payload that decoded but can't be stored (`payload not stored: <reason>`), as it does a `statusline.json` that is corrupt (it is replaced). A failed write: renders, logged. No usable `sesshin.json`, or an `id` of `null`: the sesshin ID segment is hidden.

**Cost:** the session directory opened once, four small file reads (`statusline.json` twice, for step 1 and for step 6's re-read; `lifecycle.json` once, for the title and the check in step 6; `sesshin.json`), one small file write, a `.git` walk, a few process-table reads, no lock. An unchanged payload is written like any other: the write is the cheap part of the hook ([Hook cost](design-spec.md#hook-cost)), and a cache that skipped it could skip a write that never landed.

#### Rendering

Two lines of emoji segments. Segments are joined with ` | `; line 2 is printed on its own line only when it has a segment. There is **no trailing newline**: Claude Code renders the output verbatim, and a trailing newline is a blank line in its UI.

```text
#12 | ⬢ api refactor | 🧠 35% 351k/1M | ♨️ until 3:04PM | 📁 sesshin | 🌿 main | 🤖 Opus 5.5 | 💰 $1.20 | 🔥 $3.40/h | ⌛ 12m API
⏱️ 5h 22% resets 3:00PM | 7d 41% resets 10/6 9:00AM
```

**Line 1**, in this order:

| Segment | Shows | Hidden when |
|---|---|---|
| `#12` | The session's [sesshin ID](design-spec.md#sesshin-ids), from `sesshin.json`. The one segment with no emoji: `#` already marks it. | No `sesshin.json`, or `id` `null`. |
| `⬢ <name>` | The `/rename` title (`session_title`), else `session_name`. Not the full [name](design-spec.md#terms) rule: its last fallbacks are the sesshin ID and the UUID, and the ID has its own segment. | Neither is set. |
| `🧠 35% 351k/1M` | `used_percentage`, rounded, then `total_input_tokens` / `context_window_size` in `k` and `M`. | **Never.** The one segment that always renders: before the first API response it reads `🧠 0%`, which says "not started yet" where a missing segment would look broken. The tokens are hidden when either is missing. |
| `♨️ until 3:04PM` / `🧊 ~45k` | The derived [prompt cache](design-spec.md#prompt-cache) state. Warm: when it expires, `expires_at` as the local time of day. A clock time, not the time left, because an idle session gets no tick until the cache expires: a countdown would freeze at its last value, while the expiry time stays true until the expiry tick replaces it. Warm shows no date, even when the expiry falls on another day. Cold: about how many tokens the next request re-caches, from `recache_tokens_if_cold`, or `🧊 cold` without it. Without it means missing, not a number, or negative; zero is shown as `🧊 ~0`. | `unknown`. |
| `📁 <dir>` | The last path element of `cwd`. | No `cwd`. |
| `🌿 <branch>` | `git_branch`. | Not in a repository. |
| `🤖 <model>` | `model.id` as "Family Version", by rule, never from a list of known families: (1) keep what follows the last `claude-`, or the whole id when there is none; (2) cut at the first `[`, and at the first `-` followed by eight digits (a date); (3) split the rest on `-`; (4) the first token that is not all digits, title-cased, is the family; (5) the all-digit tokens, joined with `.`, are the version; other tokens are dropped. `claude-opus-5-5` is `Opus 5.5`; `us.anthropic.claude-opus-4-20250514-v1:0` is `Opus 4`; `claude-sonnet-4-6[1m]` is `Sonnet 4.6`; `claude-3-5-sonnet-20241022` is `Sonnet 3.5`; `claude-opus` is `Opus`. | No model, or no family token. |
| `💰 $1.20` | `total_cost_usd`, two decimals. | Not a number. |
| `🔥 $3.40/h` | `burn_usd_per_hour`, two decimals. | `null`. |
| `⌛ 12m API` | `total_api_duration_ms` as `Mm`, or `HhMm` from an hour. | Zero or missing: a session that has made no API call shows nothing, not `0m`. |

**Line 2**, the rate limits. They are per account, not per session, but they are what Claude Code reports to this session, so they are shown:

| Segment | Shows | Hidden when |
|---|---|---|
| `⏱️ 5h 22% resets 3:00PM` | `five_hour.used_percentage`, rounded; then `resets_at`, a Unix time, as the local time of day. | The percentage is missing. `resets …` alone is hidden when `resets_at` isn't a number. |
| `7d 41% resets 10/6 9:00AM` | `seven_day`, the same way, with the reset as month/day and time. | As above. |

**Number formats:**

- **Percentages** are rounded half away from zero (Go's `math.Round`), with no decimals. A result of zero is `0%`, never `-0%`.
- **Token counts** (`351k/1M`, `🧊 ~45k`): the count is first rounded to a whole number. Below 1,000 as the integer; below 999,500 as `round(n / 1000)` followed by `k`; otherwise `n / 1,000,000` rounded half away from zero to one decimal, followed by `M`, with a trailing `.0` dropped (`1M`, `1.5M`). A negative count is not a count: it hides the `🧠` tokens.
- **Durations** (`⌛`) are floored to whole minutes: `Mm` below an hour, `HhMm` from an hour (`1h5m`). A duration above zero and under a minute is `<1m`.
- **Times of day** (`resets`, `♨️ until`) are in the local time zone, 12-hour, with no leading zero and no space: `3:00PM`. Midnight is `12:00AM` and noon `12:00PM`. Dates are `M/D`. A Unix time with a fractional part is truncated to the second. The `5h` reset shows no date, even when it falls on another day.
- **Line 2's `⏱️`** prefixes whichever rate-limit segment comes first, so `7d` gets it when `5h` is hidden.

**Rules for every segment:**

- **Absent is not zero.** A number that is missing or not a number hides its segment rather than rendering `0`: "0%" and "not reported" are different claims. `🧠` is the one exception, above.
- **Rendered from what decoded.** Whenever there is a session to render for, the line is rendered on every path, including a payload that only partly decoded and a tick that writes nothing. Where there is none, it is the fallback line. A wrong render lasts one tick; nothing is ever stored from a render.
- **Printed once, from a buffer** ([H3](#the-contract)), so a panic mid-render prints the fallback line, never half of one.
- **The fallback line** is `#<id> | 🧠 0%` when the sesshin ID was read, else `🧠 0%`. It is printed when stdin is empty, when there is no usable `session_id` or no usable `HOME`, when rendering itself panics, and when a panic comes before the statusline's own frame: `main`'s recover prints it then. With empty stdin, no usable `session_id`, or no usable `HOME`, there is no session to read an ID from, so it is `🧠 0%`.
- **Names, directories, and ids are shown as stored.** An empty `session_title` counts as unset. The `📁` directory is the last element of `cwd` with trailing slashes ignored, and `/` for a `cwd` of only slashes. The model family is title-cased by its first letter upper-case and the rest lower-case. Empty tokens between two `-` are dropped. An unusable `sesshin.json` or `lifecycle.json` hides its segment and is not logged by the render: a `lifecycle.json` that is unusable is logged by step 6.
- **Presentation, not contract.** The layout may change in any release. Scripts read `statusline.json`, never the rendered line.

Adding a segment is a renderer change, not a data change: the payload is stored verbatim, so anything Claude Code reports is already on disk (see [Why verbatim?](design-spec.md#statuslinejson)).
