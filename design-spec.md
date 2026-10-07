# sesshin design spec

## Goals

Record every Claude Code session on one machine — which are working, which are waiting on you, where they live in kitty, what they have cost — with the filesystem as the database: no daemon, no SQLite, no server.

- **The data is the product.** sesshin's value is what it collects and how it lays it out on disk: one directory per session, a small JSON file per writer, readable with `jq`. sesshin records what Claude Code reports through its hooks and statusline, and derives everything else at read time.
- **Never in Claude's way.** A hook never blocks, delays past a bound, or fails a Claude Code session, whatever state sesshin's files are in (see [The contract](hooks-spec.md#the-contract)).

sesshin is two binaries: `sesshin-hook`, which Claude Code runs for every hook ([hooks-spec.md](hooks-spec.md); [Hook cost](#hook-cost) says why it is separate), and `sesshin`, for the commands ([operations.md](operations.md), [cli-spec.md](cli-spec.md)). The views are each session's status line, which shows that session's data and no other's ([`statusline`](hooks-spec.md#statusline)), and [`list`](operations.md#list) and [`show`](operations.md#show), which report every session as JSON, for `jq` and agents. The actions are [`spawn`](operations.md#spawn), [`resume`](operations.md#resume), [`send`](operations.md#send), and the picker [`restart`](picker-spec.md#restart). The rest is upkeep: [`install`](operations.md#install), [`uninstall`](operations.md#uninstall), [`prune`](operations.md#prune), and [`version`](operations.md#version). The other commands (`focus`, and the rest) are [deferred](deferred/README.md).

## Non-goals

- **Notifying you.** sesshin judges no session as needing you, and pushes nothing anywhere. Claude Code's bell and kitty's tab flag stay the ambient channel.
- **History.** sesshin keeps each session's *latest* state, not a log of events. The transcript (`transcript_path`) is the history; [`event_seq`](#event-ordinal) tells a reader when it missed something and should go there.
- **More than one machine, or more than one user.** See [Assumptions](#assumptions).
- **Terminals other than kitty.** Placement is kitty-only, behind one seam ([Placement](#placement)) so another backend could be added without touching the rest.

## Assumptions

- **sesshin is the only writer under the state directory.** Every file under it is created, changed, and removed by sesshin. Any change made another way is an **outside change**, outside the contract, except removing a reservation, which releases its job (see [Reservations](#reservations)).
- **One user, one machine.** sesshin serves a single OS user's Claude Code sessions, all on the machine sesshin runs on. The state directory is per machine and is not meant to be synced or committed: it names pids, which mean nothing anywhere else.
- **The state directory is on a local filesystem,** so `flock` and `rename` behave as specified (see [Concurrency](#concurrency)).
- **Claude Code's payloads are a superset that grows.** Every payload field sesshin reads is optional, and every enum it records (a status source, an end reason) is an open set: sesshin keeps a value it has never seen rather than dropping it (see [Open sets](#open-sets)).
- **Claude Code marks its children's environment.** Claude sets `CLAUDE_PID` (its own pid) and `CLAUDECODE=1` in the environment of every process it starts, hooks and the statusline included. `CLAUDE_PID` names Claude, and `CLAUDECODE` is in Claude's own environment only when another session started it ([verified](#claude-code-21288); see [Open questions](#open-questions)). sesshin uses the first to find Claude's process and the second to tell a session started by another session, and falls back to the process tree when either is missing (see [Liveness](#liveness)).
- **Hidden entries are ignored.** Any entry under the state directory whose name starts with `.` is ignored by every read. They are sesshin's own temp files and pruned directories (see [Files](#files)).

## Supported platforms

Linux and macOS (macOS not yet built: #47), on amd64 and arm64. Windows is never supported.

## Terms

| Term | Meaning |
|---|---|
| **session** | One Claude Code session, identified by Claude's session UUID. A `claude --resume` of it is the same session; a `/clear` starts a new one. |
| **state directory** | The directory holding everything sesshin records (see [Locations](#locations)). |
| **session directory** | `sessions/<uuid>/` under the state directory: one session's files. |
| **sesshin ID** | A short integer sesshin gives each session, shown as `#12` (see [Sesshin IDs](#sesshin-ids)). Stored in `sesshin.json`; the directory is still named by the UUID. |
| **job** | A name you give a session, unique among live sessions, stored in `sesshin.json` (see [Reservations](#reservations)). Set when sesshin first records the session, by the [Adopt](#reservations) rules, from `SESSHIN_JOB` in Claude's environment (`SESSHIN_JOB=api claude`), or from a reservation `spawn` or `resume` made. |
| **reservation** | A job claimed by [`spawn`](operations.md#spawn) or [`resume`](operations.md#resume) before the session it launches has a UUID: `reservations/<key>.json`, named by the job's [key](#reservations) (see [Reservations](#reservations)). |
| **fresh / stale** | Whether a reservation still holds its job (see [Reservations](#reservations)). |
| **held** | A job is held by a live session that reports it, or by a fresh reservation (see [Reservations](#reservations)). Jobs that differ only in case are one job: holding `API` holds `api`. |
| **name** | How a session is shown: its `/rename` or `claude --name` title (`session_title`; [`spawn`](operations.md#spawn) passes its job as `--name`), else Claude's session name from the statusline payload, else `#<sesshin ID>`, else the first 8 characters of its UUID. |
| **headless** | A session no one is typing into: one started by another session (`nested`), or one whose `entrypoint` is an SDK's (`sdk-…`, as `claude -p` reports). Derived from `lifecycle.json`. Pruned sooner (see [Retention](#retention)). |
| **live / ended / unknown** | A session's liveness, always derived, never stored: unknown when it can't be judged (see [Liveness](#liveness)). |
| **last seen** | The later of `last_event_at` and the statusline's `received_at`: when sesshin last heard from a session (see [Liveness](#liveness)). |
| **prunable** | An ended session last seen longer ago than its retention window (see [Retention](#retention)). |
| **status** | What Claude last said the session is doing: `idle`, `working`, `waiting`, `needs_approval` (see [Status](#status)). Meaningful only while live. |
| **lifecycle event** | A hook invocation that records a semantic edge: start, prompt, tool result, stop, notification, compact, end. Not the statusline. |
| **activity clock** | `last_event_at`: when the last lifecycle event was recorded (see [The two clocks](#the-two-clocks)). |
| **tier 1 / tier 2** | Facts that would be true without sesshin (Claude's) / sesshin's own relationship to a session (see [Two tiers](#two-tiers)). |
| **outside change** | Any change under the state directory not made by sesshin. |
| **read / setup / write** | The kinds of [operation](operations.md#operation-kinds). Hooks are not operations, but they write. |

## Locations

| What | Linux | macOS |
|---|---|---|
| **Config directory** | `$XDG_CONFIG_HOME/sesshin`, else `~/.config/sesshin` | `~/Library/Application Support/sesshin` |
| **State directory** | `$XDG_STATE_HOME/sesshin`, else `~/.local/state/sesshin` | `~/Library/Application Support/sesshin/state` |
| **Claude Code's settings** | `$CLAUDE_CONFIG_DIR/settings.json`, else `~/.claude/settings.json` | the same |

An XDG variable, or `CLAUDE_CONFIG_DIR`, is used only when set to an absolute path, as the XDG Base Directory spec requires. `~` is `HOME`, which must be absolute; without it, every operation that needs a location fails with [`environment`](operations.md#error-kinds).

The config directory holds `config.toml`, read by `sesshin`, and `hooks.properties`, read by `sesshin-hook` (see [Configuration](#configuration)). The state directory holds what sesshin records.

Every entry point resolves these locations from its own environment, and hooks inherit Claude's. A CLI run from cron, ssh, or an IDE can resolve different ones and silently read other state. [`install`](operations.md#install) records the locations it resolved, so `uninstall` proposes changes to the same settings file.

There is no other per-machine state: no database, no socket, no pid file, no lock file, no runtime directory.

## Data model

### State directory layout

```text
<state>/
  state.json                    root metadata: format version, last_id, migration
  install.json                  what install proposed, for uninstall
  settings.proposed.json        install's or uninstall's proposed settings.json, for you to apply
  hooks.log, hooks.log.1        the hook log (see hooks-spec Log)
  reservations/
    <key>.json                  a job claimed by spawn or resume, not yet adopted; key = job lowercased
  sessions/
    <uuid>/
      lifecycle.json            tier 1: lifecycle — written by lifecycle hooks
      statusline.json           tier 1: metrics — written by the statusline
      sesshin.json              tier 2: id, job, source, placement — written by sesshin

```

A session directory exists from the session's first `SessionStart`, or its [late adoption](hooks-spec.md#late-adoption), until it is [pruned](#retention). Hooks that don't adopt never create one. Each of its files has a fixed set of writers:

| File | Writers | Lock | Absent means |
|---|---|---|---|
| `lifecycle.json` | The session's lifecycle hooks | [session lock](#locks) | The session directory is not a session; skipped. |
| `statusline.json` | The session's statusline | none: a write that would replace a newer payload is skipped | No statusline tick yet: no metrics. |
| `sesshin.json` | `SessionStart` and [late adoption](hooks-spec.md#late-adoption) (create, issue the ID), any lifecycle hook ([completion, repair](hooks-spec.md#recording-an-event)), `terminal-sync` | session lock | No sesshin ID yet, no job, no placement. |

> **Why split a session across files?** The statusline must never move the activity clock: it writes far more often than anything else, and a clock it touched would make every session read as busy. The split makes that rule structural: the activity clock lives in `lifecycle.json`, and the statusline never writes that file (it only reads the `/rename` title from it). The split also puts the [tier](#two-tiers) boundary on disk, and lets the most frequent writer take no lock at all.

### Session UUIDs

A session directory is named by Claude's session UUID, lowercased:

```text
^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$
```

A payload `session_id` that does not match is never used as a path: the hook records nothing and logs it (see [Reading the payload](hooks-spec.md#reading-the-payload)). The directory name always equals `session_id` in `lifecycle.json`.

### File fields

Each file sesshin writes is described below, field by field: its JSON type, where the value comes from, and why sesshin keeps it. The [File schemas](#file-schemas) are the normative form of the same fields. Every JSON file starts with `schema`, its format version (see [Format versions](#format-versions)), written by whoever writes the file; the tables leave it out. `settings.proposed.json` is the exception: it is Claude Code's `settings.json` as sesshin proposes it, in Claude Code's format, with no `schema` and no schema of sesshin's. "Timestamp" is a [timestamp](#timestamps) string, and "open set" an [open set](#open-sets) of lowercase values.

#### `state.json`

The state directory's root metadata. Written only when a sesshin ID is issued, or by [`migrate`](operations.md#migrate), under the [state lock](#locks), and flushed (see [Sesshin IDs](#sesshin-ids)).

| Field | Type | Source | Notes |
|---|---|---|---|
| `last_id` | integer ≥ 0 | +1 by each hook that issues an ID: creating `sesshin.json`, completing a pending `id`, or reissuing one that couldn't be read | The highest sesshin ID ever issued; never decreases. Missing or corrupt while sessions exist: rebuilt from the highest `id` in any `sesshin.json`. |
| `migration` | integer ≥ 0 | The latest [migration](#migrations) step, when a hook creates the file on a first run; `0` when a hook rebuilds it; the latest, when `migrate` finishes | The last migration step applied to the state directory. Every other write keeps it. |

#### `lifecycle.json`

The session's lifecycle, as Claude Code's hooks reported it (tier 1). Written only by lifecycle hooks, under the session lock ([Recording an event](hooks-spec.md#recording-an-event)); each event's changes are in the hooks spec's [effects table](hooks-spec.md#effects-table).

**Identity and environment**

| Field | Type | Source | Notes |
|---|---|---|---|
| `session_id` | lowercase UUID | `.session_id` of any payload | Never changes. Always equals the directory name. |
| `cwd` | string or `null` | `SessionStart` `.cwd`; `CwdChanged` `.new_cwd` | Where the session is working; a later `resume` would reopen it there. |
| `transcript_path` | string or `null` | `SessionStart` `.transcript_path` | Claude's JSONL transcript. sesshin never writes it or reads it. |
| `session_title` | string or `null` | `UserPromptSubmit` `.session_title`, when non-empty | The `/rename` title; `null` if never renamed. The first choice for a session's [name](#terms). |
| `model` | string or `null` | `SessionStart` `.model` | Readers prefer the statusline's model when its payload is from the session's current life (`received_at` at or after `last_start_at`), since it follows a `/model` switch. So a resumed session never shows the model of its previous life. |
| `permission_mode` | camelCase open set or `null` | `.permission_mode` of every lifecycle payload that has one | `default`, `plan`, `acceptEdits`, `bypassPermissions`, …. A session that skips permissions acts on what it is given at once. |

**Process and lives**

| Field | Type | Source | Notes |
|---|---|---|---|
| `pid` | integer or `null` | `SessionStart`'s [lookup](#liveness) of Claude's process: `CLAUDE_PID`, else a walk up the process tree | `null` when the lookup found none; the statusline's lookup then supplies it (see [Liveness](#liveness)). |
| `pid_started_at` | opaque string or `null` | The same lookup | The process's start time, prefixed by the boot it belongs to. Compared only for equality, so a reused pid never matches. `null` exactly when `pid` is. |
| `entrypoint` | open set or `null` | `CLAUDE_CODE_ENTRYPOINT` in the hook's environment, at `SessionStart` or a late adoption | How Claude was started: `cli` interactively, `sdk-cli` for `claude -p`, other `sdk-…` values from the SDKs. Verbatim, through its own shape guard, `^[a-z][a-z0-9_-]{0,63}$`, since the values have hyphens; `null` when unset or when the guard fails. Makes a session [headless](#terms). |
| `nested` | boolean or `null` | The same [lookup](#liveness): `CLAUDECODE` in Claude's own environment | `true` for a session started by another session. `null` when Claude's environment couldn't be read and the ancestry walk found no answer either. Makes a session [headless](#terms). |
| `started_at` | timestamp | The session's first `SessionStart`, or its late adoption | Preserved across resumes, so a session's age is its whole age. |
| `last_start_at` | timestamp | Each `SessionStart` of source `startup`, `resume`, `clear`, or `fork`, and a late adoption | When the session's current life began. Orders sessions sharing one process (see [Liveness](#liveness)), and dates the statusline's `model`. |

**Turn state** (read by `list`, `show`, `send`, and the statusline)

| Field | Type | Source | Notes |
|---|---|---|---|
| `status` | open set | The last lifecycle event (see [Status](#status)) | `idle`, `working`, `waiting`, `needs_approval`. Meaningful while live; an ended session keeps its last. |
| `stall_reason` | open set or `null` | `StopFailure` `.error`, or `unknown` | Why the last turn died of an API error. Qualifies `waiting` (⛔). Cleared by every event that starts or continues a turn. |
| `background_tasks` | integer ≥ 0 or `null` | Length of `Stop`'s `.background_tasks` | Pending when the last turn ended. `null` while a turn is under way, and before any has ended. See [Self-waking](#self-waking). |
| `session_crons` | integer ≥ 0 or `null` | Length of `Stop`'s `.session_crons` | Scheduled wakeups pending when the last turn ended. As `background_tasks`. |
| `compactions` | integer ≥ 0 | +1 on each `PostCompact` | Manual and automatic. `0` at the first `SessionStart`; continued across a resume; counts every `PostCompact` (see [Straggler guard](#straggler-guard)). |
| `ended_prompt_id` | string or `null` | `Stop` or `StopFailure` `.prompt_id`; cleared by `UserPromptSubmit` | The turn that last ended: the [straggler guard](#straggler-guard)'s key. |

**Clocks**

| Field | Type | Source | Notes |
|---|---|---|---|
| `last_event_type` | `<verb>` or `<verb>:<qualifier>` | The event, e.g. `stop`, `start:resume`, `compact:auto`, `end:clear` | An open set. The qualifier is a copied enum; one that fails the shape guard is dropped, never the write. |
| `last_event_at` | timestamp | Now, on every recorded lifecycle event | The [activity clock](#the-two-clocks). |
| `event_seq` | integer ≥ 1 | +1 on every recorded lifecycle event | The [event ordinal](#event-ordinal). Continues across resumes; restarts only when a corrupt file is rewritten (see [Format versions](#format-versions)). A file that exists but can't be read is not rewritten. |

**End**

| Field | Type | Source | Notes |
|---|---|---|---|
| `ended_at` | timestamp or `null` | `SessionEnd`, the one death a hook reports | Cleared by a resume. |
| `end_reason` | open set or `null` | `SessionEnd` `.reason` | `clear`, `resume`, `logout`, `prompt_input_exit`, `other`. `null` while `ended_at` is, and when the reason failed its guard. Readers also report `superseded`, never stored, for a session another one replaced in the same process without a recorded `SessionEnd` (see [Liveness](#liveness)). |

#### `statusline.json`

The latest statusline payload, **verbatim**, with what sesshin derives from it (tier 1). Written only by the session's [statusline](hooks-spec.md#statusline), with no lock; a tick that finds a newer payload already stored skips its write.

| Field | Type | Source | Notes |
|---|---|---|---|
| `received_at` | timestamp | The tick | Says the UI is alive, not that the session is doing anything ([The two clocks](#the-two-clocks)); feeds last seen. |
| `received_ns` | integer | The tick: Unix nanoseconds, read when the tick's process starts | Orders overlapping ticks, which are 300 ms apart and so usually share `received_at`'s second (see [Files](#files)). A stored value more than a minute ahead of the tick's was written before the wall clock stepped back, and counts as older: it is replaced. sesshin-private; nothing reads it as a time. |
| `payload` | object | Claude Code's statusLine stdin, unchanged | Its shape is Claude Code's, not sesshin's (see below). |
| `git_branch` | string or `null` | A walk up from `payload.cwd` to `.git`: a directory's `HEAD`, or for a `.git` file (a worktree or submodule) the `HEAD` in the directory its `gitdir:` line names; no `git` process | Recomputed each tick, so a session that leaves a repository goes back to `null`. The walk starts at `cwd` and stops before `/`; an empty or relative `cwd` has no walk. A `.git` file's relative `gitdir:` is relative to the directory holding it. Only `HEAD` naming `refs/heads/<name>` has a branch: a detached `HEAD`, a `ref:` elsewhere, and a name that isn't [text](#file-schemas) give `null`. The first `.git` found is the answer: if its `HEAD` (or `gitdir:` line) can't be read, `git_branch` is `null`, never an enclosing repository's branch. |
| `cost_sample` | `{at: timestamp, usd: number}` or `null` | Carried forward from the last tick; replaced when more than 300 seconds old | The base of the burn rate. The first tick, or one after a previous tick with no sample, starts it from `cost.total_cost_usd`, and is `null` when that is absent, not a number, or negative. A tick with no such cost keeps the base as it is, whatever its age. Past the floor, a cost below the base's replaces it at once: it is a new run's. A base dated after the tick (the clock stepped back) is replaced. |
| `burn_usd_per_hour` | number ≥ 0 or `null` | `(cost − usd) / (received_at − at)`, scaled to an hour, computed before the sample is replaced, once the sample is at least 60 seconds old; before that, the previous tick's value carried forward | `null` unless both differences are positive and the sample is at most 600 seconds old, so an idle gap never averages into it. The 60-second floor keeps a fresh sample from turning one API response into an hourly rate. Ages are measured between the two timestamps, in whole seconds. Past the floor, a tick with no cost has a rate of `null`; so does one whose rate overflows. The first tick's is `null`. Readers report it as stored: it never decays with the reader's clock. |
| `pid` | integer or `null` | The statusline's own [lookup](#liveness), as `SessionStart`'s, on every tick | The statusline runs as a child of `claude`. [Liveness](#liveness) falls back to this when `lifecycle.json`'s `pid` is `null`, as it is for a session [adopted late](hooks-spec.md#late-adoption) by an async hook. |
| `pid_started_at` | opaque string or `null` | The same lookup | As in `lifecycle.json`. |

**What sesshin reads from `payload`.** Every path is optional; a missing one costs only what it feeds.

| Path | Type | Used for |
|---|---|---|
| `session_id` | UUID | Finding the session directory. |
| `session_name` | string | The session's [name](#terms), when it has no `/rename` title. |
| `model` | object (`{id, …}`) | The current model. |
| `cwd` | string | `git_branch`, and the statusline's directory. |
| `cost.total_cost_usd` | number | Cost, a running total for the session, and the burn rate. |
| `cost.total_api_duration_ms` | integer | Time spent waiting on the API, a running total. |
| `context_window.total_input_tokens` | integer | Context tokens: the window's current fill, not a running total. It equals `used_percentage` × `context_window_size`, and falls after a compaction. The payload carries no running token total. |
| `context_window.context_window_size` | integer | The window's size, shown as e.g. `351k/1M`. |
| `context_window.used_percentage` | number | The window's fill, as a percentage. |
| `rate_limits` | object | The account's limits: `five_hour` and `seven_day`, each `{used_percentage, resets_at}`, `resets_at` in Unix seconds. Per account, not per session. |
| `prompt_cache` | object | The [prompt cache](#prompt-cache) state. |

##### Prompt cache

`payload.prompt_cache` (Claude Code 2.1.251 and later; absent until the session's first API response) describes the main conversation's prompt cache: `warm`, `caching_observed`, `ttl` (`5m` or `1h`), `expires_at`, `requests`, `misses`, `expected_rebuilds`, `hit_ratio`, `cache_write_tokens`, `miss_recache_tokens`, `last_miss_at`, `last_miss_cause` (`{causes, …}`), `miss_causes`, and `recache_tokens_if_cold`. Its shape is Claude Code's, stored verbatim like the rest of the payload.

**`warm` is read through `expires_at`, never on its own.** The payload is a snapshot from the last tick. Claude Code does re-run the statusline when the cache expires, about a second after `expires_at`, with `warm: false` and `expires_at` unchanged ([verified](#claude-code-21288)), but only while it is running, and a reader can't tell whether that tick landed. A stored `warm: true` would stay true on disk long after the cache went cold. So readers derive:

- **warm** — `warm` is `true` and now is before `expires_at`.
- **cold** — `warm` is `false`, or `expires_at` has passed.
- **unknown** — no `prompt_cache` yet, or `caching_observed` is `false` (caching is off, or the provider doesn't report it), or `warm` isn't a boolean, or `warm` is `true` and `expires_at` isn't a number.

Two edges the list leaves open. An absent `caching_observed` is not `false`: only `false` makes the state unknown. And a now exactly equal to `expires_at` is cold, since warm needs now strictly before it. `expires_at` keeps its fractional part for the comparison.

The types sesshin relies on: `warm` and `caching_observed` booleans, `expires_at` a Unix time in seconds (as `rate_limits`' `resets_at`), `recache_tokens_if_cold` an integer ([verified](#claude-code-21288)).

While warm, the cache lasts until `expires_at` (the statusline shows that time, not a countdown that would freeze between ticks). Once cold, the next request re-caches about `recache_tokens_if_cold` tokens. Together these say whether an idle session is about to get more expensive to resume, and by how much. The statusline shows the derived state for its own session (see [`statusline`](hooks-spec.md#statusline)). The other fields (`hit_ratio`, `misses`, the miss causes) are stored as reported, for readers; the statusline doesn't show them. They're cumulative counts that don't decay with time, so they need no deriving.

> **Why verbatim?** A payload field with no place of its own to go would be lost the moment the tick ended, so every new question about cost or context growth would need a schema change before the data existed to answer it. Keeping the whole payload costs a few kilobytes per session and no schema at all: anything Claude Code reports is there for a later reader, and a new renderer is a code change, not a migration.

#### `sesshin.json`

sesshin's own relationship to a session (tier 2). Written under the session lock: created, with the ID, by the hook that finds it missing (see [Creating `sesshin.json`](hooks-spec.md#creating-sesshinjson)); then changed only by the writers named per field. Each writer changes only its own keys.

| Field | Type | Source | Notes |
|---|---|---|---|
| `id` | integer ≥ 1, or `null` | `last_id` + 1, when the file is created | The session's [sesshin ID](#sesshin-ids). Kept across resumes, never changed. `null` only while an issue is pending, until the next lifecycle hook completes it (see [Creating `sesshin.json`](hooks-spec.md#creating-sesshinjson)). |
| `job` | [job name](#reservations) or `null` | The [Adopt](#reservations) rules, when the file is created or its pending `id` completed | Decided by the Adopt rules; see [Reservations](#reservations). |
| `source` | `spawn` or `hook` | `spawn` when the session adopted a reservation; `hook` otherwise | How sesshin came to know the session: launched by `spawn`, or a `claude` you started yourself or one sesshin [adopted late](hooks-spec.md#late-adoption). Fixed when the job is decided, which can be a later hook than the one that first wrote the file ([Creating `sesshin.json`](hooks-spec.md#creating-sesshinjson)); never changed after. |
| `placement` | object or `null` | The terminal backend, from `SessionStart`'s environment, or a [late adoption](hooks-spec.md#late-adoption)'s | Where the session runs, in its terminal's own terms, tagged by `terminal` (see [Placement](#placement)). Every terminal-specific value sesshin keeps is in here, and nothing outside it names a terminal. `null` when sesshin can't place it: another terminal, kitty with remote control off, a session inside tmux or screen (whose kitty variables name the window the multiplexer started in), or a `claude` started by another session. Replaced at each `SessionStart` (also in a file whose `id` is still `null`), except the keys only the backend's sync writes. |
| `extra` | object | [`SESSHIN_EXTRA`](#user-owned-extra), when the file is written afresh; `{}` when [migration 1](#migrations) added it | Yours, not sesshin's (see [User-owned extra](#user-owned-extra)). `{}` when `SESSHIN_EXTRA` is unset or unusable. Kept across resumes, never changed: every hook that rewrites the file keeps it as it found it. |


**The kitty placement**, e.g. `{"terminal": "kitty", "socket": "unix:/tmp/kitty-554338", "window_id": 7, "tab_title": "api review", "user_vars": {"project": "api"}}`:

| Key | Type | Source | Notes |
|---|---|---|---|
| `terminal` | `kitty` | The backend's tag | The only key outside the backend that anything reads. |
| `socket` | string | `KITTY_LISTEN_ON` | The remote-control socket, verbatim: it may hold kitty's own placeholders (`unix:/tmp/kitty-{kitty.pid}-4099`), which `kitten` resolves. |
| `window_id` | integer | `KITTY_WINDOW_ID` | |
| `tab_title` | string | [`terminal-sync`](hooks-spec.md#terminal-sync), from `kitten @ ls` at each prompt | Recorded while the session is alive: a dead session's tab can't be asked. Scrubbed, as are the `user_vars` values (see [Placement](#placement)). |
| `user_vars` | object of strings | `terminal-sync`, from `kitten @ ls` at each prompt | The window's user variables (kitty's `--var`), set by hand or by whatever opened the window. |

#### `reservations/<key>.json`

A job claimed by `spawn` or `resume` before the session it launches has a UUID (see [Reservations](#reservations)). Written only under the [state lock](#locks); removed by the session that adopts it or is resumed into it (fresh or, once stale, by the session it was made for), by a launch that fails, or, once [stale](#reservations), by the next [`spawn`](operations.md#spawn) or [`resume`](operations.md#resume) of its job, or [`prune`](operations.md#prune).

| Field | Type | Source | Notes |
|---|---|---|---|
| `job` | [job name](#reservations) | `spawn`'s `job`, or `resume`'s (its `job`, else the ended session's stored one) | As given, case kept. Its [key](#reservations), the job lowercased, is the file name. |
| `token` | 32 lowercase hex characters | Random, from `spawn` or `resume` | Passed to the launch as `SESSHIN_TOKEN`. A session adopts the reservation only when its `SESSHIN_TOKEN` matches, so a `/clear`ed session or a nested `claude` that inherited the variables can never take a newer launch's claim. |
| `created_at` | timestamp | The claim | Dates the reservation for staleness. |
| `placement` | placement or `null` | The launched window, recorded by `spawn` or `resume` once the launch returns | `null` until then. Validated by its backend, as `sesshin.json`'s is. |

#### `install.json`

What [`install`](operations.md#install) proposed, so [`uninstall`](operations.md#uninstall) can propose undoing it. Written only by `install`, atomically, with no lock. It changes only when you run `install`. sesshin never writes Claude Code's `settings.json`: `install` and `uninstall` write `settings.proposed.json` beside this file, for you to apply.

| Field | Type | Source | Notes |
|---|---|---|---|
| `hook_binary` | absolute path | `sesshin-hook` in the directory of `sesshin`'s own executable (`os.Executable`, symlinks resolved) | The binary every hook runs. |
| `version` | string | `sesshin`'s build info, which `sesshin-hook`'s must match | The version that proposed the hooks for `settings.json`. |
| `installed_at` | timestamp | The install | |
| `locations` | `{config_dir, state_dir, claude_settings}`: absolute paths | The [locations](#locations) `install` resolved | `uninstall` proposes changes to the recorded `claude_settings`, not whatever it resolves itself. |

#### `hooks.log`

The hooks' only channel to a person (see [Log](hooks-spec.md#log)). Plain text, one line per entry, `<timestamp> <verb> <session-uuid or -> <message>`, appended by every hook with `O_APPEND` and no lock. At 1 MiB it is renamed to `hooks.log.1`, replacing the previous one. Its messages are for people, not a contract.

### Sesshin IDs

Each session gets a positive integer, its **sesshin ID**, issued the way koan issues task IDs: from `last_id` in `state.json`, which records the highest ID ever issued and never decreases.

```json
{ "schema": 2, "last_id": 41, "migration": 1 }
```

- **Issued when `sesshin.json` is created** — at the first `SessionStart`, or by the first hook that finds the file missing — under the session lock and then the [state lock](#locks): read `last_id`, write `last_id + 1`, then write `sesshin.json` with that `id`. A resume finds `sesshin.json` already there and keeps its ID; a `/clear` is a new session and gets a new one.
- **`state.json` is the one file sesshin flushes.** It is written about once per session, and losing it would reissue IDs, so it is written with `fsync` of the file and its directory (see [Files](#files)). A hook that finds it missing or corrupt while `sessions/` has sessions doesn't start again from 0: it issues from the highest `id` in any `sesshin.json`, plus one, rewrites it with `migration` 0 (see [Migrations](#migrations)), and logs the rebuild (`last_id rebuilt from 199`) once it is written. A missing `state.json` with no sessions is a first run. One that exists but can't be read (`EIO`, `EACCES`) is neither: it may hold a `last_id` the hook can't see, so the hook logs it and issues no ID. Nor does one in another [format](#format-versions), which only `migrate` (older) or a newer binary (newer) may write. `sesshin.json` is written with `id` `null`, as when the state lock's wait runs out, and the next hook that can read `state.json` issues from it.
- **Never reused while `state.json` survives,** even after its session is pruned, so a `#12` in an old note or a script never comes to mean another session. A crash between incrementing `last_id` and writing `sesshin.json` consumes an ID with no session: an allowed gap. Losing `state.json` is an [outside change](#assumptions): the rebuild above then reissues the IDs of sessions pruned since the highest surviving one. Refusing to issue IDs until someone repaired it by hand would cost every new session its handle to protect old notes, so sesshin recovers and logs instead.
- **Unique** across the state directory. A duplicate can come only from an outside change, such as a restored `state.json` with a lower `last_id`.
- **Found by scanning.** There is no index from ID to session: looking up ID 12 reads every `sesshin.json`. With [retention](#retention), that is at most a few hundred small files, as long as `prune` runs regularly: headless sessions, which an agent can start by the hundred a day, are prunable after a day.

The UUID still names the directory, because it is what every hook payload carries and what a resume brings back. The ID is the handle for people and agents: the statusline shows it, and commands take it (`sesshin show 12`).

### Reservations

`spawn` names a session before Claude has a UUID for it, so the job name can be the handle for everything after. That makes *one live session per job* worth guaranteeing. [`spawn`](operations.md#spawn) claims a job, and so does [`resume`](operations.md#resume), for the job it reopens a session under. A job can also be given by hand: `SESSHIN_JOB=api claude` names the session `api`, if no one else holds it.

- **Claim, launch, record.** `spawn` and `resume` claim the job under the [state lock](#locks): when no one holds it, they create `reservations/<key>.json` with a new random `token`, `created_at` now, and `placement` `null`, replacing a stale or unusable file of the same name. They launch `claude` with `SESSHIN_JOB=<job>` and `SESSHIN_TOKEN=<token>` in its environment, holding no lock, then record the launched window as the reservation's `placement`, if the file still holds their `token`: the one rewrite a reservation ever gets. A launch the backend refused removes the reservation, so the job is free at once; one whose outcome is unknown keeps it, since a window may have opened. The steps are [`spawn`](operations.md#spawn)'s Effects.
- **Adopt.** The hook that creates a session's `sesshin.json`, or completes its pending `id`, decides the job under the state lock: a session started by another session gets none, since it inherited the variables; a fresh reservation whose `token` equals `SESSHIN_TOKEN` is this session's (`source` `spawn`); otherwise the job is `SESSHIN_JOB`, if no one holds it. The steps are [Creating `sesshin.json`](hooks-spec.md#creating-sesshinjson) step 3.
- **Adopt on resume.** A resumed session already has its `sesshin.json`, so it never runs the Adopt rules. Its [`session-start`](hooks-spec.md#session-start) adopts the reservation it was launched with instead, and only then takes `SESSHIN_JOB` as its job. So a session resumed under another job (`resume --job`, for when its own is taken) changes job only once it has actually started. A `/clear`ed session carries a token whose reservation is long gone, so it finds nothing.
- **One holder at a time.** At every moment either the reservation or a live session holds the job: a hook removes the reservation only after `sesshin.json` names its job, and every decision is made under the state lock, so a concurrent `spawn` of the same job is always refused.
- **`SESSHIN_JOB` outlives a session.** It stays in Claude's environment for the process's whole life. So a `/clear` or an in-session `/resume`, which starts another session in the same process, gives that session the same job by the Adopt rules (`SESSHIN_JOB`, if no one holds it): it inherits the name you gave the window. Its `SESSHIN_TOKEN` names a reservation long since removed, so it can never take a newer `spawn`'s reservation of the same job, and if that `spawn` got in first, the new session simply has no job.
- **Fresh or stale.** A reservation is **stale** when one of these holds, and **fresh** otherwise. The words are the `reason`s [`prune`](operations.md#prune) reports:

  | Reason | When |
  |---|---|
  | `expired` | It is more than a day (86,400 seconds) past `created_at`. |
  | `stranded` | Its `placement` is still `null` 120 seconds after `created_at`: the launch never finished. |
  | `window-gone` | Its launched window no longer exists, as the terminal backend answered. |

  A launched session can wait at Claude's workspace-trust dialog, before any hook runs, for as long as you leave it, so a launched reservation stays fresh while its window exists, up to a day: age alone would free its job meanwhile. Only `spawn`, `resume`, and `prune` ask the backend about a window ([Placement](#placement)), and an answer counts only for a reservation still holding the `token` and `placement` it was asked about; the hooks and every other reader judge by age alone. A `placement` its backend rejects is not `null`, so such a reservation is judged launched, by the backend's answer or by age. Both limits are fixed, not configured, since the hooks judge freshness too and read no `config.toml`; should they need tuning, they belong in [`hooks.properties`](#hook-settings). A stale reservation is ignored by every check, and removed by the next `spawn` or `resume` of its job, or by [`prune`](#retention).
- **Unusable.** A reservation that doesn't validate, or whose `job`'s key isn't its file name, is unusable: ignored by every check, as if it weren't there, and removed by the next `prune`, or replaced by the next `spawn` or `resume` of its job.
- **Released by hand.** Removing `reservations/<key>.json` (`rm`, or asking Claude to) releases its job at once, and is not an [outside change](#assumptions): it is how a reservation is cancelled. Edit nothing inside one. If the launched session starts later, it finds no reservation, and takes the job only as a `claude` started with `SESSHIN_JOB` would, when no one else holds it, with `source` `hook`.
- **Holding a job.** A job is **held** by a live session that reports it (below), or by a fresh reservation. A session whose liveness is `unknown` counts as live. A job held by an ended session is free: names are recyclable.
- **Revived by hand.** `claude --resume <uuid>`, `claude --continue`, and an in-session `/resume` bypass `resume`'s check, so a revived session can come back storing a job another live session now holds. No hook rewrites it. Readers settle it, as [Liveness](#liveness) rule 3 settles one process: among live sessions storing one job (by key, so `API` and `api` contend), the one whose current life started first (earliest `last_start_at`, then the lower UUID) holds it, and the others report `job` `null` until it ends, however each came by it. So a job names at most one live session. A revival that starts while a launched session of the same job still waits at the workspace-trust dialog starts first, and holds the job: a rare collision, made by hand, and not worth a stored field to settle the other way. An ended session reports the job it stored. Every job check uses the job as readers report it.

Job names follow koan's name rule, `^[A-Za-z0-9](?:[A-Za-z0-9-]{0,62}[A-Za-z0-9])?$`, and are not all digits, so `12` always means a [sesshin ID](#sesshin-ids).

A job is stored and shown as given, case kept, but jobs that differ only in case are **one job**, as koan refuses folder names that differ only in case: macOS's default filesystem ignores case, so `API.json` and `api.json` would be one file. A job's **key** is the job with `A`–`Z` lowercased. Every check of whether a job is held compares keys: the claim, the Adopt rules, and the readers' settling of revived sessions. So `spawn --job api` while `API` is held fails `job-taken`, and a job's key names at most one live session. A [selector](operations.md#selecting-a-session) still matches a job exactly: `job:api` does not find a session whose job is `API`.

### User-owned extra

`sesshin.json`'s `extra` is for whoever uses sesshin, as koan's `extra` is for tasks: a durable link from a session to the work it does (`{"shingi-unit": "auth-3", "koan-task": 57}`), labels, or notes. sesshin stores it and hands it back, and never reads, validates, or acts on its contents. Finding sessions by it is left to `jq`: `sesshin list --fields extra | jq '.result.sessions[] | select(.extra["koan-task"] == 57)'`.

- **Set at the start, from `SESSHIN_EXTRA`.** [`spawn`](operations.md#spawn)'s `extra` reaches the session as `SESSHIN_EXTRA`, as its job reaches it as `SESSHIN_JOB`, and the hook that writes `sesshin.json` afresh copies it in ([Creating `sesshin.json`](hooks-spec.md#creating-sesshinjson)). An environment variable, not a file `spawn` writes, because the file doesn't exist until the session's first hook creates it, which can wait indefinitely at the workspace-trust dialog: the session has its `extra` from its first record. A `claude` started by hand takes it the same way: `SESSHIN_EXTRA='{"koan-task":57}' claude`.
- **Fixed once written.** A hook never changes it: every rewrite of `sesshin.json` keeps it as found. Changing it on a session already running, or ended, is the [deferred](deferred/operations.md#update) `update`.
- **Kept across resumes.** A resumed session already has its `sesshin.json`, and keeps it with its `extra`; `resume` passes no `SESSHIN_EXTRA`.
- **`SESSHIN_EXTRA` outlives a session,** as `SESSHIN_JOB` does: a `/clear` or an in-session `/resume` that starts a new session in the same process gives it the same `extra`. The window is likely still on the same work, so read `extra` as the work the window was started for, not what it is doing now. A session started by another session gets none, as it gets no job, since it inherited the variable.
- **Rebuilt when the file is.** A `sesshin.json` that is corrupt is written afresh by the next hook ([Format versions](#format-versions)), with a new sesshin ID and `extra` from `SESSHIN_EXTRA` again. One in an older format keeps its `extra` through [`migrate`](#migrations).
- **Limits.** An object, at most 65,536 bytes as compact JSON (the environment holds one variable to 131,072), nested at most 32 levels of objects and arrays, counting its own object, with no repeated key and no unpaired surrogate escape. A `SESSHIN_EXTRA` that breaks one is ignored, as if unset, and `spawn` refuses an `extra` that would. Numbers inside `extra` are exempt from the integer-literal and `float64`-range rules: like the statusline `payload`'s, they are written back exactly as given (`1.10` stays `1.10`).

It is deliberately separate from `placement`, which looks similar, a JSON object sesshin doesn't fully define, but is the opposite: sesshin's own working state, written only by sesshin and read by `send` and `resume`. User data under a key sesshin depends on would be one edit away from sending text to the wrong window.

### Two tiers

`lifecycle.json` and `statusline.json` are **tier 1**: facts that would be true whether or not sesshin existed. `sesshin.json` and reservations are **tier 2**: sesshin's relationship to a session. The rule is one-way: tier-2 code may read tier-1 files, and a hook may read tier 2 to decide what to write, but **no tier-2 value is ever written into a tier-1 file**. A reader that ignores `sesshin.json` sees exactly what Claude Code reported.

### Status

`status` is what the last lifecycle event said, and the set is **open** — a reader renders an unknown value as `unknown`:

| Status | Set by |
|---|---|
| `idle` | `SessionStart` (started, resumed, cleared into, or forked; at its prompt with no turn behind it) |
| `working` | `UserPromptSubmit`, tool results, `elicitation_complete`; a [late adoption](hooks-spec.md#late-adoption) by a status-neutral event |
| `waiting` | `Stop`, `StopFailure` (the turn ended; it wants you, unless [self-waking](#self-waking)) |
| `needs_approval` | `Notification` `permission_prompt` or `elicitation_dialog` (blocked on you) |

Compaction moves the clocks, and counts in `compactions`, but not the status: it proves the session alive and says nothing about whose turn it is. So does a `SessionStart` with a source sesshin doesn't know: `compact` shows a source can arrive mid-turn, and resetting to `idle`, which claims no turn is under way, would be the unsafe guess.

**A turn you interrupt ends without a hook.** Esc or Ctrl-C fires no `Stop`, so the status stays `working`, or `needs_approval` if you pressed Esc at a permission dialog, until the session's next event, usually your next prompt. Nothing else follows an interrupt: Claude Code's `idle_prompt` notification comes only 60 seconds after a turn that ended with `Stop`, never while a permission dialog is up and never after an interrupt ([verified](#claude-code-21288)), so sesshin records nothing for it, as for every notification type it doesn't use.

A new fact about a status arrives as a new nullable field beside it (as `stall_reason` and the pending counts did), not as a new status value, so readers that switch on `status` keep working.

#### Self-waking

`waiting` means the turn ended *and it wants you*. A session paused on its own background tasks, or on a wakeup it scheduled, wants nothing from you. `Stop` reports both counts, so a reader can tell the two apart. `null` is not zero: it means no turn has ended since the current one began (every event that starts or continues a turn clears the counts).

### Open sets

Claude Code's enums grow. Every value sesshin copies from a payload into a stored field (`.source`, `.reason`, `.trigger`, a notification type) passes a **shape guard** — `^[a-z][a-z0-9_]{0,63}$`, or with capitals allowed for the camelCase `permission_mode` — not a whitelist. A value that fails the guard costs only the qualifier (`end` instead of `end:<junk>`), never the write: dropping a `SessionEnd` because its reason was unreadable would leave the session looking alive.

### Timestamps

As in koan: UTC, whole seconds, with a `Z` suffix (`YYYY-MM-DDTHH:MM:SSZ`, e.g. `2026-10-02T18:31:51Z`), naming a real time (no `02-30`, no hour `24`). No offsets and no fractional seconds. Two events in the same second are ordered by [`event_seq`](#event-ordinal), not by time.

## Liveness

A session is **live** when all three hold:

1. `lifecycle.json` has `ended_at` `null`;
2. a process with its `pid` exists whose start time equals `pid_started_at`;
3. no other session with the same `pid` and `pid_started_at` ranks above it. Sessions rank by `last_start_at`; within the same second, by `last_event_at`; then the lower UUID ranks above. The session really running keeps getting events and the superseded one gets none, so `last_event_at` settles a tie within moments; the UUID only makes it deterministic.

Otherwise it is **ended**, or **unknown** when its liveness can't be judged (see [Unknown](#liveness)). Liveness is derived on every read and never stored.

- **From the process table, never from kitty.** A window missing from `kitten @ ls` is evidence about placement, not life: a socket blip would otherwise end every session at once.
- **pid is Claude's own.** Claude puts its own pid in its children's environment as `CLAUDE_PID`. A hook or the statusline takes it when it names one of its own ancestors: its parent when the shell Claude runs the command through replaces itself with the command (as it does, [verified](#claude-code-21288)), else its parent's parent. Otherwise, as under a version that doesn't set it, it walks up its process ancestry to the first `claude` process: one whose name is `claude`, or whose executable is a versioned binary under Claude Code's `versions/` directory (an IDE or the updater may start it by that path, giving it the version as its name). Either way, not the window's shell, which outlives Claude. An npm or Agent SDK launch runs Claude as `node`, which only `CLAUDE_PID` identifies: the walk never matches `node` by name. `SessionStart` (a blocking hook, so a live descendant of Claude) records the result. The statusline, also a child of `claude`, records its own lookup in `statusline.json`, and a reader uses it when `lifecycle.json`'s `pid` is `null`.
- **Started by another session.** A Claude started from inside a Claude session (a `claude -p` run by a tool, say) inherited `CLAUDECODE=1`, so it has it in its own initial environment, which sesshin reads from the process table (`/proc/<pid>/environ` on Linux, `KERN_PROCARGS2` on macOS). A top-level Claude has no `CLAUDECODE`. When that environment can't be read, sesshin falls back to looking for another `claude` above it in the ancestry, by the walk's name rule.
- **The start time closes pid reuse.** A pid recycled after Claude died belongs to a process with a different start time, so the session reads as ended. The start time carries the boot it belongs to, since a session autostarted after a reboot can land on the same pid at nearly the same tick count.
- **One process, one live session.** `/clear` and an in-session `/resume` end one session and start another in the same process. Their `SessionEnd` normally marks the first one ended, but a lost `SessionEnd` (a lock wait that ran out, a hook killed at exit) would leave both live, sharing a window. Rule 3 settles it at read time: the session that started last in a process is its live one, and the others are ended, reported with `end_reason` `superseded`.
- **No pid.** When neither lookup found Claude's process, the pid is `null`. Such a session is live until `SessionEnd`, or until its [last seen](#liveness) is more than 24 hours old, then ended. The limit is fixed, not configured: the hooks judge liveness too, to decide a [job](#reservations), and read no `config.toml`. It counts as live: the prune never removes it. The statusline's lookup makes this rare: a session with no Claude found by either is one whose statusline isn't sesshin's.
- **Unknown.** A session whose liveness can't be judged is **unknown**: its `lifecycle.json` is missing or unusable, or the check of its process failed for any reason other than there being no such process (an unreadable process table, a malformed entry, an unreadable boot ID). An unknown session is never pruned. An unusable `statusline.json` only takes away the pid fallback, so the no-pid rule above applies; a session whose `lifecycle.json` is unusable takes no part in rule 3.
- **When it ended.** A session ended by `SessionEnd` ended at `ended_at`. One whose process is gone ended at some unknown time; sesshin reports its **last seen** — the later of `last_event_at` and the statusline's `received_at` — and orders by that.

> **Why derive it?** No hook reports some deaths (`kill -9`, a crash, a closed terminal). With the pid and its start time in the session's file, any reader can tell a dead session from a live one in one `kill(pid, 0)` and one start-time lookup, so there is nothing to write down and nothing to keep running.

## The two clocks

- **`last_event_at`** (`lifecycle.json`) — semantic activity. Written only by lifecycle events.
- **`received_at`** (`statusline.json`) — the last statusline tick. Says the session's UI is rendering, not that it is doing anything.

`last_event_at` is a pure activity clock: it moves only when a lifecycle event is recorded, so a reader can trust its silence (`now − last_event_at`). `received_at` says only that the UI is alive, and feeds [last seen](#liveness). **Nothing that gates a lifecycle write may depend on a clock**: a lifecycle write is never skipped because the status did not change, because then a busy session's clock would stop and it would read as silent. Notification types sesshin doesn't use, `idle_prompt` among them, record nothing at all, not even the clocks: `idle_prompt` comes 60 seconds after a `Stop`, and moving the clock then would date activity that never happened. Every tool result is recorded, a subagent's included: a session busy with tools is never silent.

### Event ordinal

`event_seq` counts lifecycle events: every write that sets `last_event_at` increments it by exactly one, and nothing else changes it. It starts at 1 on the first `SessionStart`, continues across a resume, and never decreases, except when an unusable `lifecycle.json` is rewritten and it restarts at 1 ([Format versions](#format-versions)).

A reader polling on an interval cannot otherwise tell "nothing happened" from "three things happened": `prompt → tool → stop` between two reads looks like the previous `stop`. If `event_seq` advanced by more than one, the reader missed events and can read the transcript for the interval. It counts events, not turns, and says nothing about *which* events landed. It also does not mean the status changed (see [Straggler guard](#straggler-guard)).

### Straggler guard

Hooks are separate processes and arrive out of order: a `PostToolUse` can land after the `Stop` of its own turn, and would turn `waiting` back into `working`. So `Stop` and `StopFailure` record the ended turn's `prompt_id` in `ended_prompt_id`, and a later event whose `prompt_id` equals it is a **straggler**: it advances `last_event_at` and `event_seq` — a late tool result *is* activity — but leaves `status`, `stall_reason`, the pending counts, and `last_event_type` alone.

The key is turn identity, not time: the events are milliseconds apart within one turn, which is exactly what a timestamp cannot order. And it has a field of its own, so events that don't touch the status (a compaction after the turn ended, say) can't hide that the turn ended. `UserPromptSubmit` clears `ended_prompt_id`: a new turn has begun, and nothing from it can be stale. A `null` `ended_prompt_id`, or an event with no `prompt_id`, makes the guard inert.

A compaction is never withheld as a straggler: `compactions` counts on every `PostCompact`, since a compaction isn't a stale status.

## Placement

Where a session runs, in its terminal's terms. A **terminal backend** owns everything terminal-specific: recognizing its terminal from a hook's environment, writing and reading its own `placement` keys, and saying whether a placement's window still exists. The rest of sesshin sees `placement` as an opaque object with a `terminal` tag and calls the backend that the tag names. kitty is the only backend; a second (tmux, WezTerm) adds a tag and a backend, and changes no file format.

Placement is recorded for commands that act on a window, because it can only be learned while the session runs: [`resume`](operations.md#resume) reopens a session under its tab title and user variables, [`send`](operations.md#send) uses its `socket` to find the session's window afresh by pid, and the deferred `focus` will need the window too; an ended session's tab can't be asked. How `focus` verifies and repairs it is [deferred](deferred/design-spec.md#placement-verifying-and-repairing-a-window) with it.

The kitty backend:

- **Recorded from the hook's own environment.** `SessionStart` runs inside the session's window, so `KITTY_LISTEN_ON` and `KITTY_WINDOW_ID` are its placement. So are they for any lifecycle hook that creates `sesshin.json` for a session [adopted late](hooks-spec.md#late-adoption), though only `SessionStart` replaces a placement that exists. Outside kitty, with remote control off, under tmux or screen (`TMUX` or `STY` set), or in a `claude` started by another session, `placement` is `null`: the kitty variables were inherited and name some other window.
- **Replaced with care.** A new placement keeps the old one's `tab_title` and `user_vars` only when the old one is kitty's and either names the same `socket` and `window_id`, or the session is being resumed (`SessionStart` source `resume`). Those keys describe the window, so another window starts without them, except on a resume: [`resume`](operations.md#resume) opens its tab with exactly those, and the sync runs only at a prompt, so a session restarted and never prompted before the next reboot would otherwise lose its title. A session resumed by hand in some other window shows the old title and variables until its next prompt's sync.
- **Validated by the backend, when it reads.** The file schema checks only the `terminal` tag. The backend treats a kitty placement as `null` when `socket` is missing or empty, `window_id` isn't a positive integer, `tab_title` is present and not a string, or `user_vars` is present and not an object of strings. It checks wherever it reads a placement: when replacing one (an invalid old placement keeps nothing) and in `terminal-sync` (an invalid one is not written to, and the file is left alone). It never rewrites `sesshin.json` just to remove one. Keys it doesn't know are ignored.
- **Scrubbed.** `tab_title` and each `user_vars` value from `kitten @ ls` are scrubbed before they are stored, like every string sesshin stores ([Reading the payload](hooks-spec.md#reading-the-payload)). `socket` is kept verbatim: `kitten` resolves it, placeholders and all.
- **A cache, not the truth.** Inherited variables can name the wrong window (a shell started with `kitten @ launch --copy-env`, say), so anything that acts on a placement must verify it first.
- **Launches a window** for [`spawn`](operations.md#spawn) and [`resume`](operations.md#resume), with one `kitten @ launch` on the caller's socket, as [Launching `claude`](operations.md#launching-claude) says. The caller is recognized as a hook is: `KITTY_LISTEN_ON` and `KITTY_WINDOW_ID` set, outside tmux and screen.
- **Asked whether a window exists,** for a launched reservation (see [Reservations](#reservations)), by `spawn`, `resume`, and `prune`: one `kitten @ --to <socket> ls` per distinct socket, with a 1-second timeout, and no lock held. The window is gone only when `kitten` answers and doesn't list its `window_id`. Any failure (no `kitten`, a timeout, a socket that refuses, output that isn't `kitten @ ls`'s) is no answer, and the reservation is judged by age alone: a closed kitty's socket refuses as a blip's would, and freeing a job early is the error to avoid.
- **Probed, never parsed.** Whether remote control works is read from the environment (`KITTY_LISTEN_ON` set; `KITTY_WINDOW_ID` set without it means "in kitty, remote control off"), never by parsing `kitty.conf`.

## Retention

Ended sessions are kept, so `sessions/` only grows. A session is **prunable** when it has ended and its last seen is older than `retain_days` (default 30), or, for a [headless](#terms) session, older than `retain_headless_hours` (default 24). `retain_days = 0` means never prune, not "keep zero days"; `retain_headless_hours = 0` means headless sessions are kept as long as any other. Headless sessions get their own window because an agent running `claude -p` can leave hundreds a day, which no one will resume, and every read scans them all.

**sesshin never prunes on its own.** [`prune`](operations.md#prune) runs when you run it, and only then: no hook prunes, and there is no daemon or timer. Run it by hand, or schedule it with whatever your machine already uses (examples in [`prune`](cli-spec.md#prune)). Until it runs, ended sessions cost disk space, a few kilobytes each, and time for `list` and `show`, which read every session directory; the statusline reads only its own.

A prune never removes a live session, or one whose liveness is `unknown`, whatever its age. It judges each session again under that session's lock before removing it, so a session resumed by hand while a prune runs is kept, and it writes nothing outside the directories and reservations it removes: `state.json` is left alone, so `last_id` keeps counting past the IDs of pruned sessions. It also removes [stale and unusable reservations](#reservations). The steps are [`prune`](operations.md#prune)'s Effects.

An unusable clock makes a prune decline: guessing a cutoff is the one way retention could delete what it should keep. The clock is unusable when now is earlier than the newest `last_event_at`, `received_at`, or reservation `created_at` the run reads: hooks recorded those on the same clock, so a later time can't be in the past. A clock that jumped forward isn't caught: the cutoff then removes sessions early. `prune --dry-run` shows what a run would remove.

## Concurrency

Writers are hooks of many sessions, firing concurrently, plus `install`, `uninstall`, `spawn`, `resume`, `prune`, and `migrate`. Readers (the statusline, `list`, `show`, an agent polling with `jq`) take no lock.

### Locks

Two locks, both `flock` on a directory (as koan's write lock), so there is no lock file to clean up, and a crashed holder releases its lock when it exits:

- **Session lock** — the session directory. Serializes read-modify-write of `lifecycle.json` and `sesshin.json`. Held for one hook's writes, or one operation's. **One lock per session:** hooks of different sessions never wait on each other. The lock orders only the hooks of *one* session that overlap — parallel tool calls, an async `PostToolUse` racing its own `Stop` — which would otherwise lose updates (see [Hook cost](#hook-cost)).
- **State lock** — `sessions/`. Held by a hook while it creates a session's `sesshin.json`, issuing a [sesshin ID](#sesshin-ids) and deciding its [job](#reservations), or adopts the reservation a resumed session was launched with (a few file operations, and with `SESSHIN_JOB` set, a read of every session), by [`spawn`](operations.md#spawn) and [`resume`](operations.md#resume) while they claim a job (a read of every session and a file write) and again after the launch to record the window, by [`prune`](#retention) while it removes stale reservations, which it only *tries*, once, holding no session lock, and by [`migrate`](#migrations) while it writes `state.json`, holding no session lock. `spawn`, `resume`, and `prune` never hold a session lock; `migrate` holds one session's at a time, waiting for it as a hook does, and never with the state lock. Locks are always taken session lock first, then state lock, so they can't deadlock. `prune` only *tries* each session's lock too, and skips that session if it's held.

**`spawn` and `resume` wait less.** With a job, each waits up to 500 ms for the state lock to claim it, then fails [`busy`](operations.md#error-kinds): hooks hold it for milliseconds, and a script spawning in a loop would otherwise collide with its own previous spawn. After the launch each waits up to 2 seconds to record the window, since giving up then costs the reservation its protection at the trust dialog. Both are fixed.

**Hooks wait.** A lifecycle write that fails loses an event that will not come again, so a hook waits for a lock for up to `hook_lock_wait_ms` (default 2000, from [`hooks.properties`](#hook-settings)), and for all its locks together up to twice that, then gives up and logs ([H4](hooks-spec.md#the-contract)).

### Files

- **Atomic replacement.** Every file is written to a hidden temp file in the same directory and renamed into place. A reader sees the old file or the new one, never part of one.
- **No `fsync`, but one.** sesshin's files describe processes that a system crash ends anyway. After one, a file may be lost or empty; a read reports it as corrupt, and the next write of that file replaces it. The hot paths (statusline, tool hooks) do not pay for a flush. `state.json` is the exception: losing it would reissue [sesshin IDs](#sesshin-ids), and it is written once per session.
- **No session lock for two kinds of file.** `statusline.json` has one writer, the statusline, whose ticks can overlap: before renaming, a tick re-reads the stored `received_ns` and skips its write if that is newer (by up to a minute: one further ahead was written before the wall clock stepped back), so an older tick finishing last almost never puts back a stale payload for the whole idle period that follows. The re-read and the rename are not atomic, so two ticks finishing within microseconds of each other can still land in the wrong order; that costs a display until the next tick, and isn't worth a lock. `state.json` and reservations are written, and reservations removed, under the state lock. Every other file in a session directory is written under its session lock; `install.json` is written only by `install`, `settings.proposed.json` only by `install` and `uninstall`, and `hooks.log` is appended with `O_APPEND`, none under a lock.
- **Leftovers are left alone.** A crash can leave a hidden temp file, a hidden directory from an interrupted prune, or a session directory with no `lifecycle.json`. Reads ignore them and nothing removes them; they cost only disk space. Cleaning them up is deferred with `repair`, which must judge them by age: each is also what a write in progress looks like, so it is a leftover only once it is more than 60 seconds old.

### Format versions

Every JSON file sesshin writes starts with a `schema` integer, its format version, except `settings.proposed.json`, which is in Claude Code's format. A binary supports exactly one version of each file, as koan's does, and [`version`](operations.md#version) reports them. A file a binary can't use is **unusable**, and is one of two kinds:

- **In another format:** its `schema` is an integer literal, but not this binary's version, older or newer. Only [`migrate`](#migrations) changes a file's `schema`, so every other writer leaves such a file alone: an older one waits for `sesshin migrate`, and a newer one belongs to a newer binary, which this one never downgrades. Reads treat it as missing.
- **Corrupt:** anything else: not JSON, no usable `schema`, or invalid at this binary's version. Reads treat it as missing, and the next write of that file replaces it from scratch, as if it had never existed. A replaced `lifecycle.json` starts as a [new record](hooks-spec.md#a-new-lifecyclejson), as a late adoption does: `event_seq` from 1, `started_at` and `last_start_at` the time of the rewrite. A replaced `sesshin.json` gets a fresh sesshin ID from `last_id`, never the old one. Nothing tries to read a corrupt file's fields.

Rules:

- **Every format change bumps `schema` and ships a [migration](#migrations),** before 1.0 as after: adding a field, removing one, or changing what one may hold. A binary on either side of the change then sees the other's files as in another format, and leaves them alone, rather than reading them as corrupt and replacing them. (Before migrations, formats changed in place without a bump, and a new binary replaced the old files from scratch; `extra` was the last such change, and migration 1 repairs it.)
- **Two kinds of file are replaced whatever their format,** since each is written whole by one command and lives briefly or holds nothing worth keeping: `install.json`, which every [`install`](operations.md#install) rewrites, and a [reservation](#reservations), which [`spawn`](operations.md#spawn) and [`resume`](operations.md#resume) replace and [`prune`](operations.md#prune) removes once it is unusable, whatever the kind.
- **Until `migrate` runs, a session records what it can.** A hook writes the files in its own format and leaves the others alone, logging each one it leaves only from `session-start`, so a busy session's every tool call doesn't add a line. An older `lifecycle.json` records nothing: the session's events are lost until `migrate`. An older `sesshin.json` keeps its ID, job, and `extra` but takes no new placement, and the statusline shows no ID. An older `statusline.json` keeps its last metrics. An older `state.json` issues no ID: a new session's `sesshin.json` is written with `id` `null`, and the first lifecycle hook after `migrate` completes it.
- **Upgrading needs no session stopped.** The hooks' command in `settings.json` is `sesshin-hook`'s path, so replacing the binary at that path changes it for running sessions too, from their next hook: Claude Code snapshots the command, not the binary. Run `sesshin migrate` right after replacing the binaries, so that window stays short (see the [README](README.md#upgrading)).

### Migrations

A format change ships with a **migration step**, which [`sesshin migrate`](operations.md#migrate) runs to bring existing files to the new format, as Flyway runs numbered scripts. Steps are numbered 1, 2, 3, … in the order they were written, and a binary knows every step up to its **latest**. `state.json`'s `migration` records the last step applied to the state directory.

- **A step takes one or more kinds of file from one `schema` to the next,** and may change their content as it does. Migration 1, `extra`: `sesshin.json` 1 → 2, adding `extra` as `{}` after `placement`; `state.json` 1 → 2, adding `migration`.
- **`migrate` runs every step after the recorded one,** in order. For each file, it applies the pending steps that cover it to its content in memory, starting from the file's own `schema`, and writes the result once, atomically, only if it then validates as this binary's format. A file already in this binary's format is left as it is, so a step run twice changes nothing.
- **Progress is recorded last.** Each session's files are converted under its [session lock](#locks), one session at a time, waiting for it as a hook does. Then, under the state lock, `state.json` is converted and its `migration` set to the latest. A run interrupted halfway (a crash, a lock it couldn't get) leaves some sessions converted and the number where it was; the next run converts the rest. Since it takes a hook's own locks, `migrate` runs alongside running sessions.
- **A file it can't convert is reported, and left as it is.** A file in an older format that the steps can't read, or whose result doesn't validate, can only come from an [outside change](#assumptions). `migrate` lists it, and the number still advances: every step ran. Hooks go on leaving it alone. Remove it to have its session recorded afresh (for `sesshin.json`, with a new sesshin ID).
- **A fresh start needs no migration.** A hook that creates `state.json` on a first run (no other session) writes the latest number, since there is nothing to convert. One that rebuilds it (missing or corrupt while sessions exist; see [Sesshin IDs](#sesshin-ids)) writes 0, since it can't know what was converted: the next `migrate` runs every step, and each passes over what is already current. A `state.json` at schema 1 predates migrations, and counts as 0.
- **A binary older than the data never runs a step.** When the recorded number is past its latest, or `state.json` is in a newer format, `migrate` fails [`unsupported-format`](operations.md#error-kinds), and hooks leave the newer files alone.
- **Reported, never run for you.** [`version`](operations.md#version) reports the latest step. [`install`](operations.md#install) and every operation that reads the state directory warn [`migration-pending`](operations.md#warning-kinds) while the recorded number is behind the latest, and `migration-ahead` while it is past it. Only `sesshin migrate` runs steps.
- **Only `sesshin` has them.** `sesshin-hook` links no step: a hook only compares a file's `schema` with its own.

### File format

Every JSON file sesshin writes is written the same way, so `jq` and `diff` show only real changes:

- Keys in the order the schema lists them. The statusline `payload` keeps its keys in the order received, and `settings.proposed.json` the order of the `settings.json` it was made from.
- Two-space indentation, one key per line; empty objects and arrays written `{}` and `[]`.
- Minimal string escaping: only `"`, `\`, U+0000–U+001F, U+2028, and U+2029; everything else is raw UTF-8.
- Inside `payload`, numbers and string escapes written back exactly as received (`\u0041` stays `\u0041`), except that raw U+2028 and U+2029 are escaped, and an escape naming half a surrogate pair (`\ud83d` alone, which Node writes for a string cut inside an emoji) becomes `\ufffd`, the character every decoder reads it as: sesshin's strict reader refuses lone surrogates, and a stored payload must read back. A payload nested deeper than 64 levels, or with a key repeated in any object (after those rewrites, which can make two keys equal), isn't stored at all (Claude Code's is about 4; indented output grows with the square of the depth).
- UTF-8, no byte-order mark, a single trailing newline.

### Reads

A read is not a snapshot. It may combine a `lifecycle.json` and a `statusline.json` written moments apart, and be stale by the time it is shown. A session directory that disappears mid-read was pruned, and is skipped. A file that cannot be parsed is treated as missing: the session is shown with what was readable, or skipped when `lifecycle.json` itself is unusable.

## Configuration

Two files in the config directory, one per binary, so that each setting has exactly one source and a hook never parses TOML.

### `config.toml`

Read by `sesshin`: `prune`, `spawn`, `resume`, `restart`, and `install`; the other commands use no key, so they don't read it. Every key is optional; a missing file means every default.

```toml
retain_days           = 30
retain_headless_hours = 24
spawn_shell           = ["/bin/zsh", "-l", "-i"]   # default: [$SHELL, "-l", "-i"]
```

`retain_days` and `retain_headless_hours` are integers ≥ 0. `spawn_shell` is the shell [`spawn`](operations.md#spawn) and [`resume`](operations.md#resume) run `claude` through ([Launching `claude`](operations.md#launching-claude)): a non-empty array of non-empty strings, the first an absolute path. Absent, it is `$SHELL` with `-l -i`, or `/bin/sh -l -i` when `SHELL` is unset, empty, or not absolute. A login, interactive shell gives `claude` the `PATH` a tab opened by hand has. An unknown key, a value of the wrong type, or one out of range makes the config `corrupt`: `sesshin` commands that read it fail on it, and `install` checks it.

### Hook settings

`hooks.properties`, read by `sesshin-hook` on every run. A missing file means the default.

```properties
hook_lock_wait_ms=2000
```

One `key=value` per line, with no spaces around `=` and a CRLF ending read as LF (one trailing `\r` per line is dropped); blank lines and lines starting with `#` are ignored. The one key is `hook_lock_wait_ms`, an integer from 0 to 4000 (a hook waits at most twice it in all, and `SessionStart` must stay under its 10-second `timeout`). An unknown key, a key given twice, a malformed line (spaces around `=`, a `#` that isn't the first character), or a value out of range makes the file bad: hooks silently use the default, since a log line on every run would bury everything else, and `install` fails `corrupt` on it. Run `sesshin install` after editing it to check it.

**No environment variables.** Neither binary reads a setting from the environment (`SESSHIN_JOB` and `SESSHIN_TOKEN` are hand-offs to one session, not settings), except `SESSHIN_PICK_OPTS`, which only restyles the picker's fzf ([fzf options](picker-spec.md#fzf-options)); tests, and `install`'s self-test, point `HOME` at a temporary directory instead (with the XDG variables unset, since macOS ignores them). A hook's environment is whatever started Claude, which a shell profile doesn't reliably reach, so a setting in the environment could reach the CLI and not the hooks, a divergence nobody could see.

## Hook cost

Hooks are how Claude Code tells sesshin anything: each is `sesshin-hook <verb>`, registered in Claude Code's `settings.json` by [`install`](operations.md#install), and every one keeps [the contract](hooks-spec.md#the-contract), which binds it to exit 0, stay silent, and wait only within a bound, so that it never blocks or fails a session. A hook's cost is almost entirely the cost of starting a process; the write itself is noise, as long as it doesn't flush. Measured on Linux (btrfs on LUKS, 24 cores) with a prototype Go hook — read the payload, lock the session directory, read `lifecycle.json`, update it, write a temp file, rename — n=500 each, warm cache:

| | median | p99 |
|---|---:|---:|
| Go binary that does nothing | 1.02ms | 1.24ms |
| Lifecycle event (lock, read, write, rename) | 1.25ms | 1.50ms |
| Statusline (store payload verbatim, print a line) | 1.20ms | 1.51ms |
| Lifecycle event **with** `fsync` of file and directory | 11.10ms | 11.55ms |
| herd's SQLite `W4_event`, for comparison (ext4, its own benchmark) | 18.00ms | |

Eight concurrent writers × 100 events to one session finished in 162ms with `event_seq` at exactly 800: the session lock loses no updates.

Contention, 10 sessions × 4 concurrent writers each × 50 events (2,000 hook runs):

| Load | Lock | Per hook, median / p99 | Lock wait, median / p99 | Lost updates |
|---|---|---:|---:|---:|
| Saturated: every writer back to back, ~6,000 hooks/s | none | 5.93ms / 22.5ms | — | **~48%** (`event_seq` 101–105 of 200) |
| Saturated | per session | 5.89ms / 17.9ms | 2.2ms / 6.5ms | 0 |
| Each writer every 200ms: 20 events/s per session, 200/s in all | per session | 1.63ms / 22.7ms | 0.018ms / 0.73ms | 0 |

Saturated, the lock costs nothing measurable: total time is the same with and without it, because process starts on a full CPU are the bottleneck. Without it, half the events are lost. At 20 events/s per session — several times what a session produces, since a session records one event per tool call at most, and the statusline takes no lock — the median wait is 18µs.

What this decides:

- **No `fsync`** (see [Files](#files)) is the decision that matters: it is 90% of a write. The language is the next lever, and a small one.
- **Go.** A process start of about 1ms is the floor; an interpreter (herd's Python cost ~60ms per start) or a shell plus `jq` is several times it.
- **`settings.json` runs the binary directly** (`sesshin-hook <verb>`), with no shell shim: herd's shim cost 0.6–2ms of `bash` startup.
- **Two binaries.** Go runs every linked package's `init()` at every start, whether or not the hook path reaches it, so what the CLI links, every hook pays for. Measured on the same machine on 2026-10-03 (n=1000, warm):

  | | median | p99 |
  |---|---:|---:|
  | Go binary that does nothing | 0.88ms | 1.02ms |
  | Lifecycle event, standard library only | 1.21ms | 1.41ms |
  | The same, with cobra and pflag linked (dispatched before cobra runs) | 1.40ms | 1.65ms |
  | Do-nothing, plus a TOML library parsing a 5-key file | 1.05–1.13ms | |
  | Do-nothing, plus a JSON Schema library linked but unused | 3.46ms | 4.02ms |

  So `sesshin` (the CLI, with cobra and a TOML library) and `sesshin-hook` (every hook, the standard library only) are separate binaries, built together and installed side by side. `sesshin-hook` reads no TOML: its one setting is a one-line [properties file](#hook-settings). The implementation spec's [performance gate](implementation-spec.md#performance-gate) is a benchmark with two budgets, one for start-up and one for a whole event, that fails when `sesshin-hook` drifts above either over the do-nothing binary.

## Departures from herd

| herd | sesshin | Why |
|---|---|---|
| SQLite database, WAL, `busy_timeout` | One directory per session, a JSON file per writer | No schema migrations, no write lock shared by every session, inspectable with `jq`. That the statusline never moves the activity clock was a comment and a source scan; now it never writes the file that holds the clock. |
| Daemon: reaper, attention tick, retention sweep | None | Liveness and staleness are derivable at read time (see [Liveness](#liveness), [Reservations](#reservations)), and `prune` runs when you run it. |
| Surrogate integer ID (`AUTOINCREMENT`), adoption by window or `SESSHIN_JOB` | UUID names the directory; a sesshin ID from `last_id` is the short handle; reservations by job key, adoption by `SESSHIN_TOKEN` | A reservation file holds the job before the UUID exists, and the environment makes adoption exact; the ID stays a handle, never a key. |
| Boot sweep, pid claim, unique live-pid index | `pid_started_at` | One comparison closes pid reuse, which those three only approximated (herd's deferred `pid_start_time`). |
| `sesshin_attention` row, arm / ack / rearm statements | Dropped | Never earned its keep. |
| `working` at `SessionStart`, which armed 🥱 for every session opened and left alone | `idle` | A session at its prompt hasn't started a turn. |
| `PostToolUse` throttled to one write per two seconds | Every tool result recorded | With one lock per session, the [measured](#hook-cost) wait at several times a busy session's rate is 18 µs. |
| A column per statusline field | The payload, verbatim | Nothing reported is lost for want of a column. |
| A fingerprint cache that skipped unchanged statusline writes | None | The write is the cheap part of the hook, and the cache could skip a write that never landed. |
| Two-line emoji statusline | The same layout, plus the sesshin ID, context tokens, and the prompt cache | |
| `SESSHIN_*` env vars over `~/.sesshin/config` | `config.toml` for `sesshin`, `hooks.properties` for `sesshin-hook` | One source per setting, and no TOML on the hook path. |
| Human output, `rows` / `preview` text verbs | JSON envelope; the statusline for people | Agents can drive sesshin. |
| `ls`; shell completion (`complete`, `tcomplete`); the internal `poke` | `list`, with no aliases; none | Command names are operation names. |
| `~/.sesshin` | XDG config and state directories | Config and state have different lifetimes. |

## Open questions

- **The no-pid limit.** With the statusline's lookup as a fallback, how often is a pid still unknown? If never in practice, `null` pid could be treated as ended at once.
- **`CLAUDE_PID` and `CLAUDECODE` elsewhere.** Both are [verified](#claude-code-21288) only on 2.1.288's native install. To verify on each target version, and on npm and Agent SDK launches.
- **Dialogs that send no notification.** Do `AskUserQuestion` and plan approval fire a `Notification`? If not, a session blocked on one reads `working` rather than `needs_approval`. [`send`](operations.md#send) doesn't depend on it: it refuses every status but `waiting` and `idle`.

### Settled

These are the facts to re-check when upgrading Claude Code or kitty. Each gives what sesshin relies on, what was observed, the date, and the rule that relies on it.

#### Claude Code 2.1.288

Verified on 2026-10-03, on the native install, with a recording hook in fresh sessions.

- **`CLAUDE_PID` and `CLAUDECODE`.** `CLAUDE_PID` names Claude's own process. `CLAUDECODE` is in Claude's own environment only when another session started it. Relied on by [Assumptions](#assumptions) and [Liveness](#liveness).
- **The hook shell replaces itself.** The shell Claude runs a hook command through execs the command, so `CLAUDE_PID` is the hook's direct parent. Relied on by [Liveness](#liveness) and [Process lookup](implementation-spec.md#process-lookup).
- **`SessionStart`'s payload.** It carries no `permission_mode`, and no `model` under `claude -p`. Relied on by [session-start](hooks-spec.md#session-start).
- **`prompt_id` in hook payloads.** Every hook event of a turn carries the turn's `prompt_id`. A turn Claude starts on its own (a background task finishing) fires `UserPromptSubmit` with a new `prompt_id`. A scheduled wakeup wasn't tested. Relied on by the [straggler guard](#straggler-guard).
- **`Stop` payload fields.** `Stop` always carries `background_tasks` and `session_crons`, as arrays (`[]` when none). `StopFailure` carries neither, so it records 0 for both. Relied on by [stop](hooks-spec.md#stop).
- **Statusline payload shapes.** Recorded every tick of two fresh sessions through a wrapper around herd's statusline. `rate_limits` is the `five_hour`/`seven_day` object, each `{used_percentage, resets_at}` with `resets_at` in Unix seconds; `five_hour` can be missing for a tick right after it resets. `prompt_cache` has the fields listed under [Prompt cache](#prompt-cache): `warm` and `caching_observed` booleans, `expires_at` in Unix seconds, `recache_tokens_if_cold` an integer, `ttl` `"1h"` on this account. Both are missing until the first API response, when `context_window.used_percentage` is `null` and `total_input_tokens` is `0`. Relied on by [Prompt cache](#prompt-cache) and [Rendering](hooks-spec.md#rendering).
- **Statusline ticks.** No tick while the session sits idle. An idle hour brought exactly one, one second after the prompt cache's `expires_at`, with `warm: false` and `expires_at` unchanged. Relied on by [Prompt cache](#prompt-cache) and [statusline](hooks-spec.md#statusline).
- **`idle_prompt`.** It fires 60 seconds after a `Stop`, never while a permission dialog is up, and never after an interrupt (tested with Esc at a dialog, Esc mid-reply, and Ctrl-C). sesshin records nothing for it. Relied on by [Status](#status).

#### Claude Code 2.1.289

Verified on 2026-10-04.

- **`--name`.** Claude shows the name in the prompt box, `/resume`, and the window title, reports it as the statusline's `session_name` and the hooks' `session_title`, and keeps it in the transcript, so `claude --resume` brings it back without `--name`. Relied on by [Launching `claude`](operations.md#launching-claude).
- **An ESC inside a paste.** Text after an embedded `ESC[201~` arrived as keystrokes. Relied on by [send](operations.md#send)'s validation.
- **Enter right after a paste.** Enter sent at once after the paste submits it whole, tested from 11 bytes to 1 MiB (with kitty 0.49.1). Relied on by [send](operations.md#send).
- **What Claude does with a paste.** A paste of several lines shows as `[Pasted text #1]` and reaches the model wrapped in `<pasted_content>` tags after two blank lines; one line arrives as it is. Tabs become four spaces, and a trailing line break is dropped. Described in [send](operations.md#send).
- **Signals end a session with `other`.** SIGTERM, SIGHUP, and SIGTERM to kitty each ran `SessionEnd` with reason `other`, within 10 ms. Relied on by [restart](picker-spec.md#lines)'s `killed`.
- **`prompt_input_exit`.** `/exit`, Ctrl-C twice, and Ctrl-D twice each end with `prompt_input_exit`. Relied on by [restart](picker-spec.md#lines)'s `exited`.

#### kitty 0.49.1

Verified on 2026-10-04.

- **A remote `launch` passes none of the caller's environment.** A variable named alone (`--env=CLAUDECODE`) is set to `_delete_this_env_var_`, not removed. Relied on by [Launching `claude`](operations.md#launching-claude).
- **Foreground processes.** A window running `claude` lists it as its one foreground process, also while it runs a tool's command. Relied on by [send](operations.md#send)'s window lookup.
- **kitty's bracketed paste is per chunk.** `send-text --bracketed-paste` wraps each 2048-byte chunk of a longer text as a paste of its own, with `--stdin` and `--from-file` alike. Relied on by [send](operations.md#send)'s paste.

## Future work

### Another terminal

A tmux backend behind the [placement](#placement) seam.

## File schemas

These are the normative JSON Schemas for the JSON files sesshin writes; the [field tables](#file-fields) describe the same fields in prose. A file that doesn't validate is unusable: in another format when only its `schema` differs from this binary's, else corrupt (see [Format versions](#format-versions)). So is one that breaks a rule its field table states beyond the schema: a `session_id` other than its directory's name, a reservation's `job` other than its file name, `pid_started_at` and `pid` not both `null` or both set, an `end_reason` without `ended_at`, a timestamp that names no real time, a number past a `float64`'s range (outside `extra` and `payload`), an `extra` past its [limits](#user-owned-extra), or a repeated key ([Validation](implementation-spec.md#validation)). Every key is required. Schemas that list open-set values (`status`, `permission_mode`, `last_event_type`, `end_reason`) check only the value's shape, never its value (see [Open sets](#open-sets)).

Shared definitions, referenced below as `defs`:

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "defs",
  "$defs": {
    "timestamp": { "type": "string", "pattern": "^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$" },
    "uuid": { "type": "string", "pattern": "^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$" },
    "job": { "type": "string", "pattern": "^(?![0-9]+$)[A-Za-z0-9](?:[A-Za-z0-9-]{0,62}[A-Za-z0-9])?$" },
    "enum": { "type": "string", "pattern": "^[a-z][a-z0-9_]{0,63}$" },
    "entrypoint": { "type": "string", "pattern": "^[a-z][a-z0-9_-]{0,63}$", "description": "CLAUDE_CODE_ENTRYPOINT's values have hyphens (sdk-cli), so its guard allows them." },
    "text": { "type": "string", "pattern": "^[^\\u0000-\\u001F\\u007F-\\u009F\\u2028\\u2029]*$", "description": "Scrubbed of line breaks and control characters (hooks-spec Reading the payload)." },
    "sesshin_id": { "type": "integer", "minimum": 1, "maximum": 9007199254740991 },
    "placement": {
      "type": ["object", "null"],
      "required": ["terminal"],
      "properties": { "terminal": { "$ref": "#/$defs/enum" } },
      "description": "Checked only for its terminal tag; the other keys belong to the backend."
    }
  }
}
```

**`state.json`:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "state-file",
  "type": "object",
  "required": ["schema", "last_id", "migration"],
  "properties": {
    "schema": { "const": 2 },
    "last_id": { "type": "integer", "minimum": 0, "maximum": 9007199254740991 },
    "migration": { "type": "integer", "minimum": 0, "maximum": 9007199254740991, "description": "The last migration step applied (design-spec Migrations)." }
  },
  "additionalProperties": false
}
```

**`lifecycle.json`:** the fields are described under [`lifecycle.json`](#lifecyclejson).

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "lifecycle-file",
  "type": "object",
  "required": ["schema", "session_id", "cwd", "transcript_path", "session_title", "model", "permission_mode", "pid", "pid_started_at", "entrypoint", "nested", "started_at", "last_start_at", "status", "compactions", "stall_reason", "background_tasks", "session_crons", "last_event_type", "last_event_at", "event_seq", "ended_prompt_id", "ended_at", "end_reason"],
  "properties": {
    "schema": { "const": 1 },
    "session_id": { "$ref": "defs#/$defs/uuid" },
    "cwd": { "anyOf": [{ "$ref": "defs#/$defs/text" }, { "type": "null" }] },
    "transcript_path": { "anyOf": [{ "$ref": "defs#/$defs/text" }, { "type": "null" }] },
    "session_title": { "anyOf": [{ "$ref": "defs#/$defs/text" }, { "type": "null" }] },
    "model": { "anyOf": [{ "$ref": "defs#/$defs/text" }, { "type": "null" }] },
    "permission_mode": { "anyOf": [{ "type": "string", "pattern": "^[A-Za-z][A-Za-z0-9_]{0,63}$" }, { "type": "null" }], "description": "Claude Code's modes are camelCase (acceptEdits), so this guard allows capitals." },
    "pid": { "type": ["integer", "null"], "minimum": 1, "maximum": 9007199254740991 },
    "pid_started_at": { "$ref": "#/$defs/pid_started_at" },
    "entrypoint": { "anyOf": [{ "$ref": "defs#/$defs/entrypoint" }, { "type": "null" }], "description": "CLAUDE_CODE_ENTRYPOINT, verbatim (cli, sdk-cli, …)." },
    "nested": { "type": ["boolean", "null"], "description": "Started by another session." },
    "started_at": { "$ref": "defs#/$defs/timestamp" },
    "last_start_at": { "$ref": "defs#/$defs/timestamp" },
    "status": { "$ref": "defs#/$defs/enum" },
    "compactions": { "type": "integer", "minimum": 0, "maximum": 9007199254740991 },
    "stall_reason": { "anyOf": [{ "$ref": "defs#/$defs/enum" }, { "type": "null" }] },
    "background_tasks": { "type": ["integer", "null"], "minimum": 0, "maximum": 9007199254740991 },
    "session_crons": { "type": ["integer", "null"], "minimum": 0, "maximum": 9007199254740991 },
    "last_event_type": { "type": "string", "pattern": "^[a-z][a-z0-9_]{0,63}(?::[a-z][a-z0-9_]{0,63})?$" },
    "last_event_at": { "$ref": "defs#/$defs/timestamp" },
    "event_seq": { "type": "integer", "minimum": 1, "maximum": 9007199254740991 },
    "ended_prompt_id": { "anyOf": [{ "$ref": "defs#/$defs/text" }, { "type": "null" }] },
    "ended_at": { "anyOf": [{ "$ref": "defs#/$defs/timestamp" }, { "type": "null" }] },
    "end_reason": { "anyOf": [{ "$ref": "defs#/$defs/enum" }, { "type": "null" }] }
  },
  "additionalProperties": false,
  "$defs": {
    "pid_started_at": { "type": ["string", "null"], "maxLength": 128, "description": "The process's start time, prefixed by the boot it belongs to, compared only for equality: linux:<boot_id>:<starttime> (boot_id from /proc/sys/kernel/random/boot_id, starttime field 22 of /proc/<pid>/stat, in clock ticks) or darwin:<kern.boottime sec>.<usec>:<p_starttime sec>.<usec>. The boot prefix makes a pid reused after a reboot never match. null exactly when pid is." }
  }
}
```

**`statusline.json`:** `payload` is Claude Code's, and is not validated beyond being an object.

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "statusline-file",
  "type": "object",
  "required": ["schema", "received_at", "received_ns", "payload", "git_branch", "cost_sample", "burn_usd_per_hour", "pid", "pid_started_at"],
  "properties": {
    "schema": { "const": 1 },
    "received_at": { "$ref": "defs#/$defs/timestamp" },
    "received_ns": { "type": "integer", "minimum": 0, "maximum": 9223372036854775807, "description": "Unix nanoseconds when the tick started; orders overlapping ticks." },
    "payload": { "type": "object" },
    "git_branch": { "anyOf": [{ "$ref": "defs#/$defs/text" }, { "type": "null" }] },
    "cost_sample": {
      "type": ["object", "null"],
      "required": ["at", "usd"],
      "properties": {
        "at": { "$ref": "defs#/$defs/timestamp" },
        "usd": { "type": "number", "minimum": 0 }
      },
      "additionalProperties": false
    },
    "burn_usd_per_hour": { "type": ["number", "null"], "minimum": 0 },
    "pid": { "type": ["integer", "null"], "minimum": 1, "maximum": 9007199254740991 },
    "pid_started_at": { "$ref": "lifecycle-file#/$defs/pid_started_at" }
  },
  "additionalProperties": false
}
```

**`sesshin.json`:** `placement` is checked only for its `terminal` tag. Its other keys belong to the backend, which validates them itself and treats a placement it can't use as `null`. `extra` is checked only for being an object within its [limits](#user-owned-extra).

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "sesshin-file",
  "type": "object",
  "required": ["schema", "id", "job", "source", "placement", "extra"],
  "properties": {
    "schema": { "const": 2 },
    "id": { "anyOf": [{ "$ref": "defs#/$defs/sesshin_id" }, { "type": "null" }], "description": "null only while an issue is pending (hooks-spec Late adoption)." },
    "job": { "anyOf": [{ "$ref": "defs#/$defs/job" }, { "type": "null" }] },
    "source": { "enum": ["spawn", "hook"] },
    "placement": { "$ref": "defs#/$defs/placement" },
    "extra": { "type": "object", "description": "User-owned; sesshin never reads its contents (User-owned extra)." }
  },
  "additionalProperties": false
}
```

**`reservations/<key>.json`:** the file name is the `job`'s [key](#reservations), the job lowercased.

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "reservation-file",
  "type": "object",
  "required": ["schema", "job", "token", "created_at", "placement"],
  "properties": {
    "schema": { "const": 1 },
    "job": { "$ref": "defs#/$defs/job" },
    "token": { "type": "string", "pattern": "^[0-9a-f]{32}$", "description": "Random; passed to the launch as SESSHIN_TOKEN." },
    "created_at": { "$ref": "defs#/$defs/timestamp" },
    "placement": { "$ref": "defs#/$defs/placement", "description": "The launched window; null until the launch returns." }
  },
  "additionalProperties": false
}
```

**`install.json`:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "install-file",
  "type": "object",
  "required": ["schema", "hook_binary", "version", "installed_at", "locations"],
  "properties": {
    "schema": { "const": 1 },
    "hook_binary": { "type": "string", "pattern": "^/", "description": "The sesshin-hook every hook runs." },
    "version": { "type": "string" },
    "installed_at": { "$ref": "defs#/$defs/timestamp" },
    "locations": {
      "type": "object",
      "required": ["config_dir", "state_dir", "claude_settings"],
      "properties": {
        "config_dir": { "type": "string", "pattern": "^/" },
        "state_dir": { "type": "string", "pattern": "^/" },
        "claude_settings": { "type": "string", "pattern": "^/", "description": "The settings.json install proposed changes to." }
      },
      "additionalProperties": false
    }
  },
  "additionalProperties": false
}
```
