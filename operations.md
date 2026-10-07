# sesshin operations

An operation is a single query of, or change to, what the [design spec](design-spec.md) defines. Operations are the domain layer, small and orthogonal. They are not CLI commands; [cli-spec.md](cli-spec.md) maps commands onto them.

The operations are the two that read the sessions sesshin recorded, [`list`](#list) and [`show`](#show), and [`version`](#version); the two that propose wiring sesshin into Claude Code, [`install`](#install) and [`uninstall`](#uninstall); [`spawn`](#spawn) and [`resume`](#resume), which launch sessions; [`send`](#send), which types into one; [`update`](#update), which changes a session's user-owned `extra`; and [`prune`](#prune), which cleans up after them. The other session operations (`focus`, and the planned `wait`), the diagnostic ones (`doctor`, `repair`), and `info` are [deferred](deferred/operations.md), with the rules and kinds only they use, such as findings.

Hooks are not operations. They are sesshin's writers of what Claude Code reports, with their own contract, in [hooks-spec.md](hooks-spec.md).

Terms follow the design spec's [Terms](design-spec.md#terms).

## Conventions

- **JSON in, JSON out.** Input and output are JSON with published schemas, so an agent can build requests and parse results without scraping text. Every result is wrapped in the [output envelope](#output-envelope).
- **Schema identifiers.** Shared schemas have short `$id`s (`envelope`, `error`, `warning`, `selector`, `session-ref`, `session-view`, `session-projection`). Each operation's are `<op>-input` and `<op>-output`. An operation's schema may refer to the design spec's `defs` (`defs#/$defs/job`).
- **Referring to operations and kinds.** Operation names, error kinds, and warning kinds are written in code (`install`, `corrupt`), linked on their first mention in a section. Error qualifiers are written `` `kind` (`field`: `value`) ``, e.g. `self-test-failed` (`hook`: `statusline`).
- **Parameters.** An operation takes a parameter only if it changes the meaning of the result or the work done.
- **Versioning.** The schemas in this document are sesshin's public contract (see [Versioning](#versioning)).

## Operation kinds

- ***read*** — Takes no lock and changes nothing: [`list`](#list), [`show`](#show), and [`version`](#version).
- ***setup*** — Proposes changes to Claude Code's configuration so sesshin's hooks run, for you to apply: [`install`](#install) and [`uninstall`](#uninstall). They read Claude Code's `settings.json` and never write it, write only their own files in the state directory, take no sesshin lock, and define their own error precedence.
- ***write*** — Changes the state directory, the terminal, or both: [`spawn`](#spawn) and [`resume`](#resume) open windows, [`send`](#send) types into one and writes no file, [`update`](#update) rewrites one session's `sesshin.json` under its session lock, and [`prune`](#prune) removes sessions. They hold the state lock only for a few file operations (and a read of every session), never across a launch or a wait. `prune` only *tries* its locks, so it never waits; `spawn` and `resume` wait for the state lock briefly, and `update` for its session's lock ([Locks](design-spec.md#locks)), and fail [`busy`](#error-kinds) past that.

The deferred operations add the ***diagnostic*** kind back, and more write operations (see [deferred/operations.md](deferred/operations.md)).

## Operation template

Every operation is specified with the same parts, in this order. Every part is always present except **Order**, which appears only for operations that return a collection (`list`'s `sessions`, `install`'s and `uninstall`'s `changes`, `prune`'s `pruned`). An empty part is written `**Part:** none.`, optionally followed by one sentence saying why.

| Part | Content |
|---|---|
| **Summary** | Unlabeled first paragraph: what the operation does, in one or two sentences. |
| **Kind** | One of the [operation kinds](#operation-kinds), and the lock it takes, if any. |
| **Input schema** | JSON Schema (draft 2020-12), `$id` `<op>-input`. |
| **Additional validation** | Input rules the schema can't express. All raise `invalid-input`. |
| **Preconditions** | State that must hold beforehand; the matching errors are under Errors. |
| **Effects** | The resulting state — in the state directory, the terminal, or Claude Code's configuration — in data-model terms. |
| **Output schema** | JSON Schema of `result`, `$id` `<op>-output`, or a reference to a shared schema. |
| **Order** | Collections only: the order of the items. |
| **Errors** | Table of [error kinds](#error-kinds) and when each is raised, in [precedence](#precedence) order. `io` and `internal` are omitted: any operation can raise them. |
| **Warnings** | Table of [warning kinds](#warning-kinds) and when each is reported. |
| **Retry safety** | Whether running it again after an error, a crash, or an unclear outcome is safe, and with what result. |


## Output envelope

Every operation returns one of two shapes:

```text
{ "ok": true,  "result": { }, "warnings": [ ] }
{ "ok": false, "error":  { }, "warnings": [ ] }
```

- **`result`** — the operation's output, per its Output schema.
- **`error`** — why it failed, per the [error schema](#error-schema). An error means the operation's effects did not take place, except as its Errors table says.
- **`warnings`** — problems that did not stop it, per the [warning schema](#warning-schema). Present, possibly empty, on success and failure alike.

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "envelope",
  "oneOf": [
    {
      "type": "object",
      "required": ["ok", "result", "warnings"],
      "properties": {
        "ok": { "const": true },
        "result": { "type": "object" },
        "warnings": { "type": "array", "items": { "$ref": "warning" } }
      },
      "additionalProperties": false
    },
    {
      "type": "object",
      "required": ["ok", "error", "warnings"],
      "properties": {
        "ok": { "const": false },
        "error": { "$ref": "error" },
        "warnings": { "type": "array", "items": { "$ref": "warning" } }
      },
      "additionalProperties": false
    }
  ]
}
```

## Errors

`kind` and `details` are the contract; `message` is for people and may change between releases. A caller treats an unknown kind as a generic failure.

### Error kinds

| Kind | Meaning | `details` |
|---|---|---|
| `invalid-input` | Input failed validation. Raised before any lock is sought or any file is read. Reports every problem, not just the first. | `problems`: `{field, reason}` list, `field` a JSON Pointer into the input; sorted by `field`, then `reason`; at most 20, with `problems_truncated: true` past that. |
| `environment` | The process's environment lacks what sesshin needs to find its files: a usable `HOME` (see [Locations](design-spec.md#locations)). | `variable`: currently always `HOME`. |
| `not-found` | A session or path named by the input does not exist. | `sessions`: the [selectors](#selecting-a-session) that matched nothing; `paths`: the paths, as given, that don't exist or aren't what the operation needs. Both always present, possibly empty. |
| `ambiguous` | A selector matched more than one session. | `selector`; `candidates`: the matching sessions as [session refs](#session-ref), in [session order](#session-order), at most 20, with `candidates_truncated: true` past that. |
| `conflict` | The operation was refused because of the state it found. | `rule`: `job-taken` (a live session or a fresh reservation holds the job), `live` (the session to [`resume`](#resume) is live, or its liveness is `unknown`), `not-live` (the session to [`send`](#send) to has ended), `mid-turn` (its turn hasn't ended), `no-placement` (sesshin doesn't know its window), `no-sesshin-file` (the session to [`update`](#update) has no usable `sesshin.json`), or `extra-too-large` (`update`'s result would break `extra`'s [limits](design-spec.md#user-owned-extra)). `sessions`: the sessions involved, as [session refs](#session-ref), possibly empty. With `no-sesshin-file`, also `path`: the `sesshin.json`, and `file`: `missing` or `unusable`. |
| `busy` | Another process held a lock this write needs for longer than it waits. Safe to retry. | `lock`: `state`, or `session` (for [`update`](#update)). |
| `terminal` | The terminal backend could not do what was asked. | `reason`: `unavailable` (no backend recognizes the caller's terminal: not in kitty, remote control off, or under tmux or screen), `launch-failed` (the backend refused to open the window; nothing was opened), `launch-unknown` (the launch timed out, or its answer named no window; one may have opened), and for [`send`](#send): `unreachable` (no window with the session's pid was found; nothing was typed), `send-failed` (the paste failed; some text may have been typed), `submit-failed` (the text was pasted, but Enter failed). `terminal`: the backend's tag, or `null`; `detail`: human-readable. |
| `corrupt` | A file sesshin needs is present and readable, but its content is wrong: `config.toml`, `hooks.properties`, or Claude Code's `settings.json`. | `path`; `detail`: human-readable. |
| `io` | The environment refused an operation: permission denied, disk full, and the like. | `path`; `code`: the symbolic OS error, e.g. `EACCES`. |
| `self-test-failed` | [`install`](#install)'s self-test found a hook that doesn't work, so nothing was proposed: `sesshin-hook` is missing beside `sesshin`, is from another build or one that can't be identified, or a verb misbehaved. | `path`: the `sesshin-hook` tested; `hook`: the verb, or `null` when `sesshin-hook` itself is missing, from another build, or unidentifiable; `detail`: human-readable, what it did or wrote. |
| `internal` | A bug sesshin detects. | none (`{}`). |

`usage` is a CLI-only kind, raised for a malformed command line (see [cli-spec.md](cli-spec.md)). The deferred operations add more `conflict` rules and `terminal` reasons (see [deferred/operations.md](deferred/operations.md#errors)).

### Precedence

Each operation's Errors table lists its checks in the order it makes them, and an error is the first check that fails. `invalid-input` always comes first, then `environment`.

### Error schema

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "error",
  "type": "object",
  "required": ["kind", "message", "details"],
  "properties": {
    "kind": { "type": "string" },
    "message": { "type": "string", "description": "Human-readable; not part of the contract." },
    "details": { "type": "object" }
  },
  "additionalProperties": false
}
```

## Warnings

A warning is a problem an operation worked around. It never changes the exit status.

### Warning kinds

| Kind | Meaning | `details` |
|---|---|---|
| `unusable-file` | A session file or a reservation could not be read, or is not one this binary can use. [`list`](#list) and [`show`](#show) leave the session out when it is its `lifecycle.json`, and otherwise show it with what was readable; [`prune`](#prune) can't judge a session whose `lifecycle.json` it is, and keeps it, and removes an unusable reservation. The session's next hook rewrites the file (see [Format versions](design-spec.md#format-versions)); an ended session's never will. | `path`. |
| `duplicate-id` | [`list`](#list) found several sessions with the same sesshin ID, which only an [outside change](design-spec.md#assumptions) makes. Each is listed. | `id`; `sessions`: [session refs](#session-ref), in [session order](#session-order). |
| `not-started` | [`spawn`](#spawn) launched its session, or [`resume`](#resume) resumed one, but didn't see it start within `start_timeout_secs`. | `job`, or `null`; `placement`: the launched window; `waited_secs`; for `resume`, `session`: a [session ref](#session-ref). |
| `transcript-missing` | [`resume`](#resume) launched a session whose transcript isn't where it was recorded. | `session`: a [session ref](#session-ref); `path`: the `transcript_path`. |
| `placement-not-recorded` | [`spawn`](#spawn) or [`resume`](#resume) launched its session, but couldn't record the window in its reservation, which then goes stale 120 seconds after `created_at` unless the session adopts it first. | `job`; `placement`. |
| `status-line-replaced` | [`install`](#install)'s proposal replaces a `statusLine` sesshin didn't install. Applying it removes that one, and sesshin keeps no copy: this warning is the record. | `settings_path`; `status_line`: the replaced value, verbatim. |

The deferred operations' kinds are in [deferred/operations.md](deferred/operations.md#warnings).

### Warning schema

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "warning",
  "type": "object",
  "required": ["kind", "message", "details"],
  "properties": {
    "kind": { "type": "string" },
    "message": { "type": "string" },
    "details": { "type": "object" }
  },
  "additionalProperties": false
}
```

## Versioning

The schemas here, the error and warning kinds, and the envelope are sesshin's public contract. Adding an optional input field, an output field, an error or warning kind, or an enum value an [open set](design-spec.md#open-sets) allows is a minor change; callers must ignore what they don't know. Anything else is a major change. Before 1.0, as the design spec's [Format versions](design-spec.md#format-versions) says, any of it may change in a minor release.

## Shared rules

### Selecting a session

[`show`](#show), [`resume`](#resume), [`send`](#send), and [`update`](#update) take a **selector**: a string naming one session.

| Form | Matches |
|---|---|
| `12` | The session whose [sesshin ID](design-spec.md#sesshin-ids) is 12. Decimal digits with no leading zero. `#12` is the display form, not a selector. |
| `0b6c5a3e-1f7e-4c2b-9a51-6d2f0e8b7c11` | The session with that UUID. |
| `0b6c5a3e` | Every session whose UUID starts with it: 8 to 36 characters of hex digits and hyphens, a prefix of the UUID as written. |
| `api`, `job:api` | One session that reports the [job](design-spec.md#reservations) `api`: the live one, if one holds it, else the most recently [seen](design-spec.md#liveness). |

- **All digits is always a sesshin ID,** even when it is also 8 or more characters of hex. A UUID prefix that happens to be all digits is given one character longer.
- **8 to 36 characters of hex and hyphens is always a UUID prefix.** A job that looks like one (`deadbeef`, `cafe-1234`) is selected as `job:deadbeef`. Any other [job name](design-spec.md#reservations) is a job, bare or after `job:`.
- **Case doesn't matter** for a UUID or a prefix: it is lowercased before matching, as session UUIDs are stored. A job name is matched exactly, case included, though jobs that differ only in case are [one job](design-spec.md#reservations) for holding it: `api` doesn't select a session whose job is `API`.
- **A job selects one session, never `ambiguous`.** Every `/clear` in a job's window ends a session that keeps reporting the job, so after a day's work a job names many ended sessions. The one meant is the live one, which a job names at most one of, else the last one seen; ties break as in [session order](#session-order). Jobs are as readers report them.
- **Exact, never fuzzy.** Finding a session by its title or name is the pickers' job (fzf), not a selector's.
- **Within the operation's scope.** `show` and `update` select among live, ended, and headless sessions alike; `resume` among ended ones only, and `send` among live ones (and liveness `unknown`) only; their Preconditions say how they report the others. A selector that matches nothing in scope is `not-found`.
- **One, or `ambiguous`.** A sesshin ID or UUID prefix matching several sessions, a UUID prefix shared by several or a sesshin ID that an [outside change](design-spec.md#assumptions) duplicated, is `ambiguous`, listing them.

Anything else, a sesshin ID with a leading zero or above 2^53 − 1, or a string that is none of these forms, is `invalid-input`.

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "selector",
  "type": "string",
  "anyOf": [
    { "pattern": "^[1-9][0-9]{0,15}$" },
    { "pattern": "^[0-9A-Fa-f-]{8,36}$" },
    { "pattern": "^(job:)?(?![0-9]+$)[A-Za-z0-9](?:[A-Za-z0-9-]{0,62}[A-Za-z0-9])?$" }
  ]
}
```

### Reading the sessions

Every read walks `sessions/`: one directory per session, three small files each. Hidden entries are ignored. There is no index, so selecting one session reads every `sesshin.json` to find its sesshin ID. With [retention](design-spec.md#retention), that is a few hundred small files, as long as `prune` runs.

- **A session needs a usable `lifecycle.json`.** A session directory without one is skipped: silently when there is none (a session being created, or a leftover), with an `unusable-file` warning when it is there and unusable. Such a session is in no result, and no selector matches it.
- **The other two files are optional.** An unusable `sesshin.json` reads as missing (`id` `null`, `job` `null`, `source` `null`, `placement` `null`, `extra` `null`), and an unusable `statusline.json` as missing (no metrics, no prompt cache, no pid fallback), each with an `unusable-file` warning.
- **A directory that vanishes mid-read** was pruned, and is skipped without a warning.

Liveness costs one `kill(pid, 0)` and one process start-time read per session with a pid, and a `stat` of each `transcript_path`.

### Narrowing

[`list`](#list), like koan's, takes the few parameters an agent needs to keep its result small, since its output goes straight into the agent's context:

- **`liveness`** — `live` (the default; includes liveness `unknown`), `ended`, or `all`.
- **`include_headless`** — `true` to include [headless](design-spec.md#terms) sessions, live or ended. Hidden by default: an agent's `claude -p` workers aren't sessions anyone watches, and there can be hundreds.
- **`fields`** — return each session as a [session projection](#session-projection): only these top-level fields of its [session view](#session-view), in the view's order, plus `id` and `session_id`, which are always present.
- **`limit`** — at most this many sessions, a prefix of [session order](#session-order). `0` returns none, for the count alone, in `total`.

Narrowing never changes liveness, the order, or the warnings: a warning about a session the narrowing leaves out is still reported. Anything finer is left to `jq`.

### Session order

Live sessions, liveness `unknown` included, come first, then ended ones. Within each, the most recently [seen](design-spec.md#liveness) first. Ties break by sesshin ID, lowest first and `null` last, then by `session_id`.

### Launching `claude`

[`spawn`](#spawn) and [`resume`](#resume) start `claude` in a new window through the user's login shell, so it gets the same `PATH` and environment as a tab opened by hand. No shell ever parses any part of the command line, and nothing of the caller's environment reaches the new window.

- **The arguments are passed out of band.** The backend runs `spawn_shell` (see [`config.toml`](design-spec.md#configtoml)) with its own arguments, then `-c`, a fixed script, then the argument vector:
  - a POSIX-family shell: `<spawn_shell…> -c 'exec "$@"' sesshin claude <arg…>` (`sesshin` fills `$0`);
  - `fish`, judged by the base name of `spawn_shell`'s first word: `<spawn_shell…> -c 'exec $argv' claude <arg…>`.

  Nothing sesshin or the caller supplies is put into the script, so `$(…)`, quotes, globs, and `~` in a prompt or an argument reach `claude` exactly as given.
- **The name comes first, and the prompt follows `--`.** The arguments are `[--name <name>] <args…> -- <prompt>`, or without `-- <prompt>` when there is no prompt. `--name` is `spawn`'s `name`, else its job; Claude Code shows it in the prompt box, `/resume`, and the window title, reports it as the statusline's `session_name` and the hooks' `session_title`, and keeps it in the transcript, so a `claude --resume` brings it back without it ([verified](design-spec.md#claude-code-21289)). A `--name` in `args` comes later, and `claude` takes that one. A prompt that begins with `-` is then never read as an option, and an `args` list that ends in an option taking a value (`--model`) makes `claude` take `--` as that value and fail visibly in the new window, rather than swallow the prompt. sesshin passes `args` in order and never interprets them.
- **`resume` puts `--resume <uuid>` first.** Its arguments are `--resume <uuid> <args…>`, with no prompt. An `args` list that ends in an option taking a value then has none, and `claude` fails visibly in the new window, rather than taking `--resume` as the value and starting a new session. `args` that resume or continue another session (`--continue`, a second `--resume`) are passed like any others: `claude` decides.
- **The environment is the terminal's own,** never the caller's. kitty starts the window with its own environment (sesshin never passes `--copy-env`), plus `SESSHIN_JOB` and `SESSHIN_TOKEN` when there is a job, and `SESSHIN_EXTRA` when `spawn` has an `extra`, and sesshin passes no other `--env`. So an agent running `sesshin spawn` from a session launched with an `extra` never passes its own on: the new session's `extra` is only what `spawn` was given. A caller that is itself a Claude session (an agent running `sesshin spawn`) would otherwise make the new one read as [nested](design-spec.md#liveness), with no job and no placement. A remote `launch` passes none of the caller's variables ([verified](design-spec.md#kitty-0491)). It also can't remove one: a variable named alone (`--env=CLAUDECODE`) is set to `_delete_this_env_var_`, which would make the session nested, so sesshin names none. A kitty started from inside a Claude session would pass its own `CLAUDECODE` on; that is kitty's environment, and out of sesshin's reach.
- **The kitty launch** is one `kitten @ --to <socket> launch`, with `socket` the caller's `KITTY_LISTEN_ON`: `--type` `tab`, `window` (for `split`), or `os-window`; `--self`, so a tab or split goes beside the caller's window rather than the focused one; `--keep-focus`, so the caller keeps working; `--cwd`, `--tab-title` for a tab or OS window when there is a name, `spawn`'s or the title `resume` reopens under (a split keeps its tab's), one `--var` per user variable, and `--env` for the variables above. It prints the new window's ID, which with the socket is the launched window's placement. A nonzero exit is `launch-failed`; the 10-second limit passing, or output that isn't a positive integer, is `launch-unknown`.

### sesshin's entries in settings.json

`install`'s and `uninstall`'s proposals change only the entries in `settings.json` that sesshin owns. A hook entry's `command`, or `statusLine.command`, is sesshin's when it splits, by POSIX shell word rules, into exactly `[P, <verb>]` (`<verb>` is `statusline` for `statusLine`), where `P` is the `sesshin-hook` beside this `sesshin`, the `hook_binary` recorded in `install.json`, or any path whose base name is `sesshin-hook`. Anything else, of any tool, is not sesshin's, and that includes herd's shim script, whose entries you remove by hand.

A `statusLine` is "already sesshin's" by the same test. One that runs something else is the user's: `install`'s proposal replaces it, with a [`status-line-replaced`](#warning-kinds) warning.

## Shared schemas

### Session ref

The smallest way to name a session in an error.

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "session-ref",
  "type": "object",
  "required": ["id", "session_id", "name"],
  "properties": {
    "id": { "type": ["integer", "null"], "minimum": 1 },
    "session_id": { "type": "string" },
    "name": { "type": "string" }
  },
  "additionalProperties": false
}
```

### Session view

One session, as every read reports it: what is stored, and what is derived from it at read time, in this order. Fields from `sesshin.json` are `null` without a usable one, and fields from `statusline.json` before its first tick or without a usable one.

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "session-view",
  "type": "object",
  "required": ["id", "session_id", "name", "job", "source", "extra", "headless", "liveness", "status", "stall_reason", "pending", "cwd", "git_branch", "model", "permission_mode", "entrypoint", "nested", "pid", "started_at", "last_start_at", "last_event_at", "last_event_type", "event_seq", "last_seen", "ended_at", "end_reason", "compactions", "metrics", "prompt_cache", "placement", "transcript_path", "transcript_exists"],
  "properties": {
    "id": { "type": ["integer", "null"], "minimum": 1, "description": "Sesshin ID, from sesshin.json; null while an issue is pending or without a usable sesshin.json." },
    "session_id": { "type": "string", "description": "Claude's session UUID, lowercase." },
    "name": { "type": "string", "description": "Derived (design-spec Terms): the /rename title, else the statusline's session name, else #id, else the UUID's first 8 characters." },
    "job": { "type": ["string", "null"], "description": "Derived (design-spec Reservations): sesshin.json's job, or null for a live or unknown session whose job a live or unknown session that started earlier holds." },
    "source": { "enum": ["spawn", "hook", null], "description": "From sesshin.json; null without a usable one." },
    "extra": { "type": ["object", "null"], "description": "From sesshin.json, as stored, its key order and numbers kept (design-spec User-owned extra); null without a usable one." },
    "headless": { "type": "boolean", "description": "Derived (design-spec Terms): nested, or an sdk-… entrypoint. Hidden from list unless include_headless." },
    "liveness": { "enum": ["live", "ended", "unknown"], "description": "Derived (design-spec Liveness)." },
    "status": { "type": "string", "description": "Open set: idle, working, waiting, needs_approval, or a value this binary doesn't know. The last recorded status, also for an ended session." },
    "stall_reason": { "type": ["string", "null"] },
    "pending": {
      "type": ["object", "null"],
      "required": ["background_tasks", "session_crons"],
      "properties": { "background_tasks": { "type": "integer" }, "session_crons": { "type": "integer" } },
      "additionalProperties": false,
      "description": "null before any turn has ended, and while a turn is under way."
    },
    "cwd": { "type": ["string", "null"] },
    "git_branch": { "type": ["string", "null"], "description": "From statusline.json." },
    "model": { "type": ["string", "null"], "description": "Derived: the statusline's model.id when its payload is from the session's current life (received_at at or after last_start_at), else SessionStart's." },
    "permission_mode": { "type": ["string", "null"] },
    "entrypoint": { "type": ["string", "null"] },
    "nested": { "type": ["boolean", "null"] },
    "pid": { "type": ["integer", "null"], "description": "The pid liveness judged: lifecycle.json's, else statusline.json's." },
    "started_at": { "type": "string" },
    "last_start_at": { "type": "string" },
    "last_event_at": { "type": "string" },
    "last_event_type": { "type": "string" },
    "event_seq": { "type": "integer", "minimum": 1 },
    "last_seen": { "type": "string", "description": "The later of last_event_at and the statusline's received_at." },
    "ended_at": { "type": ["string", "null"], "description": "When SessionEnd reported it; null for a live session and for one whose process just vanished." },
    "end_reason": { "type": ["string", "null"], "description": "As SessionEnd reported it, or superseded (derived): another session started later in the same process without this one's SessionEnd being recorded." },
    "compactions": { "type": "integer", "minimum": 0 },
    "metrics": {
      "type": ["object", "null"],
      "required": ["received_at", "cost_usd", "burn_usd_per_hour", "api_duration_ms", "context_tokens", "context_window", "context_percent", "rate_limits"],
      "properties": {
        "received_at": { "type": "string" },
        "cost_usd": { "type": ["number", "null"], "description": "cost.total_cost_usd." },
        "burn_usd_per_hour": { "type": ["number", "null"], "description": "As the last statusline tick computed it (design-spec statusline.json); null when it can't be computed honestly." },
        "api_duration_ms": { "type": ["integer", "null"], "description": "cost.total_api_duration_ms." },
        "context_tokens": { "type": ["integer", "null"], "description": "context_window.total_input_tokens: the window's current fill." },
        "context_window": { "type": ["integer", "null"], "description": "context_window.context_window_size." },
        "context_percent": { "type": ["number", "null"], "description": "context_window.used_percentage." },
        "rate_limits": { "type": ["object", "null"], "description": "payload.rate_limits, as reported." }
      },
      "additionalProperties": false
    },
    "prompt_cache": {
      "type": ["object", "null"],
      "description": "Derived (design-spec Prompt cache); null without a statusline.json.",
      "required": ["state", "expires_at", "recache_tokens", "hit_ratio", "misses", "last_miss_cause"],
      "properties": {
        "state": { "enum": ["warm", "cold", "unknown"] },
        "expires_at": { "type": ["string", "null"], "description": "While warm, expires_at as a timestamp, its fraction dropped; null otherwise." },
        "recache_tokens": { "type": ["integer", "null"], "description": "recache_tokens_if_cold, when a non-negative integer." },
        "hit_ratio": { "type": ["number", "null"] },
        "misses": { "type": ["integer", "null"] },
        "last_miss_cause": { "type": ["array", "null"], "items": { "type": "string" }, "description": "last_miss_cause.causes, as reported." }
      },
      "additionalProperties": false
    },
    "placement": { "$ref": "defs#/$defs/placement" },
    "transcript_path": { "type": ["string", "null"] },
    "transcript_exists": { "type": ["boolean", "null"], "description": "Whether transcript_path names an existing file, so a claude --resume can find it; null when transcript_path is, or when the stat failed other than with ENOENT." }
  },
  "additionalProperties": false
}
```

A payload value of the wrong type reads as `null`, as in the statusline: `cost_usd` is a number or `null`, never a string.

### Session projection

Some of a [session view](#session-view)'s fields, always including `id` and `session_id`, in the view's order. Returned by [`list`](#list) in place of session views when `fields` is given (see [Narrowing](#narrowing)); each field present has exactly its session view meaning and value.

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "session-projection",
  "type": "object",
  "required": ["id", "session_id"],
  "properties": {
    "id": { "$ref": "session-view#/properties/id" },
    "session_id": { "$ref": "session-view#/properties/session_id" },
    "name": { "$ref": "session-view#/properties/name" },
    "job": { "$ref": "session-view#/properties/job" },
    "source": { "$ref": "session-view#/properties/source" },
    "extra": { "$ref": "session-view#/properties/extra" },
    "headless": { "$ref": "session-view#/properties/headless" },
    "liveness": { "$ref": "session-view#/properties/liveness" },
    "status": { "$ref": "session-view#/properties/status" },
    "stall_reason": { "$ref": "session-view#/properties/stall_reason" },
    "pending": { "$ref": "session-view#/properties/pending" },
    "cwd": { "$ref": "session-view#/properties/cwd" },
    "git_branch": { "$ref": "session-view#/properties/git_branch" },
    "model": { "$ref": "session-view#/properties/model" },
    "permission_mode": { "$ref": "session-view#/properties/permission_mode" },
    "entrypoint": { "$ref": "session-view#/properties/entrypoint" },
    "nested": { "$ref": "session-view#/properties/nested" },
    "pid": { "$ref": "session-view#/properties/pid" },
    "started_at": { "$ref": "session-view#/properties/started_at" },
    "last_start_at": { "$ref": "session-view#/properties/last_start_at" },
    "last_event_at": { "$ref": "session-view#/properties/last_event_at" },
    "last_event_type": { "$ref": "session-view#/properties/last_event_type" },
    "event_seq": { "$ref": "session-view#/properties/event_seq" },
    "last_seen": { "$ref": "session-view#/properties/last_seen" },
    "ended_at": { "$ref": "session-view#/properties/ended_at" },
    "end_reason": { "$ref": "session-view#/properties/end_reason" },
    "compactions": { "$ref": "session-view#/properties/compactions" },
    "metrics": { "$ref": "session-view#/properties/metrics" },
    "prompt_cache": { "$ref": "session-view#/properties/prompt_cache" },
    "placement": { "$ref": "session-view#/properties/placement" },
    "transcript_path": { "$ref": "session-view#/properties/transcript_path" },
    "transcript_exists": { "$ref": "session-view#/properties/transcript_exists" }
  },
  "additionalProperties": false
}
```

### Placement

Opaque outside its [terminal backend](design-spec.md#placement): a caller may show it, and reads nothing from it but `terminal`. It is reported as stored; the backend's validation applies only where sesshin acts on it.

Its schema is [`defs#/$defs/placement`](design-spec.md#file-schemas), the one `sesshin.json` and reservations are checked against.

## Read operations

### list

Return the sessions sesshin has recorded, live by default, in [session order](#session-order).

**Kind:** read. Takes no lock.

**Input schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "list-input",
  "type": "object",
  "properties": {
    "liveness": { "enum": ["live", "ended", "all"], "default": "live", "description": "live includes liveness unknown." },
    "include_headless": { "type": "boolean", "default": false, "description": "Include headless sessions (design-spec Terms), live or ended." },
    "fields": { "type": "array", "items": { "type": "string" }, "uniqueItems": true, "description": "Top-level session-view fields to include; id and session_id always are. Default: all." },
    "limit": { "type": "integer", "minimum": 0, "maximum": 9007199254740991, "description": "At most this many sessions; absent, no limit." }
  },
  "additionalProperties": false
}
```

**Additional validation:** each of `fields` names a property of the [session view](#session-view).

**Preconditions:** none.

**Effects:** none.

**Output schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "list-output",
  "type": "object",
  "required": ["sessions", "total", "truncated"],
  "properties": {
    "sessions": { "type": "array", "items": { "anyOf": [{ "$ref": "session-view" }, { "$ref": "session-projection" }] }, "description": "Session views, or session projections with fields. May be empty." },
    "total": { "type": "integer", "minimum": 0, "description": "How many sessions matched liveness and include_headless, before the limit." },
    "truncated": { "type": "boolean", "description": "Whether the limit left some out: total is more than the number returned." }
  },
  "additionalProperties": false
}
```

**Order:** [session order](#session-order). A `limit` takes a prefix of it.

**Errors,** in this order:

| Kind | When |
|---|---|
| `invalid-input` | A bad `liveness`, `fields` entry, or `limit`. |
| `environment` | `HOME` is unusable. |

A missing state directory or `sessions/` is not an error: there are no sessions yet, and `sessions` is empty.

**Warnings:**

| Kind | When |
|---|---|
| `unusable-file` | A session file is unusable: the session is left out (`lifecycle.json`), or listed with what was readable. |
| `duplicate-id` | Several listed sessions share a sesshin ID. |

**Retry safety:**

- After anything: safe. It changes nothing.

### show

Return one session in full, live or ended, and its raw statusline payload when asked.

**Kind:** read. Takes no lock.

**Input schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "show-input",
  "type": "object",
  "required": ["session"],
  "properties": {
    "session": { "$ref": "selector" },
    "include_payload": { "type": "boolean", "default": false, "description": "Also return statusline.json's payload, verbatim." }
  },
  "additionalProperties": false
}
```

**Additional validation:** `session` is a selector by the rules of [Selecting a session](#selecting-a-session).

**Preconditions:** `session` matches exactly one session.

**Effects:** none.

**Output schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "show-output",
  "type": "object",
  "required": ["session"],
  "properties": {
    "session": { "$ref": "session-view" },
    "statusline_payload": { "type": ["object", "null"], "description": "Present only with include_payload; null without a usable statusline.json." }
  },
  "additionalProperties": false
}
```

**Errors,** in this order:

| Kind | When |
|---|---|
| `invalid-input` | `session` is missing or not a [selector](#selecting-a-session). |
| `environment` | `HOME` is unusable. |
| `not-found` | `session` matches no session. |
| `ambiguous` | `session` matches several. |

**Warnings:**

| Kind | When |
|---|---|
| `unusable-file` | A session file is unusable: one of the selected session's, or the `lifecycle.json` or `sesshin.json` of a session that might have matched. |

**Retry safety:**

- After anything: safe. It changes nothing.

### version

Report this binary's version and the file formats it supports.

**Kind:** read. Takes no lock, and needs no state directory or config.

**Input schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "version-input",
  "type": "object",
  "additionalProperties": false
}
```

**Additional validation:** none.

**Preconditions:** none.

**Effects:** none.

**Output schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "version-output",
  "type": "object",
  "required": ["version", "commit", "modified", "go", "formats"],
  "properties": {
    "version": { "type": "string", "description": "The main module's version as Go stamped it: a tag, a pseudo-version, either with +dirty, or (devel)." },
    "commit": { "type": ["string", "null"], "description": "vcs.revision; null when the build has no VCS information." },
    "modified": { "type": "boolean", "description": "vcs.modified: built with uncommitted changes. false when commit is null." },
    "go": { "type": "string" },
    "formats": {
      "type": "object",
      "required": ["state", "lifecycle", "statusline", "sesshin", "reservation", "install"],
      "additionalProperties": { "type": "integer" }
    }
  },
  "additionalProperties": false
}
```

**Errors:** none.

**Warnings:** none.

**Retry safety:**

- After anything: safe. It changes nothing.

## Setup operations

### install

Propose wiring sesshin into Claude Code: a copy of Claude Code's `settings.json` ([Locations](design-spec.md#locations)) with sesshin's [hooks](hooks-spec.md#registration) and statusline registered, and its [permission rules](hooks-spec.md#registration) added, pointing at the `sesshin-hook` installed beside this `sesshin`, for you to review and apply. sesshin never writes `settings.json` itself, so it needs to know nothing about how you manage that file (a symlink into a dotfiles repository, say).

**Kind:** setup. Takes no sesshin lock. Writes `install.json`, then the proposal, each atomically. Never writes `settings.json`.

**Input schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "install-input",
  "type": "object",
  "properties": {
    "dry_run": { "type": "boolean", "default": false }
  },
  "additionalProperties": false
}
```

**Additional validation:** none.

**Preconditions:** Claude Code's `settings.json` is absent or a JSON object.

**Effects:**

1. **Check** `config.toml`, then [`hooks.properties`](design-spec.md#hook-settings), then `settings.json` against the preconditions (`corrupt`). Nothing has changed yet.
2. **Self-test.** Find `sesshin-hook` in the directory of this `sesshin`'s executable (`os.Executable`, symlinks resolved), and read its embedded Go build information without running it: its version, commit, and uncommitted-changes flag must equal this `sesshin`'s, and it must be identifiable, with a commit or a version other than `(devel)` ([Build information](implementation-spec.md#toolchain)). Then run its `session-start`, `stop`, and `statusline` verbs, each as a child process given a payload on stdin, with `HOME` set to a new temporary directory and `XDG_CONFIG_HOME`, `XDG_STATE_HOME`, `CLAUDE_CONFIG_DIR`, `CLAUDE_PID`, `CLAUDECODE`, `CLAUDE_CODE_ENTRYPOINT`, `KITTY_LISTEN_ON`, `KITTY_WINDOW_ID`, `TMUX`, and `STY` removed from its environment. A temporary `HOME` relocates the config and state directories on both platforms, and the rest keeps the caller's own session and window out of the test. It passes when every child exits 0 and writes nothing to stderr, the temporary state directory then holds a `lifecycle.json`, a `sesshin.json` with `id` 1, and a `statusline.json`, each valid against its [schema](design-spec.md#file-schemas), and `statusline` printed a non-empty line with no trailing newline. A failure stops here, with nothing written: a proposal that wires a broken hook would break every session. The temporary directory is removed either way.
3. **With `dry_run`, stop here.** The output reports the `changes` a proposal would make; nothing is written, and `proposal_path` and `apply` are `null`.
4. **Record** what `uninstall` needs in `<state>/install.json`: `sesshin-hook`'s path and the version, the time, and the config and state directories and the `settings.json` it resolved.
5. **Propose.** Write `<state>/settings.proposed.json`: Claude Code's `settings.json` (an empty object when it doesn't exist) with each hook registered, with `sesshin-hook`'s absolute path from step 2, replacing any entry [sesshin owns](#sesshins-entries-in-settingsjson), removing sesshin's entries under events it no longer registers and every duplicate of a registered command (the first in `settings.json` order is kept), and leaving every other entry, of any tool, exactly as it was; and with `statusLine` set to `sesshin-hook`'s absolute path followed by `statusline`, replacing a `statusLine` that isn't sesshin's with a [`status-line-replaced`](#warning-kinds) warning; and with each of sesshin's permission rules appended to `permissions.allow` or `permissions.ask` where it is missing. The proposal is built as [Registration](hooks-spec.md#registration) says.

**Applying it is yours.** The output's `apply` holds two commands: `diff -uN` of `settings.json` against the proposal, to review it, and `cat` of the proposal redirected onto `settings.json`, which writes through a symlink and keeps the file's mode. Run `install` with `dry_run` to check the result, which writes nothing: when every item of `changes` is `unchanged`, `settings.json` wires sesshin. Running it again after an upgrade or a move proposes the re-wiring.

**Output schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "install-output",
  "type": "object",
  "required": ["dry_run", "hook_binary", "settings_path", "proposal_path", "apply", "changes"],
  "properties": {
    "dry_run": { "type": "boolean" },
    "hook_binary": { "type": "string", "description": "The sesshin-hook every hook runs once the proposal is applied." },
    "settings_path": { "type": "string" },
    "proposal_path": { "type": ["string", "null"], "description": "The proposed settings.json; null with dry_run." },
    "apply": { "type": ["array", "null"], "items": { "type": "string" }, "description": "Shell commands, paths single-quoted: diff -uN to review the proposal, then cat to apply it. null with dry_run." },
    "changes": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["what", "action"],
        "properties": {
          "what": { "type": "string", "description": "hooks.<Event>:<verb>, statusLine, or permissions.<allow|ask>:<rule>" },
          "action": { "enum": ["added", "replaced", "unchanged", "removed"], "description": "A permission rule is only ever added or unchanged." }
        },
        "additionalProperties": false
      }
    }
  },
  "additionalProperties": false
}
```

**Order:** `changes` has one item per registered command, in [Registration](hooks-spec.md#registration)'s order, then one `removed` item per sesshin entry the proposal drops (under an event sesshin no longer registers, or a duplicate), in `settings.json` order, then `statusLine`, then one item per permission rule, in [Registration](hooks-spec.md#registration)'s order (`permissions.allow:Bash(sesshin:*)`, say). `what` is `hooks.<Event>:<verb>` (e.g. `hooks.UserPromptSubmit:terminal-sync`), since one event can have two of sesshin's commands. So a `settings.json` wires sesshin exactly when every item is `unchanged`: a stale or duplicate entry is an item that isn't.

**Errors,** in this order:

| Kind | When |
|---|---|
| `invalid-input` | A bad field. |
| `environment` | `HOME` is unusable. |
| `corrupt` | `config.toml` or `hooks.properties` is [corrupt](design-spec.md#configuration): `prune` would fail on the first, and the hooks would silently ignore the second, so `install` is where you find out. Then `settings.json` isn't a JSON object, or has a sesshin-relevant key of the wrong shape ([Registration](hooks-spec.md#registration)). |
| `self-test-failed` | `sesshin-hook` is missing beside `sesshin`, from another build, or unidentifiable, or the self-test failed. |

**Warnings:**

| Kind | When |
|---|---|
| `status-line-replaced` | The proposal replaces a `statusLine` sesshin didn't install. |

**Retry safety:**

- After anything: safe. It converges: it writes only its own two files, each atomically, and never `settings.json`.

### uninstall

Propose removing sesshin from Claude Code: sesshin's hook entries, its statusline, and its permission rules, for you to review and apply as [`install`](#install)'s proposal is.

**Kind:** setup. Takes no sesshin lock. Writes the proposal atomically. Never writes `settings.json`.

**Input schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "uninstall-input",
  "type": "object",
  "properties": { "dry_run": { "type": "boolean", "default": false } },
  "additionalProperties": false
}
```

**Additional validation:** none.

**Preconditions:** none.

**Effects:** writes `<state>/settings.proposed.json`: the `settings.json` that `install.json` records (else the one this process resolves), with every entry sesshin owns removed, `statusLine` removed while it is still [sesshin's](#sesshins-entries-in-settingsjson), and every copy of the four permission rules that name sesshin removed from the array [Registration](hooks-spec.md#registration) puts each in (`Bash(jq:*)` stays: it isn't sesshin's alone). A `statusLine` set since install is left alone and reported `unchanged`. Every other entry stays exactly as it is, including ones added since install; the proposal is built as [Registration](hooks-spec.md#registration) says, and applied as `install`'s is. Apart from the proposal, the state directory is left alone: what sesshin recorded is yours to delete. With `dry_run`, nothing is written, and the output reports what would change, with `proposal_path` and `apply` `null`.

**Output schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "uninstall-output",
  "type": "object",
  "required": ["dry_run", "settings_path", "proposal_path", "apply", "changes"],
  "properties": {
    "dry_run": { "type": "boolean" },
    "settings_path": { "type": "string" },
    "proposal_path": { "type": ["string", "null"], "description": "The proposed settings.json; null with dry_run." },
    "apply": { "type": ["array", "null"], "items": { "type": "string" }, "description": "As install's." },
    "changes": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["what", "action"],
        "properties": {
          "what": { "type": "string", "description": "hooks.<Event>:<verb>, statusLine, or permissions.<allow|ask>:<rule>" },
          "action": { "enum": ["removed", "unchanged"] }
        },
        "additionalProperties": false
      }
    }
  },
  "additionalProperties": false
}
```

**Order:** unlike [`install`](#install)'s: sesshin's entries in the order they appear in `settings.json`, then `statusLine` when there is one (`removed` while sesshin's, else `unchanged`), then one `removed` item per permission rule removed, `allow` before `ask`, each in Registration's order (copies of one rule are one item); empty when `settings.json` holds no sesshin entry, no sesshin rule, and no `statusLine`.

**Errors,** in this order:

| Kind | When |
|---|---|
| `invalid-input` | A bad field. |
| `environment` | `HOME` is unusable. |
| `corrupt` | `settings.json` isn't a JSON object, or has a sesshin-relevant key of the wrong shape ([Registration](hooks-spec.md#registration)). |

**Warnings:** none.

**Retry safety:**

- After anything: safe. It writes only the proposal, atomically, and never `settings.json`.


## Write operations

### spawn

Launch `claude` in a new tab, split, or OS window of the caller's terminal, optionally under a job name reserved first, and optionally wait for it to start.

**Kind:** write. With a job, waits briefly for the state lock to claim it, and again after the launch to record the window ([Locks](design-spec.md#locks)); holds no lock across the launch or the wait.

**Input schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "spawn-input",
  "type": "object",
  "required": ["cwd"],
  "properties": {
    "job": { "$ref": "defs#/$defs/job", "description": "Job name to reserve; none when absent." },
    "cwd": { "type": "string", "description": "Absolute directory to start in." },
    "type": { "enum": ["tab", "split", "os-window"], "default": "tab", "description": "Where to open it: a new tab in the caller's OS window, a split of the caller's tab, or a new OS window. Each backend maps these to its own terms (kitty: tab, window, os-window)." },
    "name": { "type": "string", "minLength": 1, "description": "The session's name: passed to claude as --name, and the tab title. Default: the job; with neither, claude's own name and the backend's own title." },
    "prompt": { "type": "string", "description": "First prompt, passed to claude as its last argument, after --." },
    "args": { "type": "array", "items": { "type": "string" }, "default": [], "description": "Extra claude arguments, passed in order before the prompt." },
    "vars": { "type": "object", "additionalProperties": { "type": "string" }, "default": {}, "description": "The new window's user variables (kitty's --var): for matching windows, not environment variables." },
    "extra": { "type": "object", "description": "The session's user-owned extra (design-spec User-owned extra), passed as SESSHIN_EXTRA. None when absent: the session starts with {}." },
    "start_timeout_secs": { "type": "integer", "minimum": 0, "maximum": 120, "default": 15, "description": "How long to wait for the session to start. 0 returns as soon as the window is open." }
  },
  "additionalProperties": false
}
```

**Additional validation:** `cwd` is absolute. No string in `cwd`, `name`, `prompt`, `args`, or `vars` holds a NUL, which no argument vector can carry. Each `vars` key matches `^[A-Za-z_][A-Za-z0-9_]{0,63}$`. `extra` is within `extra`'s [limits](design-spec.md#user-owned-extra), measured as the compact JSON `SESSHIN_EXTRA` will hold, and holds no NUL, which no environment can carry. Its numbers are exempt from the integer-literal rule, and kept as given.

**Preconditions:** `cwd` is an existing directory. The caller runs in a terminal a backend recognizes, outside tmux and screen: for kitty, `KITTY_LISTEN_ON` and `KITTY_WINDOW_ID` are set ([Placement](design-spec.md#placement)). With a `job`: no live session holds it, and no fresh reservation names it, comparing [keys](design-spec.md#reservations), so a held `API` refuses `api`.

**Effects:**

1. **Check the job's reservation's window,** with a `job` and no lock held: when the job's reservation, `reservations/<key>.json` ([key](design-spec.md#reservations): the job lowercased), is a launched reservation not stale by age, ask the backend whether its window exists, as [`prune`](#prune) does.
2. **Claim,** with a `job`: wait up to 500 ms for the state lock (else `busy`). Under it, read every session as [`list`](#list) does, and fail `conflict` (`rule`: `job-taken`) when a live session (liveness `live` or `unknown`) reports a job with the same key. Read `reservations/<key>.json` again: fail `job-taken` when it is fresh, judging its window by step 1's answer only if it still holds the `token` and `placement` asked about. Otherwise (none, stale, or unusable) create `reservations/<key>.json` (`{schema, job, token, created_at, placement}`, `job` as given) with a new random `token`, `created_at` now, and `placement` `null`, replacing a stale or unusable file, and release the lock. `reservations/` is created if missing.
3. **Launch** through the backend, as [Launching `claude`](#launching-claude) says: in `cwd`, named and titled `name` (else the job), with `vars` set, with `SESSHIN_JOB=<job>` and `SESSHIN_TOKEN=<token>` in its environment (with no job, both are removed), and with `SESSHIN_EXTRA` set to `extra` as compact JSON, its key order and numbers as given, when `extra` is present. The backend's launch has a 10-second limit.
4. **On failure:**
   - The backend refused (`kitten` missing, a socket that refuses, a nonzero exit): nothing was opened. Under the state lock, waited for as in step 5, remove the reservation if it still holds this `token`, so the job is free at once, and fail `terminal` (`reason`: `launch-failed`).
   - The limit passed, or the backend answered without a window it could name: a window may have opened. Keep the reservation, which a session that starts adopts, and which goes stale 120 seconds after `created_at` if none does. Fail `terminal` (`reason`: `launch-unknown`).
5. **Record the window,** with a `job`: wait up to 2 seconds for the state lock. Under it, if `reservations/<key>.json` still holds this `token`, rewrite it with the launched window as its `placement`, everything else unchanged, so it stays fresh while the window waits at Claude's workspace-trust dialog. If it is gone (the session has already adopted it) or holds another `token` (removed, and the job claimed again), leave it. If the lock isn't taken in time, or the rewrite fails, warn `placement-not-recorded` and go on: the reservation goes stale 120 seconds after `created_at` unless the session adopts it first.
6. **Wait** up to `start_timeout_secs` for the session to start, reading every 100 ms with no lock: with a `job`, until a live session reports it; without one, until a session's placement names the launched window (the backend's same-window test, as in [Placement](design-spec.md#placement)'s Replaced with care). A session waiting at Claude's workspace-trust dialog starts only once you accept it, which may take longer than any timeout.

**Output schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "spawn-output",
  "type": "object",
  "required": ["job", "placement", "session"],
  "properties": {
    "job": { "oneOf": [{ "$ref": "defs#/$defs/job" }, { "type": "null" }] },
    "placement": { "$ref": "defs#/$defs/placement", "description": "The launched window, as the backend reports it." },
    "session": { "oneOf": [{ "$ref": "session-view" }, { "type": "null" }], "description": "The started session; null when start_timeout_secs was 0, or it didn't start in time (with a not-started warning)." }
  },
  "additionalProperties": false
}
```

**Errors,** in this order:

| Kind | When |
|---|---|
| `invalid-input` | A bad `job`, `cwd`, `type`, `name`, `prompt`, `args`, `vars`, `extra`, or `start_timeout_secs`. |
| `environment` | `HOME` is unusable. |
| `corrupt` | `config.toml` is corrupt. |
| `not-found` | `cwd` doesn't exist or isn't a directory (`paths`). |
| `terminal` | (`reason`: `unavailable`) No backend recognizes the caller's terminal, or it runs under tmux or screen. |
| `busy` | (`lock`: `state`) The state lock was held for 500 ms. |
| `conflict` | (`rule`: `job-taken`) A live session or a fresh reservation holds `job`, or a job differing from it only in case, which the message names as stored. `sessions` names the session, empty for a reservation. |
| `terminal` | (`reason`: `launch-failed`) The backend refused the launch; the reservation was removed. (`reason`: `launch-unknown`) The launch timed out or its answer named no window; the reservation was kept. |

**Warnings:**

| Kind | When |
|---|---|
| `not-started` | The session didn't start within `start_timeout_secs`. It may still start: `list` shows it once it has. |
| `placement-not-recorded` | The launched window couldn't be recorded in the reservation (step 5). |
| `unusable-file` | A session file read while checking the job, or while waiting, couldn't be used, as [`list`](#list) reports it. |

**Retry safety:**

- After `invalid-input`, `environment`, `corrupt`, `not-found`, `terminal` (`unavailable` or `launch-failed`), `busy`, or `conflict`: safe. Nothing was launched, and no reservation remains.
- After `terminal` (`launch-unknown`), or success with `not-started`: **not** safe. A session may still be starting. A retry with the same job fails `job-taken` while the reservation is fresh; with no job, it opens a second window. Check `sesshin list` first.
- After a crash (exit 3 or a signal): not safe, for the same reason. A reservation left behind goes stale 120 seconds after `created_at` if it was never launched, and is removed by the next `spawn` of its job or `prune`.

### resume

Reopen an ended session: launch `claude --resume <uuid>` in a new tab of the caller's terminal, in the session's last `cwd`, under its tab title and its job (or another job, named by `job`), and wait for it to be live again.

**Kind:** write. Takes the state lock as [`spawn`](#spawn) does, with a job ([Locks](design-spec.md#locks)); holds no lock across the launch or the wait.

**Input schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "resume-input",
  "type": "object",
  "required": ["session"],
  "properties": {
    "session": { "$ref": "selector", "description": "Among ended sessions." },
    "job": { "$ref": "defs#/$defs/job", "description": "Resume under this job instead of the session's stored one, for when that one is taken. Absent: the stored job, if any." },
    "args": { "type": "array", "items": { "type": "string" }, "default": [], "description": "Extra claude arguments, passed in order after --resume <uuid>." },
    "start_timeout_secs": { "type": "integer", "minimum": 0, "maximum": 120, "default": 15, "description": "How long to wait for the session to be live again. 0 returns as soon as the tab is open." }
  },
  "additionalProperties": false
}
```

**Additional validation:** no string in `args` holds a NUL.

**Preconditions:** `session` selects one ended session ([Selecting a session](#selecting-a-session)): a sesshin ID or UUID of a session that is live, or whose liveness is `unknown`, is refused, not resumed twice. Its `cwd` is recorded and is an existing directory. The caller runs in a terminal a backend recognizes, outside tmux and screen, as for `spawn`. If it resumes under a job (`job`, else the session's stored job, as an ended session reports it): no live session holds it, and no fresh reservation names it.

**Effects:**

1. **Select** the session, reading every session as [`list`](#list) does, and check it is ended. A headless session can be resumed like any other.
2. **Check the job's reservation's window, then claim,** if it resumes under a job: as `spawn`'s steps 1 and 2, with the job.
3. **Launch** a tab through the backend, as [Launching `claude`](#launching-claude) says, running `claude --resume <uuid> <args…>`: in the session's `cwd`, titled with its placement's stored `tab_title`, else its [name](design-spec.md#terms); with its stored `user_vars` as the window's user variables when its placement is the caller's backend's (kitty's) and has them; and with `SESSHIN_JOB=<job>` and `SESSHIN_TOKEN=<token>` in its environment when it resumes under a job. The backend's launch has a 10-second limit.
4. **On failure,** and **record the window:** as `spawn`'s steps 4 and 5.
5. **Wait** up to `start_timeout_secs` for the session to be live again (liveness `live` or `unknown`, as `spawn` counts it), reading only its own directory every 100 ms, with no lock. A transcript `claude` can't find makes it start a new session instead, or exit: this one then never comes back, and the wait runs out.

`resume` writes no session file, and passes no `SESSHIN_EXTRA`: the session keeps the `extra` in its `sesshin.json` ([User-owned extra](design-spec.md#user-owned-extra)). The resumed session's own [`session-start`](hooks-spec.md#session-start) revives its directory (new `pid`, `ended_at` cleared, `event_seq` continuing) and, matching the `SESSHIN_TOKEN` it was launched with, [adopts](design-spec.md#reservations) the reservation, which sets its job when `job` named another.

A session whose job a live session or a fresh reservation now holds is refused (`job-taken`), not silently renamed or resumed without one: the resumed session would take the job from its environment, and the reported jobs would settle which keeps it only at read time. To resume it anyway, name another job with `job`. Its stored job changes to that one once it starts; a launch that fails leaves it as it was. There is no way to resume a session with a job stored without one.

**Output schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "resume-output",
  "type": "object",
  "required": ["job", "placement", "session"],
  "properties": {
    "job": { "oneOf": [{ "$ref": "defs#/$defs/job" }, { "type": "null" }], "description": "The job it was resumed under, or null." },
    "placement": { "$ref": "defs#/$defs/placement", "description": "The launched window, as the backend reports it." },
    "session": { "oneOf": [{ "$ref": "session-view" }, { "type": "null" }], "description": "The session, live again; null when start_timeout_secs was 0, or it wasn't live in time (with a not-started warning)." }
  },
  "additionalProperties": false
}
```

**Errors,** in this order:

| Kind | When |
|---|---|
| `invalid-input` | A bad `session`, `job`, `args`, or `start_timeout_secs`. |
| `environment` | `HOME` is unusable. |
| `corrupt` | `config.toml` is corrupt. |
| `not-found` | (`sessions`) `session` selects no session; for a job, no ended one. |
| `ambiguous` | `session` selects several sessions. |
| `conflict` | (`rule`: `live`) `session` selects a live session, or one whose liveness is `unknown`. `sessions` names it. |
| `not-found` | (`paths`) The session's `cwd` is gone or isn't a directory (the `cwd`), or was never recorded (empty). |
| `terminal` | (`reason`: `unavailable`) As for `spawn`. |
| `busy` | (`lock`: `state`) It resumes under a job, and the state lock was held for 500 ms. |
| `conflict` | (`rule`: `job-taken`) A live session or a fresh reservation holds the job, or one differing from it only in case, which the message names as stored. `sessions` names the session, empty for a reservation; the message suggests `job`. |
| `terminal` | (`reason`: `launch-failed`, `launch-unknown`) As for `spawn`. |

**Warnings:**

| Kind | When |
|---|---|
| `transcript-missing` | The session's transcript isn't where it was recorded (`transcript_exists` `false`), so `claude --resume` will likely fail in the new tab. Launched anyway: the transcript may be somewhere sesshin can't see. |
| `not-started` | The session wasn't live again within `start_timeout_secs`. It may still start. |
| `placement-not-recorded` | As for `spawn`. |
| `unusable-file` | A session file read while selecting, checking the job, or waiting couldn't be used, as [`list`](#list) reports it. |

**Retry safety:**

- After any error but `terminal` (`launch-unknown`): safe. Nothing was launched, and no reservation remains.
- After `terminal` (`launch-unknown`), success with `not-started`, or a crash: **not** safe. A tab may be resuming the session, and a second would resume it twice: with a job, a retry fails `job-taken` while the reservation is fresh; without one, nothing stops it. Check `sesshin list` first.

### send

Type text into a live session's window, as one paste, and by default press Enter: the one operation that acts on a running session rather than launching or observing one.

**Kind:** write, on the terminal only. Takes no lock and writes no file: it finds the session's window afresh on every call, so a stored window that has gone stale costs nothing, and nothing is repaired.

**Input schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "send-input",
  "type": "object",
  "required": ["session", "text"],
  "properties": {
    "session": { "$ref": "selector", "description": "Among live sessions." },
    "text": { "type": "string", "minLength": 1, "description": "At most 1 MiB of UTF-8. Delivered as one paste: line breaks stay in the input box, and several lines arrive as one prompt." },
    "submit": { "type": "boolean", "default": true, "description": "Press Enter after the text." },
    "force": { "type": "boolean", "default": false, "description": "Send even when the session's turn hasn't ended, including at a permission prompt or another dialog, which the text may answer." }
  },
  "additionalProperties": false
}
```

**Additional validation:** `text` is at most 1 MiB (1048576 bytes) of UTF-8, and holds no control character but tab, line feed, and carriage return: no ESC, no other C0 control (U+0000–U+001F), no DEL (U+007F), and no C1 control (U+0080–U+009F). sesshin wraps the text in the bracketed-paste markers itself (see Effects), so an ESC in it could end the paste early, and everything after would be typed as keys ([verified](design-spec.md#claude-code-21289)).

**Preconditions:** the session is live, or its liveness is `unknown`. Its turn has ended (status `waiting` or `idle`), unless `force`: mid-turn, a dialog may have the keyboard, and text plus Enter could answer it, while the status can't tell (a permission prompt is reported only after some seconds, and an `AskUserQuestion` may not be reported at all). Once the turn has ended, no tool dialog can be up. An unknown status counts as mid-turn. A turn interrupted with Esc or Ctrl-C sends no hook, so the session reads `working` (or `needs_approval`) until its next event, and only `force` reaches it ([Status](design-spec.md#status)). It has a kitty placement, and a pid.

**Effects:**

1. **Select** the session, reading every session as [`list`](#list) does ([Selecting a session](#selecting-a-session)): a job selects the live session holding it.
2. **Check** it: refuse an ended session, a turn not ended (without `force`), a session with no placement or a placement of a terminal sesshin has no backend for, and a session whose pid is unknown, since its window can't be verified.
3. **Find its window,** never trusting the stored one: ask `kitten @ --to <socket> ls`, with the placement's `socket`, for the window whose foreground processes include the session's pid. When that socket doesn't answer within 5 seconds, or has no such window, and the caller's own `KITTY_LISTEN_ON` names another socket, ask that one the same way. With no window found, fail `terminal` (`unreachable`) and type nothing: the stored `window_id` may now hold a shell. A window running `claude` lists it as its one foreground process, also while it runs a tool's command ([verified](design-spec.md#kitty-0491)).
4. **Paste** the text as one bracketed paste: `kitten @ --to <socket> send-text --match id:<window> --bracketed-paste=disable --stdin`, with `ESC[200~`, the text, and `ESC[201~` on stdin. sesshin adds the markers itself because kitty's own (`--bracketed-paste`) wraps each 2048-byte chunk of a longer text as a paste of its own, which Claude Code then shows and submits as separate pastes, with line breaks between them ([verified](design-spec.md#kitty-0491)).
5. **Submit,** if `submit`: a second call, `send-text --match id:<window> '\r'`. Enter sent at once after the paste submits it whole ([verified](design-spec.md#claude-code-21289)).

Each `kitten` call has a 5-second limit.

**What Claude Code does with a paste** ([verified](design-spec.md#claude-code-21289)), which `send` doesn't control: a paste of several lines is shown as `[Pasted text #1]`, and reaches the model wrapped in `<pasted_content>` tags after two blank lines; one line arrives as it is. Tabs become four spaces, and a trailing line break is dropped.

**Output schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "send-output",
  "type": "object",
  "required": ["session", "placement", "submitted", "status", "permission_mode", "event_seq"],
  "properties": {
    "session": { "$ref": "session-ref" },
    "placement": { "$ref": "defs#/$defs/placement", "description": "The window the text was sent to, as found in step 3." },
    "submitted": { "type": "boolean", "description": "Whether Enter was pressed." },
    "status": { "type": "string", "description": "The session's status when the text was sent." },
    "permission_mode": { "type": ["string", "null"], "description": "So a caller sees when it just prompted a session that won't ask before acting." },
    "event_seq": { "type": "integer", "minimum": 1, "description": "The session's event_seq when the text was sent: a turn the text starts is recorded after it." }
  },
  "additionalProperties": false
}
```

**Errors,** in this order:

| Kind | When |
|---|---|
| `invalid-input` | A bad `session`, `text`, `submit`, or `force`. |
| `environment` | `HOME` is unusable. |
| `not-found` | (`sessions`) `session` selects no live session. |
| `ambiguous` | `session` selects several sessions. |
| `conflict` | (`rule`: `not-live`) `session` selects an ended session by sesshin ID or UUID. (`mid-turn`) Its turn hasn't ended, and no `force`. (`no-placement`) It has no placement, or one sesshin has no backend for. `sessions` names it. |
| `terminal` | (`reason`: `unreachable`) Its pid is unknown, or no window with it was found. Nothing was typed. |
| `terminal` | (`reason`: `send-failed`) The paste failed: some of the text may be in the input box. (`submit-failed`) The text was pasted, but Enter failed: it waits in the input box. |

**Warnings:**

| Kind | When |
|---|---|
| `unusable-file` | A session file read while selecting couldn't be used, as [`list`](#list) reports it. |

**Retry safety:**

- After any error but `terminal` (`send-failed` or `submit-failed`): safe. Nothing was typed.
- After `terminal` (`send-failed` or `submit-failed`), a crash, or an unclear outcome: **not** safe. The text may be in the input box, or submitted, and a retry types it again. Look at the session's window, or its `event_seq`, first.

### update

Change a session's user-owned [`extra`](design-spec.md#user-owned-extra), live or ended: replace it, or set and remove keys. The one operation that writes a session file.

**Kind:** write. Takes the session's lock, waiting up to 500 ms ([Locks](design-spec.md#locks)); never the state lock.

**Input schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "update-input",
  "type": "object",
  "required": ["session", "extra"],
  "properties": {
    "session": { "$ref": "selector", "description": "Among all sessions: live, ended, and headless." },
    "extra": {
      "oneOf": [
        {
          "type": "object",
          "required": ["replace_all"],
          "properties": { "replace_all": { "type": "object", "description": "The complete new extra; {} clears it." } },
          "additionalProperties": false
        },
        {
          "type": "object",
          "minProperties": 1,
          "properties": {
            "merge": { "type": "object", "minProperties": 1, "description": "Keys to set; each replaces that key's whole value (shallow merge)." },
            "remove": { "type": "array", "minItems": 1, "uniqueItems": true, "items": { "type": "string" }, "description": "Keys to delete." }
          },
          "additionalProperties": false
        }
      ]
    }
  },
  "additionalProperties": false
}
```

`extra` is an object rather than the field itself so that `update` can take more fields later, as koan's does; it is the only one now.

**Additional validation:** `extra.merge` and `extra.remove` share no key. `replace_all` and `merge` are each within `extra`'s [limits](design-spec.md#user-owned-extra), and their numbers are exempt from the integer-literal rule, kept as given. With these rules, the order in which `merge` and `remove` apply doesn't matter.

**Preconditions:** `session` selects one session ([Selecting a session](#selecting-a-session)), whatever its liveness. It has a usable `sesshin.json`: one whose `id` is still `null` counts.

**Effects:**

1. **Select** the session, reading every session as [`list`](#list) does. A session without a usable `sesshin.json` has no sesshin ID and no job, so only its UUID or a prefix selects it.
2. **Lock** its directory, waiting up to 500 ms (else `busy`, `lock`: `session`). Once locked, check that the path still names the directory that was locked, as a hook does ([Recording an event](hooks-spec.md#recording-an-event)): if a [`prune`](#prune) renamed it aside meanwhile, or it is gone, fail `not-found`.
3. **Read** `sesshin.json` again, under the lock: this read, not step 1's, decides.
   - **There, but not readable** (a permission denied, an I/O error, a directory in its place): fail `io`.
   - **Missing, unusable, or in another format:** fail `conflict` (`rule`: `no-sesshin-file`, `file`: `missing` or `unusable`). `update` never creates `sesshin.json`: creating it issues a sesshin ID and decides the job, which only a hook does ([Creating `sesshin.json`](hooks-spec.md#creating-sesshinjson)).
4. **Change `extra`:**
   - `replace_all`: it becomes exactly the given object.
   - `merge`: each given key is set to the given value, replacing any earlier value whole (no recursive merge into objects); `null` is an ordinary value, not a deletion.
   - `remove`: each given key is deleted; a key that isn't there changes nothing.

   Existing keys keep their position, and new keys are appended in the order given ([File format](design-spec.md#file-format)). A result past `extra`'s limits fails `conflict` (`rule`: `extra-too-large`), and nothing is written.
5. **Write** `sesshin.json` (temp file, rename), every key but `extra` as step 3 read it, unless `extra` is unchanged: equal *as a JSON value* to what was stored (objects regardless of key order, numbers by numeric value). Then the file isn't rewritten, and its layout is untouched.
6. **Unlock,** then read the session, with no lock, for the output.

`update` changes `extra` only. `id`, `job`, `source`, and `placement` belong to the hooks ([`sesshin.json`](design-spec.md#sesshinjson)); `lifecycle.json`'s clocks don't move, since nothing happened in the session.

**Output schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "update-output",
  "type": "object",
  "required": ["session", "changed"],
  "properties": {
    "session": { "$ref": "session-view", "description": "The session after the update, read after the lock was released." },
    "changed": {
      "type": "array",
      "uniqueItems": true,
      "items": { "enum": ["extra"] },
      "description": "The fields whose value changed, compared as JSON values (see Effects); empty if none did, in which case the file was not rewritten."
    }
  },
  "additionalProperties": false
}
```

**Errors,** in this order:

| Kind | When |
|---|---|
| `invalid-input` | A bad `session` or `extra`: `replace_all` with another form, `merge` and `remove` sharing a key, an empty `extra`, `merge`, or `remove`, or a value past `extra`'s limits. |
| `environment` | `HOME` is unusable. |
| `not-found` | (`sessions`) `session` selects no session, or its directory was pruned while `update` waited for its lock. |
| `ambiguous` | `session` selects several sessions. |
| `busy` | (`lock`: `session`) A hook held the session's lock for 500 ms. |
| `io` | `sesshin.json` is there but can't be read (`path`, `code`). |
| `conflict` | (`rule`: `no-sesshin-file`) The session has no usable `sesshin.json`: `file` is `missing` or `unusable`, `path` names it, and `sessions` names the session. The message says why and what to do, by `file` and the session's liveness (below). |
| `conflict` | (`rule`: `extra-too-large`) The result would break `extra`'s limits. `sessions` names the session. |

**No `sesshin.json`.** sesshin writes `sesshin.json` at a session's first hook, so this is rare, and each case has its own way out. The message names it; `file` and the session's `liveness` let a script decide without parsing the message:

| `file` | Liveness | Why | What to do |
|---|---|---|---|
| `missing` | `live` or `unknown` | Its first hook hasn't written the file yet, e.g. while it waits at the workspace-trust dialog, or that hook couldn't. | Retry after its next prompt: that hook writes it. |
| `missing` | `ended` | It ended before any hook wrote the file, or the file was removed by hand. No hook of an ended session will write it. | `sesshin resume <uuid>`: its `session-start` writes the file. Then retry. |
| `unusable` | `live` or `unknown` | The file is corrupt, or from another sesshin build ([Format versions](design-spec.md#format-versions)). | Retry after its next prompt: that hook writes it afresh, with a new sesshin ID and `extra` from `SESSHIN_EXTRA`. |
| `unusable` | `ended` | As above, and no hook of an ended session will rewrite it. | `sesshin resume <uuid>`, then retry. |

For example: `session 0b6c5a3e has no sesshin.json yet (it is live, and its first hook hasn't written one): retry after its next prompt`.

**Warnings:**

| Kind | When |
|---|---|
| `unusable-file` | A session file read while selecting, or for the output, couldn't be used, as [`list`](#list) reports it. |

**Retry safety:** safe. Every form is idempotent: run again with the same input, it leaves `extra` as it is and returns `changed: []`. After `busy`, retry at once. After `conflict` (`no-sesshin-file`), retry as the table above says. A crash leaves `sesshin.json` old or new, never part of either.

### prune

Remove ended sessions last seen longer ago than the [retention](design-spec.md#retention) window, and stale or unusable [reservations](design-spec.md#reservations). sesshin never runs it on its own: you run it, by hand or on a schedule of your own.

**Kind:** write. Tries each candidate's session lock without waiting, then, with no session lock held, tries the state lock once to remove reservations.

**Input schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "prune-input",
  "type": "object",
  "properties": {
    "dry_run": { "type": "boolean", "default": false },
    "retain_days": { "type": "integer", "minimum": 1, "maximum": 9223372036854775807, "description": "This run only; default: the config's retain_days. Given explicitly, it prunes even when the config's is 0 (never)." }
  },
  "additionalProperties": false
}
```

**Additional validation:** none.

**Preconditions:** none. With no explicit `retain_days` and the config's at 0, only headless sessions can be prunable (by `retain_headless_hours`), and none when that is 0 too.

**Effects:** for each [prunable](design-spec.md#retention) session, `prune` tries its session lock without waiting, skips it if held, and judges it again under the lock; a session still prunable is renamed to a hidden name in `sessions/`, then removed. Live sessions, and sessions whose liveness can't be judged, are never removed.

Then reservations: each visible regular file in `reservations/` named `<key>.json` with a valid [key](design-spec.md#reservations) (a job name with no capitals) is read; other entries are ignored. For each launched reservation not already stale by age, the backend is asked whether its window exists, with no lock held. Then `prune` tries the state lock once; if it is held, no reservation is judged further, and `reservations_skipped_locked` is `true`. Under the lock, each reservation is read again, and removed when it is unusable, or stale by age, or its window was found gone and it still holds the `token` and `placement` it was asked about. Its `reason` is the first that applies of `unusable`, `expired`, `stranded`, and `window-gone`. A missing `reservations/` is no reservations, and `prune` never creates it. A missing `sessions/` ends the run before reservations too: the state lock is `sessions/`, and nothing creates a reservation before it exists.

When the clock is [unusable](design-spec.md#retention), nothing is removed, sessions or reservations, and `cutoff` is `null`. `state.json` is never written. With `dry_run`, nothing changes and the output says what would: it goes through the same locks and stops before each removal.

**Output schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "prune-output",
  "type": "object",
  "required": ["dry_run", "cutoff", "headless_cutoff", "pruned", "kept_ended", "skipped_locked", "reservations_removed", "reservations_skipped_locked"],
  "properties": {
    "dry_run": { "type": "boolean" },
    "cutoff": { "type": ["string", "null"], "description": "Timestamp: ended sessions last seen before it are pruned. null when retention is off or the clock is unusable." },
    "headless_cutoff": { "type": ["string", "null"], "description": "The same for headless sessions; null when retain_headless_hours is 0 (they follow cutoff) or the clock is unusable." },
    "pruned": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["session_id", "id", "last_seen", "headless"],
        "properties": {
          "session_id": { "type": "string" },
          "id": { "type": ["integer", "null"], "description": "Its sesshin ID, when sesshin.json could be read." },
          "last_seen": { "type": "string" },
          "headless": { "type": "boolean" }
        },
        "additionalProperties": false
      }
    },
    "kept_ended": { "type": "integer", "description": "Ended sessions inside the window." },
    "skipped_locked": { "type": "integer", "description": "Prunable sessions skipped because their lock was held; the next run judges them again." },
    "reservations_removed": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["job", "created_at", "reason"],
        "properties": {
          "job": { "type": "string", "description": "The stored job, case kept; for an unusable reservation, the file name (its key)." },
          "created_at": { "type": ["string", "null"], "description": "null for an unusable reservation." },
          "reason": { "enum": ["stranded", "window-gone", "expired", "unusable"], "description": "stranded: no placement 120 seconds after created_at; window-gone: the backend answered without its window; expired: more than a day old; unusable: design-spec Reservations." }
        },
        "additionalProperties": false
      }
    },
    "reservations_skipped_locked": { "type": "boolean", "description": "The state lock was held, so no reservation was removed; the next run judges them again." }
  },
  "additionalProperties": false
}
```

**Order:** `pruned` by last seen, oldest first; ties by `session_id`. `reservations_removed` by `job`.

**Errors,** in this order:

| Kind | When |
|---|---|
| `invalid-input` | A bad `retain_days`. |
| `environment` | `HOME` is unusable. |
| `corrupt` | `config.toml` is corrupt. |

An unusable clock is not an error: `prune` succeeds with nothing pruned and `cutoff` `null`, since guessing a cutoff is the one way retention could delete what it should keep.

**Warnings:**

| Kind | When |
|---|---|
| `unusable-file` | A session couldn't be judged (its `lifecycle.json` is unusable); it is kept. Or a reservation is unusable; it is removed (with `dry_run`, it would be). Raised as the reservation is judged under the state lock, so not when the lock was held. |


**Retry safety:**

- After anything, a crash included: safe. A crash between rename and removal leaves a hidden directory that reads ignore; cleaning those up is deferred with `repair`.
