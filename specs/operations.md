# sesshin operations

An operation is a single query of, or change to, what the [design spec](design-spec.md) defines. Operations are the domain layer, small and orthogonal. They are not CLI commands; [cli-spec.md](cli-spec.md) maps commands onto them.

The operations are the two that read the sessions sesshin recorded, [`list`](#list) and [`show`](#show), and [`version`](#version); the two that propose wiring sesshin into Claude Code, [`install`](#install) and [`uninstall`](#uninstall); [`spawn`](#spawn) and [`resume`](#resume), which launch sessions; [`send`](#send), which types into one; [`focus`](#focus), which brings one's window to the front; [`update`](#update), which changes a session's `extra`; and [`prune`](#prune), which cleans up after them; and [`migrate`](#migrate), which converts files to this binary's formats. The diagnostic operations (`doctor`, `repair`) and `info` are [deferred](deferred/operations.md), with the rules and kinds only they use, such as findings.

Hooks are not operations. They are sesshin's writers of what Claude Code reports, with their own contract, in [hooks-spec.md](hooks-spec.md).

Terms follow the design spec's [Terms](design-spec.md#terms).

## Conventions

- **JSON in, JSON out.** Input and output are JSON with published schemas, so an agent can build requests and parse results without scraping text. Every result is wrapped in the [output envelope](#output-envelope). Input schemas are closed: an unknown field is refused. Output schemas are open, since a release may add an output field ([Versioning](#versioning)): they list every field this release writes, and a caller validating with them accepts one it doesn't know.
- **Schema identifiers.** Shared schemas have short `$id`s (`envelope`, `error`, `warning`, `selector`, `session-ref`, `session-view`, `session-projection`). Each operation's are `<op>-input` and `<op>-output`. An operation's schema may refer to the design spec's `defs` (`defs#/$defs/job`).
- **Referring to operations and kinds.** Operation names, error kinds, and warning kinds are written in code (`install`, `corrupt`), linked on their first mention in a section. Error qualifiers are written `` `kind` (`field`: `value`) ``, e.g. `self-test-failed` (`hook`: `statusline`).
- **Parameters.** An operation takes a parameter only if it changes the meaning of the result or the work done.
- **Versioning.** The schemas in this document are sesshin's public contract (see [Versioning](#versioning)).

## Operation kinds

- ***read*** — Takes no lock and changes nothing: [`list`](#list), [`show`](#show), and [`version`](#version).
- ***setup*** — Proposes changes to Claude Code's configuration so sesshin's hooks run, for you to apply: [`install`](#install) and [`uninstall`](#uninstall). They read Claude Code's `settings.json` and never write it, write only their own files in the state directory, take no sesshin lock, and define their own error precedence.
- ***write*** — Changes the state directory, the terminal, or both: [`spawn`](#spawn) and [`resume`](#resume) open windows, [`send`](#send) types into one and [`focus`](#focus) brings one to the front, both writing no file, [`update`](#update) changes one session's `extra`, [`prune`](#prune) removes sessions, and [`migrate`](#migrate) converts files to this binary's formats. They hold the state lock only for a few file operations (and a read of every session), never across a launch or a wait. Who waits for which lock, and how long, is the table under [Locks](design-spec.md#locks); a wait that runs out is [`busy`](#error-kinds), except as that table says.

The deferred operations add the ***diagnostic*** kind back, and more write operations (see [deferred/operations.md](deferred/operations.md)).

## Operation template

Every operation is specified with the same parts, in this order. Every part is always present except **Order**, which appears only for operations that return a collection (`list`'s `sessions`, `install`'s and `uninstall`'s `changes`, `prune`'s `pruned`, `migrate`'s `changed`). An empty part is written `**Part:** none.`, optionally followed by one sentence saying why.

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
      }
    },
    {
      "type": "object",
      "required": ["ok", "error", "warnings"],
      "properties": {
        "ok": { "const": false },
        "error": { "$ref": "error" },
        "warnings": { "type": "array", "items": { "$ref": "warning" } }
      }
    }
  ]
}
```

## Errors

`kind` and `details` are the contract; `message` is for people and may change between releases. A caller treats an unknown kind as a generic failure. The values that qualify a kind in `details` (`conflict`'s `rule`, `terminal`'s `reason`, `no-sesshin-file`'s `file`, and `unusable-file`'s `reason` among the warnings) are [open sets](design-spec.md#open-sets): a caller treats one it doesn't know as the kind alone.

### Error kinds

| Kind | Meaning | `details` |
|---|---|---|
| `invalid-input` | Input failed validation. Raised before any lock is sought or any file is read. Reports every problem, not just the first. | `problems`: `{field, reason}` list, `field` a JSON Pointer into the input; sorted by `field`, then `reason`; at most 20, with `problems_truncated`, always present: `true` past that, else `false`. |
| `environment` | The process's environment lacks what sesshin needs to find its files: a usable `HOME` (see [Locations](design-spec.md#locations)). | `variable`: currently always `HOME`. |
| `not-found` | A session or path named by the input does not exist. | `selectors`: the [selectors](#selecting-a-session) that matched nothing, as given; `paths`: the paths, as given, that don't exist or aren't what the operation needs. Both always present, possibly empty. |
| `ambiguous` | A selector matched more than one session. | `selector`; `candidates`: the matching sessions as [session refs](#session-ref), in [session order](#session-order), at most 20, with `candidates_truncated`, always present: `true` past that, else `false`. |
| `conflict` | The operation was refused because of the state it found. | `rule`: `job-taken` (a live session or a fresh reservation holds the job), `live` (the session to [`resume`](#resume) is live, or its liveness is `unknown`), `not-live` (the session to [`send`](#send) to or [`focus`](#focus) has ended), `mid-turn` (its turn hasn't ended), `other-format` (a [`resume`](#resume) under a job, and the session's `sesshin.json` is in another format; also `path`), `no-placement` (sesshin doesn't know its window), and for [`update`](#update): `no-sesshin-file` (the session has no `sesshin.json` it can change; also `path`: the file, and `file`: `missing`, `unusable`, `other-format`, or `pending`) or `extra-too-large` (the result would break `extra`'s [limits](design-spec.md#user-owned-extra)). `sessions`: the sessions involved, as [session refs](#session-ref), possibly empty. |
| `busy` | Another process held a lock this write needs for longer than it waits. Safe to retry. | `lock`: `state`, or `session` ([`migrate`](#migrate), [`update`](#update)); `session_id`: for `session`, the session's UUID. |
| `terminal` | The terminal backend could not do what was asked. | `reason`: `unavailable` (no backend recognizes the caller's terminal; see [Placement](design-spec.md#placement)), `launch-failed` (the backend refused to open the window; nothing was opened), `launch-unknown` (the launch timed out, or its answer named no window; one may have opened), for [`send`](#send): `unreachable` (no window with the session's pid was found; nothing was typed), `send-failed` (the paste failed; some text may have been typed), `submit-failed` (the text was pasted, but Enter failed); and for [`focus`](#focus): `focus-failed` (the window couldn't be focused); and for any of them, `unsupported` (the backend lacks an ability the command needs, [Terminal backends](design-spec.md#terminal-backends); nothing was done). `terminal`: the backend's tag, or `null`; `detail`: human-readable. |
| `unsupported-format` | The state directory is newer than this binary: `state.json` is in a newer [format](design-spec.md#format-versions), or records a [migration](design-spec.md#migrations) step past this binary's latest. Use a newer binary. | `path`; `field`: `schema` or `migration`; `found`; `supported`: this binary's version of `state.json`, or its latest step. |
| `corrupt` | A file sesshin needs is present and readable, but its content is wrong: `config.toml`, `hooks.properties`, or Claude Code's `settings.json`. | `path`; `detail`: human-readable. |
| `io` | The environment refused an operation: permission denied, disk full, and the like. | `path`; `code`: the symbolic OS error, e.g. `EACCES`. |
| `self-test-failed` | [`install`](#install)'s self-test found a hook that doesn't work, so nothing was proposed: `sesshin-hook` is missing beside `sesshin`, is from another build or one that can't be identified, or a verb misbehaved. | `path`: the `sesshin-hook` tested; `hook`: the verb, or `null` when `sesshin-hook` itself is missing, from another build, or unidentifiable; `detail`: human-readable, what it did or wrote. |
| `internal` | A bug sesshin detects. | none (`{}`). |

`usage` is a CLI-only kind, raised for a malformed command line (see [cli-spec.md](cli-spec.md)), and `unavailable` and `cancelled` are the pickers' ([picker-spec.md](picker-spec.md)). The [error schema](#error-schema) gives every kind's `details`, these three's included. The deferred operations add more `conflict` rules and `terminal` reasons (see [deferred/operations.md](deferred/operations.md#errors)).

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
    "kind": { "type": "string", "description": "Open: a caller treats one it doesn't know as a generic failure." },
    "message": { "type": "string", "description": "Human-readable; not part of the contract." },
    "details": { "type": "object", "description": "Each kind's, below; any object for a kind this release doesn't know." }
  },
  "allOf": [
    { "if": { "properties": { "kind": { "const": "invalid-input" } } }, "then": { "properties": { "details": {
      "type": "object",
      "required": ["problems", "problems_truncated"],
      "properties": {
        "problems": { "type": "array", "maxItems": 20, "items": { "type": "object", "required": ["field", "reason"], "properties": { "field": { "type": "string", "description": "A JSON Pointer into the input." }, "reason": { "type": "string", "description": "Human-readable." } } } },
        "problems_truncated": { "type": "boolean", "description": "Whether past 20 problems left some out." }
      }
    } } } },
    { "if": { "properties": { "kind": { "const": "environment" } } }, "then": { "properties": { "details": { "type": "object", "required": ["variable"], "properties": { "variable": { "type": "string", "examples": ["HOME"], "description": "Open set." } } } } } },
    { "if": { "properties": { "kind": { "const": "not-found" } } }, "then": { "properties": { "details": { "type": "object", "required": ["selectors", "paths"], "properties": { "selectors": { "type": "array", "items": { "type": "string" } }, "paths": { "type": "array", "items": { "type": "string" } } } } } } },
    { "if": { "properties": { "kind": { "const": "ambiguous" } } }, "then": { "properties": { "details": {
      "type": "object",
      "required": ["selector", "candidates", "candidates_truncated"],
      "properties": {
        "selector": { "type": "string" },
        "candidates": { "type": "array", "maxItems": 20, "items": { "$ref": "session-ref" } },
        "candidates_truncated": { "type": "boolean", "description": "Whether past 20 candidates left some out." }
      }
    } } } },
    { "if": { "properties": { "kind": { "const": "conflict" } } }, "then": { "properties": { "details": {
      "type": "object",
      "required": ["rule", "sessions"],
      "properties": {
        "rule": { "type": "string", "examples": ["job-taken", "live", "not-live", "mid-turn", "other-format", "no-placement", "no-sesshin-file", "extra-too-large"], "description": "Open set." },
        "sessions": { "type": "array", "items": { "$ref": "session-ref" } },
        "path": { "type": "string", "description": "other-format and no-sesshin-file only: the session's sesshin.json." },
        "file": { "type": "string", "examples": ["missing", "unusable", "other-format", "pending"], "description": "no-sesshin-file only. Open set." }
      }
    } } } },
    { "if": { "properties": { "kind": { "const": "busy" } } }, "then": { "properties": { "details": { "type": "object", "required": ["lock"], "properties": { "lock": { "type": "string", "examples": ["state", "session"], "description": "Open set." }, "session_id": { "type": "string", "description": "For a session lock only." } } } } } },
    { "if": { "properties": { "kind": { "const": "terminal" } } }, "then": { "properties": { "details": {
      "type": "object",
      "required": ["reason", "terminal", "detail"],
      "properties": {
        "reason": { "type": "string", "examples": ["unavailable", "launch-failed", "launch-unknown", "unreachable", "send-failed", "submit-failed", "focus-failed", "unsupported"], "description": "Open set." },
        "terminal": { "type": ["string", "null"], "description": "The backend's tag." },
        "detail": { "type": "string", "description": "Human-readable." }
      }
    } } } },
    { "if": { "properties": { "kind": { "const": "unsupported-format" } } }, "then": { "properties": { "details": { "type": "object", "required": ["path", "field", "found", "supported"], "properties": { "path": { "type": "string" }, "field": { "type": "string", "examples": ["schema", "migration"], "description": "Open set." }, "found": { "type": "integer" }, "supported": { "type": "integer" } } } } } },
    { "if": { "properties": { "kind": { "const": "corrupt" } } }, "then": { "properties": { "details": { "type": "object", "required": ["path", "detail"], "properties": { "path": { "type": "string" }, "detail": { "type": "string", "description": "Human-readable." } } } } } },
    { "if": { "properties": { "kind": { "const": "io" } } }, "then": { "properties": { "details": { "type": "object", "required": ["path", "code"], "properties": { "path": { "type": "string" }, "code": { "type": "string", "description": "The symbolic OS error, e.g. EACCES." } } } } } },
    { "if": { "properties": { "kind": { "const": "self-test-failed" } } }, "then": { "properties": { "details": { "type": "object", "required": ["path", "hook", "detail"], "properties": { "path": { "type": "string" }, "hook": { "type": ["string", "null"] }, "detail": { "type": "string", "description": "Human-readable." } } } } } },
    { "if": { "properties": { "kind": { "const": "internal" } } }, "then": { "properties": { "details": { "type": "object", "properties": {} } } } },
    { "if": { "properties": { "kind": { "const": "usage" } } }, "then": { "properties": { "details": { "$ref": "usage-details" } } } },
    { "if": { "properties": { "kind": { "const": "unavailable" } } }, "then": { "properties": { "details": {
      "type": "object",
      "required": ["reason"],
      "description": "The pickers' (picker-spec.md, Errors).",
      "properties": {
        "reason": { "type": "string", "examples": ["no-terminal", "fzf-missing", "fzf-too-old", "fzf-failed"], "description": "Open set." },
        "found": { "type": "string", "description": "fzf-too-old only: the fzf version found." },
        "required": { "type": "string", "description": "fzf-too-old only: the oldest fzf that works." },
        "status": { "type": "integer", "description": "fzf-failed only, when fzf exited: its exit status." }
      }
    } } } },
    { "if": { "properties": { "kind": { "const": "cancelled" } } }, "then": { "properties": { "details": { "type": "object", "description": "The pickers' (picker-spec.md, Errors).", "properties": {} } } } }
  ]
}
```

## Warnings

A warning is a problem an operation worked around. It never changes the exit status.

### Warning kinds

| Kind | Meaning | `details` |
|---|---|---|
| `unusable-file` | A session file or a reservation could not be read, or is not one this binary can use. [`list`](#list) and [`show`](#show) leave the session out when it is its `lifecycle.json` (missing or unusable), and otherwise show it with what was readable; [`prune`](#prune) can't judge a session whose `lifecycle.json` it is, and keeps it, and removes an unusable reservation; [`migrate`](#migrate) reports a file it couldn't convert. The session's next hook replaces a corrupt file (see [Format versions](design-spec.md#format-versions)); an ended session's never will. One in an older format waits for `migrate`. | `path`; `reason`: `unreadable`, `corrupt`, `unsupported-format` (in another format), or `missing` (a session directory more than 60 seconds old with no `lifecycle.json`). |
| `migration-pending` | `state.json` records a [migration](design-spec.md#migrations) step behind this binary's latest: some files may be in an older format, which hooks leave alone and reads skip, until you run [`migrate`](#migrate). See [Migration status](#migration-status). | `recorded`; `latest`. |
| `migration-ahead` | `state.json` records a step past this binary's latest, or is in a newer format: a newer binary wrote this state directory, and this one leaves its newer files alone. See [Migration status](#migration-status). | `recorded`: `null` when `state.json` is in a newer format; `latest`. |
| `duplicate-id` | [`list`](#list) found several sessions with the same sesshin ID, which only an [outside change](design-spec.md#assumptions) makes. Each is listed. | `id`; `sessions`: [session refs](#session-ref), in [session order](#session-order). |
| `not-started` | [`spawn`](#spawn) launched its session, or [`resume`](#resume) resumed one, but didn't see it start within `start_timeout_secs`. | `job`, or `null`; `placement`: the launched window; `waited_secs`; for `resume`, `session`: a [session ref](#session-ref). |
| `transcript-missing` | [`resume`](#resume) launched a session whose transcript isn't where it was recorded. | `session`: a [session ref](#session-ref); `path`: the `transcript_path`. |
| `placement-not-recorded` | [`spawn`](#spawn) or [`resume`](#resume) launched its session, but couldn't record the window in its reservation, which then [goes stale](design-spec.md#reservations) unless the session adopts it first. | `job`; `placement`. |
| `status-line-replaced` | [`install`](#install)'s proposal replaces a `statusLine` sesshin didn't install. Applying it removes that one, and sesshin keeps no copy: this warning is the record. | `settings_path`; `status_line`: the replaced value, verbatim. |


### Warning schema

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "warning",
  "type": "object",
  "required": ["kind", "message", "details"],
  "properties": {
    "kind": { "type": "string", "description": "Open: a caller ignores one it doesn't know." },
    "message": { "type": "string" },
    "details": { "type": "object", "description": "Each kind's, below; any object for a kind this release doesn't know." }
  },
  "allOf": [
    { "if": { "properties": { "kind": { "const": "unusable-file" } } }, "then": { "properties": { "details": { "type": "object", "required": ["path", "reason"], "properties": { "path": { "type": "string" }, "reason": { "type": "string", "examples": ["unreadable", "corrupt", "unsupported-format", "missing"], "description": "Open set." } } } } } },
    { "if": { "properties": { "kind": { "const": "migration-pending" } } }, "then": { "properties": { "details": { "type": "object", "required": ["recorded", "latest"], "properties": { "recorded": { "type": "integer" }, "latest": { "type": "integer" } } } } } },
    { "if": { "properties": { "kind": { "const": "migration-ahead" } } }, "then": { "properties": { "details": { "type": "object", "required": ["recorded", "latest"], "properties": { "recorded": { "type": ["integer", "null"], "description": "null when state.json is in a newer format." }, "latest": { "type": "integer" } } } } } },
    { "if": { "properties": { "kind": { "const": "duplicate-id" } } }, "then": { "properties": { "details": { "type": "object", "required": ["id", "sessions"], "properties": { "id": { "type": "integer" }, "sessions": { "type": "array", "items": { "$ref": "session-ref" } } } } } } },
    { "if": { "properties": { "kind": { "const": "not-started" } } }, "then": { "properties": { "details": {
      "type": "object",
      "required": ["job", "placement", "waited_secs"],
      "properties": {
        "job": { "anyOf": [{ "$ref": "defs#/$defs/job" }, { "type": "null" }] },
        "placement": { "$ref": "defs#/$defs/placement" },
        "waited_secs": { "type": "integer" },
        "session": { "$ref": "session-ref", "description": "resume only." }
      }
    } } } },
    { "if": { "properties": { "kind": { "const": "transcript-missing" } } }, "then": { "properties": { "details": { "type": "object", "required": ["session", "path"], "properties": { "session": { "$ref": "session-ref" }, "path": { "type": "string" } } } } } },
    { "if": { "properties": { "kind": { "const": "placement-not-recorded" } } }, "then": { "properties": { "details": { "type": "object", "required": ["job", "placement"], "properties": { "job": { "anyOf": [{ "$ref": "defs#/$defs/job" }, { "type": "null" }] }, "placement": { "$ref": "defs#/$defs/placement" } } } } } },
    { "if": { "properties": { "kind": { "const": "status-line-replaced" } } }, "then": { "properties": { "details": { "type": "object", "required": ["settings_path", "status_line"], "properties": { "settings_path": { "type": "string" }, "status_line": { "type": ["object", "array", "string", "number", "boolean", "null"], "description": "The replaced statusLine, verbatim: Claude Code's shape." } } } } } }
  ]
}
```

## Versioning

sesshin follows [Semantic Versioning](https://semver.org/) from 1.0.0. This section is the one home of what it promises; the [CHANGELOG](../CHANGELOG.md) says what each release changed.

**Stable:** a break in any of these is a major release.

- **The output:** the [envelope](#output-envelope), the [error](#error-kinds) and [warning](#warning-kinds) kinds and their `details`, and every operation's input and output schema here, the pickers' `actions` output ([picker-spec](picker-spec.md#output)) included.
- **The command line:** command names, flags, [selectors](cli-spec.md#selectors-on-the-command-line), and [exit codes](cli-spec.md#exit-codes).
- **The files:** the state directory's layout and each file's fields, as the [File schemas](design-spec.md#file-schemas) give them, for anyone reading them with `jq`. A format change bumps the file's `schema` and ships a [migration](design-spec.md#migrations), so no release replaces your files or loses your sessions' IDs, jobs, or `extra`.
- **`extra`:** yours, kept as you wrote it ([User-owned extra](design-spec.md#user-owned-extra)).
- **The configuration:** the keys of `config.toml` (`retain_days`, `retain_headless_hours`, `spawn_shell`) and of `hooks.properties` (`hook_lock_wait_ms`), with their meaning and units ([Configuration](design-spec.md#configuration)), and the environment variables a user sets: `SESSHIN_PICK_OPTS`, `CLAUDE_CONFIG_DIR`, `XDG_CONFIG_HOME`, and `XDG_STATE_HOME`. A config file that works keeps working. A new key is a minor change; since an unknown key is `corrupt`, a file using it is one more thing an older binary can't read.
- **The hook verbs:** `sesshin-hook`'s ten verbs ([Registration](hooks-spec.md#registration)), which `install` writes into `settings.json` and its `changes` name. None is renamed or removed before 2.0, so a `settings.json` an older `install` wrote keeps working with a newer `sesshin-hook` ([Frozen verbs](hooks-spec.md#frozen-verbs)).

**Minor changes,** which callers must allow for: an optional input field, an output field, an error or warning kind, an enum value an [open set](design-spec.md#open-sets) allows, a new command or flag, and a field added to a file (with its migration). Callers ignore what they don't know, and treat an unknown error kind as a generic failure; the output schemas leave every object open for this. A release that ships a migration step says so in the CHANGELOG: run [`migrate`](#migrate) after upgrading. One whose hook registration changed (a new event, or a new verb) says to run [`install`](#install) again. Neither is a break, but until `migrate` runs every command that reads sessions warns [`migration-pending`](#migration-status), the statusline shows no `#id`, and events are missed: a minor release may need it, as the CHANGELOG says.

**Not stable:** anything may change in any release:

- what is drawn for a person: the [statusline](hooks-spec.md#rendering), the pickers' lines and preview ([picker-spec](picker-spec.md)), help text, error `message`s, and the [stderr line](cli-spec.md#output);
- `hooks.log`'s text;
- `SESSHIN_JOB` and `SESSHIN_TOKEN`, which `spawn` and `resume` pass to the session they launch: a handoff between sesshin's own binaries, not a setting;
- going back to an older binary: none reads a newer one's files ([Format versions](design-spec.md#format-versions)).

**Claude Code's shapes are Claude Code's.** Some of what sesshin stores and reports is copied from Claude Code as it reported it: `statusline.json`'s `payload` and `show`'s `statusline_payload`, the session view's `metrics.rate_limits` and `prompt_cache.last_miss_cause`, and the values of the [open sets](design-spec.md#open-sets) copied from Claude Code (`end_reason`, `stall_reason`, `permission_mode`, `entrypoint`, and `last_event_type`'s qualifier). sesshin promises where they are and that they are copied as reported, not what is inside them: they change when Claude Code changes them, in any sesshin release or none.

**Claude Code changes.** sesshin is verified against the Claude Code versions [design-spec](design-spec.md#settled) lists. A Claude Code release can rename or drop a payload field sesshin reads, or add an event, and every field sesshin reads is optional ([Open sets](design-spec.md#open-sets)), so the worst case is a value sesshin reports as `null` or `unknown` until it adapts. A sesshin release that adapts (reading a field's new name, registering a new event) keeps every stable shape above, so it is a minor or patch release, never a break.

## Shared rules

### Selecting a session

[`show`](#show), [`resume`](#resume), [`send`](#send), [`focus`](#focus), and [`update`](#update) take a **selector**: a string naming one session.

| Form | Matches |
|---|---|
| `12` | The session whose [sesshin ID](design-spec.md#sesshin-ids) is 12. Decimal digits with no leading zero. `#12` is the display form, not a selector. |
| `0b6c5a3e-1f7e-4c2b-9a51-6d2f0e8b7c11` | The session with that UUID. |
| `0b6c5a3e` | Every session whose UUID starts with it: 8 to 36 characters of hex digits and hyphens, a prefix of the UUID as written. |
| `self` | The session the caller runs in (below). |
| `api`, `job:api` | One session that reports the [job](design-spec.md#reservations) `api`: the live one, if one holds it, else the most recently [seen](design-spec.md#liveness). |

- **`self` is the caller's own session,** found much as a hook finds its own ([Liveness](design-spec.md#liveness)): Claude's process is the nearest ancestor of the `sesshin` process that is `CLAUDE_PID` or a `claude` by the walk's name rule, at any depth, since a command in a pipeline or under `xargs` sits deeper than a hook; `self` is the session whose `pid` and `pid_started_at` match that process and which is its live session (Liveness rule 3). The nearest Claude wins: from inside a nested `claude -p`, `self` is the nested session. From outside any session (a plain shell, cron), or when no session matches, it is `not-found`. It names one session, so it behaves as an exact sesshin ID does in each operation's scope: `show self` reads the caller's own record, `resume self` fails `conflict` (`live`) as `resume 12` does on a live #12, and `send self` fails `conflict` (`mid-turn`) unless `force`, since a session running a command is mid-turn. `self` is checked before the job form, which it also matches; it can't be a UUID prefix (`s` and `l` aren't hex). A job named `self` stays legal, and is selected as `job:self`.
- **All digits is always a sesshin ID,** even when it is also 8 or more characters of hex. A UUID prefix that happens to be all digits is given one character longer.
- **8 to 36 characters of hex and hyphens is always a UUID prefix.** A job that looks like one (`deadbeef`, `cafe-1234`) is selected as `job:deadbeef`. Any other [job name](design-spec.md#reservations) is a job, bare or after `job:`.
- **Case doesn't matter** for a UUID or a prefix: it is lowercased before matching, as session UUIDs are stored. A job name is matched exactly, case included, though jobs that differ only in case are [one job](design-spec.md#reservations) for holding it: `api` doesn't select a session whose job is `API`.
- **A job selects one session, never `ambiguous`.** Every `/clear` in a job's window ends a session that keeps reporting the job, so after a day's work a job names many ended sessions. The one meant is the live one, which a job names at most one of, else the last one seen; ties break as in [session order](#session-order). Jobs are as readers report them.
- **Exact, never fuzzy.** Finding a session by its title or name is the pickers' job (fzf), not a selector's.
- **Within the operation's scope.** `show` and `update` select among live, ended, and headless sessions alike; `resume` among ended ones only, and `send` and `focus` among live ones (and liveness `unknown`) only; their Preconditions say how they report the others. A selector that matches nothing in scope is `not-found`.
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
    { "const": "self" },
    { "pattern": "^(job:)?(?![0-9]+$)[A-Za-z0-9](?:[A-Za-z0-9-]{0,62}[A-Za-z0-9])?$" }
  ]
}
```

### Reading the sessions

Every read walks `sessions/`: one directory per session, three small files each. Hidden entries are ignored. There is no index, so selecting one session reads every `sesshin.json` to find its sesshin ID. With [retention](design-spec.md#retention), that is a few hundred small files, as long as `prune` runs.

- **A session needs a usable `lifecycle.json`.** A session directory without one is skipped, with an `unusable-file` warning (`reason`: `missing` when there is none, and only once the directory is more than 60 seconds old, so a session being created stays silent; `unreadable`, `corrupt`, or `unsupported-format` when it is there and unusable). A session start that could not write (a full disk, a read-only `sessions/`) leaves such a directory. Such a session is in no result, and no selector matches it.
- **The other two files are optional.** An unusable `sesshin.json` reads as missing (`id` `null`, `job` `null`, `source` `null`, `placement` `null`, `extra` `null`), and an unusable `statusline.json` as missing (no metrics, no prompt cache, no pid fallback), each with an `unusable-file` warning.
- **A directory that vanishes mid-read** was pruned, and is skipped without a warning.

Liveness costs one `kill(pid, 0)` and one process start-time read per session with a pid, and a `stat` of each `transcript_path`.

### Narrowing

[`list`](#list) takes the few parameters an agent needs to keep its result small, since its output goes straight into the agent's context:

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
- **The name comes first, and the prompt follows `--`.** The arguments are `[--name <name>] <args…> -- <prompt>`, or without `-- <prompt>` when there is no prompt. `--name` is `spawn`'s `name`, else its job; what Claude Code does with it is [settled](design-spec.md#claude-code-21289), and a `claude --resume` brings it back without it. A `--name` in `args` comes later, and `claude` takes that one. A prompt that begins with `-` is then never read as an option, and an `args` list that ends in an option taking a value (`--model`) makes `claude` take `--` as that value and fail visibly in the new window, rather than swallow the prompt. sesshin passes `args` in order and never interprets them.
- **`resume` puts `--resume <uuid>` first.** Its arguments are `--resume <uuid> <args…>`, with no prompt. An `args` list that ends in an option taking a value then has none, and `claude` fails visibly in the new window, rather than taking `--resume` as the value and starting a new session. `args` that resume or continue another session (`--continue`, a second `--resume`) are passed like any others: `claude` decides.
- **The environment is the terminal's own,** never the caller's. kitty starts the window with its own environment (sesshin never passes `--copy-env`), plus `SESSHIN_TOKEN` for every `spawn` and for a `resume` under a job, and `SESSHIN_JOB` when there is a job, and sesshin passes no other `--env`. The session's `extra` travels in the [reservation](design-spec.md#reservations), never in the environment. A caller that is itself a Claude session (an agent running `sesshin spawn`) would otherwise make the new one read as [nested](design-spec.md#liveness), with no job and no placement. A remote `launch` passes none of the caller's variables ([verified](design-spec.md#kitty-0491)). It also can't remove one: a variable named alone (`--env=CLAUDECODE`) is set to `_delete_this_env_var_`, which would make the session nested, so sesshin names none. A kitty started from inside a Claude session would pass its own `CLAUDECODE` on; that is kitty's environment, and out of sesshin's reach.
- **The kitty launch** is one `kitten @ --to <socket> launch`, with `socket` the caller's `KITTY_LISTEN_ON`: `--type` `tab`, `window` (for `split`), or `os-window`; `--self`, so a tab or split goes beside the caller's window rather than the focused one; `--keep-focus`, so the caller keeps working; `--cwd`, `--tab-title` for a tab or OS window when there is a name, `spawn`'s or the title `resume` reopens under (a split keeps its tab's), one `--var` per user variable, and `--env` for the variables above. It prints the new window's ID, which with the socket is the launched window's placement. A nonzero exit is `launch-failed`; the 10-second limit passing, or output that isn't a positive integer, is `launch-unknown`.

### Finding a session's window

[`send`](#send) and [`focus`](#focus) find the session's window afresh on every call, never trusting the stored one: they ask the placement's backend for the window running the session's pid. kitty asks `kitten @ --to <socket> ls`, with the placement's `socket`, for the window whose foreground processes include that pid. When that socket doesn't answer within 5 seconds, or has no such window, and the caller's own `KITTY_LISTEN_ON` names another socket, it asks that one the same way. A window running `claude` lists it as its one foreground process, also while it runs a tool's command ([verified](design-spec.md#kitty-0491)). The window found, with the socket that answered, is **verified**. Neither repairs the stored placement ([Placement](design-spec.md#placement)). They differ only when nothing is verified, because the pid is unknown or no window has it: `send` fails, since the stored `window_id` may now hold a shell, and `focus` falls back to it, since focusing the wrong window is harmless.

### Migration status

Every operation that reads or writes the state directory, except [`uninstall`](#uninstall) (which reads only `install.json`) and [`migrate`](#migrate) (whose output says the same), first reads `state.json` with no lock, and warns [`migration-pending`](#warning-kinds) when its `migration` is behind this binary's latest [step](design-spec.md#migrations), or `migration-ahead` when it is past it or the file is in a newer format. A `state.json` at schema 1 records 0. A missing, unreadable, or corrupt one gives no warning: there is nothing to compare, and the operation reports what it would anyway. So a pending migration, whose cost is sessions listed without IDs and events not recorded, is never silent.

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
  }
}
```

### Session view

One session, as every read reports it: what is stored, and what is derived from it at read time, in this order. Fields from `sesshin.json` are `null` without a usable one, and fields from `statusline.json` before its first tick or without a usable one.

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "session-view",
  "type": "object",
  "required": ["id", "session_id", "name", "job", "source", "extra", "headless", "liveness", "status", "stall_reason", "pending", "attention", "cwd", "git_branch", "model", "permission_mode", "entrypoint", "nested", "pid", "pid_started_at", "started_at", "last_start_at", "last_event_at", "last_event_type", "event_seq", "last_seen", "ended_at", "end_reason", "compactions", "metrics", "prompt_cache", "placement", "transcript_path", "transcript_exists"],
  "properties": {
    "id": { "type": ["integer", "null"], "minimum": 1, "description": "Sesshin ID, from sesshin.json; null while an issue is pending or without a usable sesshin.json." },
    "session_id": { "type": "string", "description": "Claude's session UUID, lowercase." },
    "name": { "type": "string", "description": "Derived (design-spec Terms): the /rename title, else the statusline's session name, else #id, else the UUID's first 8 characters." },
    "job": { "type": ["string", "null"], "description": "Derived (design-spec Reservations): sesshin.json's job, or null for a live or unknown session whose job a live or unknown session that started earlier holds." },
    "source": { "type": ["string", "null"], "examples": ["spawn", "hook", null], "description": "Open set: spawn, hook, or a value this binary doesn't know. From sesshin.json; null without a usable one." },
    "extra": { "type": ["object", "null"], "description": "From sesshin.json, as stored, its key order and numbers kept (design-spec User-owned extra); null without a usable one." },
    "headless": { "type": "boolean", "description": "Derived (design-spec Terms): nested, or an sdk-… entrypoint. Hidden from list unless include_headless." },
    "liveness": { "enum": ["live", "ended", "unknown"], "description": "Derived (design-spec Liveness)." },
    "status": { "type": "string", "description": "Open set: idle, working, waiting, needs_approval, or a value this binary doesn't know. The last recorded status, also for an ended session." },
    "stall_reason": { "type": ["string", "null"] },
    "pending": {
      "type": ["object", "null"],
      "required": ["background_tasks", "session_crons"],
      "properties": { "background_tasks": { "type": "integer" }, "session_crons": { "type": "integer" } },
      "description": "null before any turn has ended, and while a turn is under way."
    },
    "attention": { "type": ["string", "null"], "examples": ["blocked", "stalled", "self_waking", "your_turn", "idle", "working", "unknown", null], "description": "Open set: one of these, or a value this binary doesn't know, which a reader treats as unknown. Derived (design-spec Attention) from status, stall_reason, and pending: what the session wants from you. null for an ended session." },
    "cwd": { "type": ["string", "null"] },
    "git_branch": { "type": ["string", "null"], "description": "From statusline.json." },
    "model": { "type": ["string", "null"], "description": "Derived: the statusline's model.id when its payload is from the session's current life (received_at at or after last_start_at), else SessionStart's." },
    "permission_mode": { "type": ["string", "null"] },
    "entrypoint": { "type": ["string", "null"] },
    "nested": { "type": ["boolean", "null"] },
    "pid": { "type": ["integer", "null"], "description": "The pid liveness judged: lifecycle.json's, else statusline.json's." },
    "pid_started_at": { "type": ["string", "null"], "description": "Opaque; compare only for equality. The start time of the process pid names, from the same file as pid: lifecycle.json's, else statusline.json's. null exactly when pid is." },
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
      }
    },
    "prompt_cache": {
      "type": ["object", "null"],
      "description": "Derived (design-spec Prompt cache); null without a statusline.json.",
      "required": ["state", "expires_at", "recache_tokens", "hit_ratio", "misses", "last_miss_cause"],
      "properties": {
        "state": { "type": "string", "examples": ["warm", "cold", "unknown"], "description": "Open set: a value this binary doesn't know reads as unknown." },
        "expires_at": { "type": ["string", "null"], "description": "While warm, expires_at as a timestamp, its fraction dropped; null otherwise." },
        "recache_tokens": { "type": ["integer", "null"], "description": "recache_tokens_if_cold, when a non-negative integer." },
        "hit_ratio": { "type": ["number", "null"] },
        "misses": { "type": ["integer", "null"] },
        "last_miss_cause": { "type": ["array", "null"], "items": { "type": "string" }, "description": "last_miss_cause.causes, as reported." }
      }
    },
    "placement": { "$ref": "defs#/$defs/placement" },
    "transcript_path": { "type": ["string", "null"] },
    "transcript_exists": { "type": ["boolean", "null"], "description": "Whether transcript_path names an existing file, so a claude --resume can find it; null when transcript_path is, or when the stat failed other than with ENOENT." }
  }
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
    "attention": { "$ref": "session-view#/properties/attention" },
    "cwd": { "$ref": "session-view#/properties/cwd" },
    "git_branch": { "$ref": "session-view#/properties/git_branch" },
    "model": { "$ref": "session-view#/properties/model" },
    "permission_mode": { "$ref": "session-view#/properties/permission_mode" },
    "entrypoint": { "$ref": "session-view#/properties/entrypoint" },
    "nested": { "$ref": "session-view#/properties/nested" },
    "pid": { "$ref": "session-view#/properties/pid" },
    "pid_started_at": { "$ref": "session-view#/properties/pid_started_at" },
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
  }
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
  }
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
| `unusable-file` | A session file is unusable: the session is left out (`lifecycle.json`, missing included), or listed with what was readable. |
| `duplicate-id` | Several listed sessions share a sesshin ID. |
| `migration-pending`, `migration-ahead` | [Migration status](#migration-status). |

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
  }
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
| `migration-pending`, `migration-ahead` | [Migration status](#migration-status). |

**Retry safety:**

- After anything: safe. It changes nothing.

### version

Report this binary's version, the file formats it supports, and its latest [migration](design-spec.md#migrations) step.

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
  "required": ["version", "commit", "modified", "go", "formats", "migration"],
  "properties": {
    "version": { "type": "string", "description": "The main module's version as Go stamped it: a tag, a pseudo-version, either with +dirty, or (devel)." },
    "commit": { "type": ["string", "null"], "description": "vcs.revision; null when the build has no VCS information." },
    "modified": { "type": "boolean", "description": "vcs.modified: built with uncommitted changes. false when commit is null." },
    "go": { "type": "string" },
    "formats": {
      "type": "object",
      "required": ["state", "lifecycle", "statusline", "sesshin", "reservation", "install"],
      "additionalProperties": { "type": "integer" }
    },
    "migration": { "type": "integer", "minimum": 0, "description": "The latest migration step this binary knows (design-spec Migrations)." }
  }
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

**Applying it is yours.** The output's `apply` holds two commands: `diff -uN` of `settings.json` against the proposal, to review it, and `mkdir -p` of `settings.json`'s directory (a fresh machine may not have `~/.claude` yet) followed by `cat` of the proposal redirected onto `settings.json`, which writes through a symlink and keeps the file's mode. Run `install` with `dry_run` to check the result, which writes nothing: when every item of `changes` is `unchanged`, `settings.json` wires sesshin. Running it again after an upgrade or a move proposes the re-wiring.

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
          "action": { "type": "string", "examples": ["added", "replaced", "unchanged", "removed"], "description": "Open set. A permission rule is only ever added or unchanged." }
        }
      }
    }
  }
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
| `migration-pending`, `migration-ahead` | [Migration status](#migration-status). |

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

**Effects:** writes `<state>/settings.proposed.json`: the `settings.json` that `install.json` records (else the one this process resolves), with every entry sesshin owns removed, `statusLine` removed while it is still [sesshin's](#sesshins-entries-in-settingsjson), and every copy of sesshin's five permission rules removed from the array [Registration](hooks-spec.md#registration) puts each in (any other rule stays, `Bash(jq:*)` included). A `statusLine` set since install is left alone and reported `unchanged`. Every other entry stays exactly as it is, including ones added since install; the proposal is built as [Registration](hooks-spec.md#registration) says, and applied as `install`'s is. Apart from the proposal, the state directory is left alone: what sesshin recorded is yours to delete. With `dry_run`, nothing is written, and the output reports what would change, with `proposal_path` and `apply` `null`.

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
          "action": { "type": "string", "examples": ["removed", "unchanged"], "description": "Open set." }
        }
      }
    }
  }
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

Launch `claude` in a new tab, split, or OS window of the caller's terminal, through a [reservation](design-spec.md#reservations) that hands the session its job, if any, and its `extra`, and optionally wait for it to start.

**Kind:** write. Waits briefly for the state lock to reserve, and again after the launch to record the window ([Locks](design-spec.md#locks)); holds no lock across the launch or the wait.

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
    "vars": { "type": "object", "additionalProperties": { "type": "string" }, "default": {}, "description": "The new window's user variables (in kitty, --var): for matching windows, not environment variables. A terminal backend without user variables refuses any." },
    "extra": { "type": "object", "description": "The session's user-owned extra (design-spec User-owned extra), handed over in the reservation. None when absent: the session starts with {}." },
    "start_timeout_secs": { "type": "integer", "minimum": 0, "maximum": 120, "default": 15, "description": "How long to wait for the session to start. 0 returns as soon as the window is open." }
  },
  "additionalProperties": false
}
```

**Additional validation:** `cwd` is absolute. No string in `cwd`, `name`, `prompt`, `args`, or `vars` holds a NUL, which no argument vector can carry. Each `vars` key matches `^[A-Za-z_][A-Za-z0-9_]{0,63}$`. `extra` is within `extra`'s [limits](design-spec.md#user-owned-extra), measured as compact JSON. Its numbers are exempt from the integer-literal rule, and kept as given.

**Preconditions:** `cwd` is an existing directory. The backend can place the caller ([Placement](design-spec.md#placement)). With a `job`: no live session holds it, and no fresh reservation of its key names it, comparing [keys](design-spec.md#reservations), so a held `API` refuses `api`.

**Effects:**

1. **Check the job's reservations' windows,** with a `job` and no lock held: for each reservation of its [key](design-spec.md#reservations) (the job lowercased), `reservations/<key>_*.json`, that is launched and not stale by age, ask the backend whether its window exists, as [`prune`](#prune) does.
2. **Reserve:** wait for the state lock ([Locks](design-spec.md#locks); else `busy`), with a `job` or without. Under it, with a `job`: read every session as [`list`](#list) does, and fail `conflict` (`rule`: `job-taken`) when a live session (liveness `live` or `unknown`) reports a job with the same key. Read `reservations/<key>_*.json` again: fail `job-taken` when one is fresh, judging its window by step 1's answer only if it still holds the `token` and `placement` asked about; otherwise remove the stale ones. Then, with a `job` or without, create the reservation (`{schema, job, token, created_at, placement, extra}`) with a new random `token`: `reservations/<key>_<token>.json` with `job` as given, or `reservations/<token>.json` with `job` `null`; `created_at` now, `placement` `null`, and `extra` as given, its key order and numbers kept, or `{}`. Release the lock. `reservations/` is created if missing.
3. **Launch** through the backend, as [Launching `claude`](#launching-claude) says: in `cwd`, named and titled `name` (else the job), with `vars` set, with `SESSHIN_TOKEN=<token>` in its environment, and `SESSHIN_JOB=<job>` with a `job`. The backend's launch has a 10-second limit.
4. **On failure:**
   - The backend refused (`kitten` missing, a socket that refuses, a nonzero exit): nothing was opened. Under the state lock, waited for as in step 5, remove the reservation if it is still there, so the job is free at once, and fail `terminal` (`reason`: `launch-failed`).
   - The limit passed, or the backend answered without a window it could name: a window may have opened. Keep the reservation, which a session that starts adopts, and which goes stale ([Reservations](design-spec.md#reservations)) if none does. Fail `terminal` (`reason`: `launch-unknown`).
5. **Record the window:** wait for the state lock ([Locks](design-spec.md#locks)). Under it, if the reservation is still there and holds this `token`, rewrite it with the launched window as its `placement`, everything else unchanged, so it stays fresh while the window waits at Claude's workspace-trust dialog. If it is gone (the session has already adopted it, or it was released by hand), leave it. If the lock isn't taken in time, or the rewrite fails, warn `placement-not-recorded` and go on: the reservation goes stale unless the session adopts it first ([Reservations](design-spec.md#reservations)).
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
  }
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
| `terminal` | (`reason`: `unsupported`) The caller's backend can't launch a window, or `vars` isn't empty and it can't set user variables. Nothing was reserved. |
| `busy` | (`lock`: `state`) The state lock was held past the wait, with a `job` or without. |
| `conflict` | (`rule`: `job-taken`) A live session or a fresh reservation holds `job`, or a job differing from it only in case, which the message names as stored. `sessions` names the session, empty for a reservation. |
| `terminal` | (`reason`: `launch-failed`) The backend refused the launch; the reservation was removed. (`reason`: `launch-unknown`) The launch timed out or its answer named no window; the reservation was kept. |

**Warnings:**

| Kind | When |
|---|---|
| `not-started` | The session didn't start within `start_timeout_secs`. It may still start: `list` shows it once it has. |
| `placement-not-recorded` | The launched window couldn't be recorded in the reservation (step 5). |
| `unusable-file` | A session file read while checking the job, or while waiting, couldn't be used, as [`list`](#list) reports it. |
| `migration-pending`, `migration-ahead` | [Migration status](#migration-status). |

**Retry safety:**

- After `invalid-input`, `environment`, `corrupt`, `not-found`, `terminal` (`unavailable`, `unsupported`, or `launch-failed`), `busy`, or `conflict`: safe. Nothing was launched, and no reservation remains.
- After `terminal` (`launch-unknown`), or success with `not-started`: **not** safe. A session may still be starting. A retry with the same job fails `job-taken` while the reservation is fresh; with no job, it opens a second window. Check `sesshin list` first.
- After a crash (exit 3 or a signal): not safe, for the same reason. A reservation left behind goes stale if it was never launched ([Reservations](design-spec.md#reservations)), and is removed by the next `spawn` of its job, or by `prune`.

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

**Preconditions:** `session` selects one ended session ([Selecting a session](#selecting-a-session)): a sesshin ID or UUID of a session that is live, or whose liveness is `unknown`, is refused, not resumed twice. Its `cwd` is recorded and is an existing directory. The caller runs in a terminal a backend recognizes, outside tmux and screen, as for `spawn`. If it resumes under a job (`job`, else the session's stored job, as an ended session reports it): the session's `sesshin.json` is not in another [format](design-spec.md#format-versions), since the resumed session's `session-start` leaves such a file alone and never adopts the reservation, which would hold the job with no session; and no live session holds the job, and no fresh reservation names it.

**Effects:**

1. **Select** the session, reading every session as [`list`](#list) does, and check it is ended. A headless session can be resumed like any other.
2. **Check the file's format, the job's reservations' windows, then reserve,** only if it resumes under a job: read the session's `sesshin.json`, and fail `conflict` (`rule`: `other-format`) if it is in another format, before anything else is checked or written (a missing or corrupt file doesn't count); then as `spawn`'s steps 1 and 2, with the job and `extra` `{}`. Without a job, `resume` writes no reservation: the session keeps its own `extra`, so there is nothing to hand over.
3. **Launch** a tab through the backend, as [Launching `claude`](#launching-claude) says, running `claude --resume <uuid> <args…>`: in the session's `cwd`, titled with its placement's stored `tab_title`, else its [name](design-spec.md#terms); with its stored `user_vars` as the window's user variables when its placement is the caller's backend's (kitty's) and has them; and with `SESSHIN_JOB=<job>` and `SESSHIN_TOKEN=<token>` in its environment when it resumes under a job. The backend's launch has a 10-second limit.
4. **On failure,** and **record the window** (under a job): as `spawn`'s steps 4 and 5.
5. **Wait** up to `start_timeout_secs` for the session to be live again (liveness `live` or `unknown`, as `spawn` counts it), reading only its own directory every 100 ms, with no lock. A transcript `claude` can't find makes it start a new session instead, or exit: this one then never comes back, and the wait runs out.

`resume` writes no session file: the session keeps the `extra` in its `sesshin.json` ([User-owned extra](design-spec.md#user-owned-extra)). The resumed session's own [`session-start`](hooks-spec.md#session-start) revives its directory (new `pid`, `ended_at` cleared, `event_seq` continuing) and, through the `SESSHIN_TOKEN` it was launched with, [adopts](design-spec.md#reservations) the reservation, which sets its job when `job` named another, and never its `extra`.

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
  }
}
```

**Errors,** in this order:

| Kind | When |
|---|---|
| `invalid-input` | A bad `session`, `job`, `args`, or `start_timeout_secs`. |
| `environment` | `HOME` is unusable. |
| `corrupt` | `config.toml` is corrupt. |
| `not-found` | (`selectors`) `session` selects no session; for a job, no ended one. |
| `ambiguous` | `session` selects several sessions. |
| `conflict` | (`rule`: `live`) `session` selects a live session, or one whose liveness is `unknown`. `sessions` names it. |
| `not-found` | (`paths`) The session's `cwd` is gone or isn't a directory (the `cwd`), or was never recorded (empty). |
| `terminal` | (`reason`: `unavailable`) As for `spawn`. |
| `terminal` | (`reason`: `unsupported`) The caller's backend can't launch a window. Nothing was reserved. |
| `conflict` | (`rule`: `other-format`) It resumes under a job, and the session's `sesshin.json` is in another [format](design-spec.md#format-versions). `sessions` names the session and `path` the file; the message says to run `sesshin migrate` first, or to upgrade `sesshin` when the file is newer. |
| `busy` | (`lock`: `state`) It resumes under a job, and the state lock was held past the wait. |
| `conflict` | (`rule`: `job-taken`) A live session or a fresh reservation holds the job, or one differing from it only in case, which the message names as stored. `sessions` names the session, empty for a reservation; the message suggests `job`. |
| `terminal` | (`reason`: `launch-failed`, `launch-unknown`) As for `spawn`. |

**Warnings:**

| Kind | When |
|---|---|
| `transcript-missing` | The session's transcript isn't where it was recorded (`transcript_exists` `false`), so `claude --resume` will likely fail in the new tab. Launched anyway: the transcript may be somewhere sesshin can't see. |
| `not-started` | The session wasn't live again within `start_timeout_secs`. It may still start. |
| `placement-not-recorded` | As for `spawn`. |
| `unusable-file` | A session file read while selecting, checking the job, or waiting couldn't be used, as [`list`](#list) reports it. |
| `migration-pending`, `migration-ahead` | [Migration status](#migration-status). |

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

**Preconditions:** the session is live, or its liveness is `unknown`. Its turn has ended (status `waiting` or `idle`), unless `force`: mid-turn, a dialog may have the keyboard, and text plus Enter could answer it, while the status can't tell (a permission prompt, an `AskUserQuestion`, or a plan approval is reported only some 6 seconds after it appears, [verified](design-spec.md#claude-code-21293)). Once the turn has ended, no tool dialog can be up. An unknown status counts as mid-turn. A turn interrupted with Esc or Ctrl-C sends no hook, so the session reads `working` (or `needs_approval`) until its next event, and only `force` reaches it ([Status](design-spec.md#status)). It has a placement whose backend can find a window by pid and paste into it, and a pid.

**Effects:**

1. **Select** the session, reading every session as [`list`](#list) does ([Selecting a session](#selecting-a-session)): a job selects the live session holding it.
2. **Check** it: refuse an ended session, a turn not ended (without `force`), a session with no placement or a placement of a terminal sesshin has no backend for, and a session whose pid is unknown, since its window can't be verified.
3. **Find its window,** as [Finding a session's window](#finding-a-sessions-window) says. With no window verified, fail `terminal` (`unreachable`) and type nothing: the stored `window_id` may now hold a shell.
4. **Paste** the text as one bracketed paste, through the backend. kitty's is `kitten @ --to <socket> send-text --match id:<window> --bracketed-paste=disable --stdin`, with `ESC[200~`, the text, and `ESC[201~` on stdin. sesshin adds the markers itself because kitty's own (`--bracketed-paste`) wraps each 2048-byte chunk of a longer text as a paste of its own, which Claude Code then shows and submits as separate pastes, with line breaks between them ([verified](design-spec.md#kitty-0491)).
5. **Submit,** if `submit`: a second call, kitty's `send-text --match id:<window> '\r'`. Enter sent at once after the paste submits it whole ([verified](design-spec.md#claude-code-21289)).

Each `kitten` call has a 5-second limit.

**What Claude Code does with a paste** is [settled](design-spec.md#claude-code-21289), and `send` doesn't control it: several lines reach the model wrapped in `<pasted_content>` tags, and tabs become spaces.

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
  }
}
```

**Errors,** in this order:

| Kind | When |
|---|---|
| `invalid-input` | A bad `session`, `text`, `submit`, or `force`. |
| `environment` | `HOME` is unusable. |
| `not-found` | (`selectors`) `session` selects no live session. |
| `ambiguous` | `session` selects several sessions. |
| `conflict` | (`rule`: `not-live`) `session` selects an ended session by sesshin ID or UUID. (`mid-turn`) Its turn hasn't ended, and no `force`. (`no-placement`) It has no placement, or one sesshin has no backend for. `sessions` names it. |
| `terminal` | (`reason`: `unsupported`) Its placement's backend can't find a window by pid, or can't paste. Nothing was typed. |
| `terminal` | (`reason`: `unreachable`) Its pid is unknown, or no window with it was found. Nothing was typed. |
| `terminal` | (`reason`: `send-failed`) The paste failed: some of the text may be in the input box. (`submit-failed`) The text was pasted, but Enter failed: it waits in the input box. |

**Warnings:**

| Kind | When |
|---|---|
| `unusable-file` | A session file read while selecting couldn't be used, as [`list`](#list) reports it. |
| `migration-pending`, `migration-ahead` | [Migration status](#migration-status). |

**Retry safety:**

- After any error but `terminal` (`send-failed` or `submit-failed`): safe. Nothing was typed.
- After `terminal` (`send-failed` or `submit-failed`), a crash, or an unclear outcome: **not** safe. The text may be in the input box, or submitted, and a retry types it again. Look at the session's window, or its `event_seq`, first.

### focus

Bring a live session's window to the front, with its tab and OS window: the non-interactive core of [`jump`](picker-spec.md#jump).

**Kind:** write, on the terminal only. Takes no lock and writes no file, as [`send`](#send).

**Input schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "focus-input",
  "type": "object",
  "required": ["session"],
  "properties": {
    "session": { "$ref": "selector", "description": "Among live sessions." }
  },
  "additionalProperties": false
}
```

**Additional validation:** none.

**Preconditions:** the session is live, or its liveness is `unknown`, and it has a placement whose backend can focus a window. Any status will do, and the pid may be unknown.

**Effects:**

1. **Select** the session, reading every session as [`list`](#list) does ([Selecting a session](#selecting-a-session)).
2. **Check** it: refuse an ended session, and a session with no placement or a placement of a terminal sesshin has no backend for.
3. **Find its window,** as [Finding a session's window](#finding-a-sessions-window) says. With none verified, use the stored `socket` and `window_id`.
4. **Focus** it, through the backend. kitty's is `kitten @ --to <socket> focus-window --match id:<window>`, with a 5-second limit. kitty activates its tab and OS window with it.

**Output schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "focus-output",
  "type": "object",
  "required": ["session", "placement", "verified", "attention"],
  "properties": {
    "session": { "$ref": "session-ref" },
    "placement": { "$ref": "defs#/$defs/placement", "description": "The window focused: the one found in step 3, or the stored one." },
    "verified": { "type": "boolean", "description": "Whether the window was found by the session's pid. false: the stored window_id was focused, and may not be the session's." },
    "attention": { "$ref": "session-view#/properties/attention", "description": "The session's attention when it was focused." }
  }
}
```

**Errors,** in this order:

| Kind | When |
|---|---|
| `invalid-input` | A bad `session`. |
| `environment` | `HOME` is unusable. |
| `not-found` | (`selectors`) `session` selects no live session. |
| `ambiguous` | `session` selects several sessions. |
| `conflict` | (`rule`: `not-live`) `session` selects an ended session by sesshin ID or UUID. (`no-placement`) It has no placement, or one sesshin has no backend for. `sessions` names it. |
| `terminal` | (`reason`: `unsupported`) Its placement's backend can't focus a window. |
| `terminal` | (`reason`: `focus-failed`) `focus-window` failed or timed out: no socket answered, or the stored window is gone. |

**Warnings:**

| Kind | When |
|---|---|
| `unusable-file` | A session file read while selecting couldn't be used, as [`list`](#list) reports it. |
| `migration-pending`, `migration-ahead` | [Migration status](#migration-status). |

**Retry safety:** safe. Focusing twice is focusing once.

### update

Change a session's user-owned [`extra`](design-spec.md#user-owned-extra), live or ended: replace it, or set and remove keys. The one operation that writes `extra` once a session has it. It is how a session is tagged after it starts, including by itself: `sesshin update self --extra-merge '{"ticket":"auth-3"}'` after a `/new`.

**Kind:** write. Takes the session's lock, waiting as [Locks](design-spec.md#locks) says; never the state lock.

**Input schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "update-input",
  "type": "object",
  "required": ["session", "extra"],
  "properties": {
    "session": { "$ref": "selector", "description": "Among all sessions: live, ended, and headless. self is the caller's own." },
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

`extra` is an object rather than the field itself so that `update` can take more fields later; it is the only one now.

**Additional validation:** `extra.merge` and `extra.remove` share no key. `replace_all` and `merge` are each within `extra`'s [limits](design-spec.md#user-owned-extra), and their numbers are exempt from the integer-literal rule, kept as given. With these rules, the order in which `merge` and `remove` apply doesn't matter.

**Preconditions:** `session` selects one session ([Selecting a session](#selecting-a-session)), whatever its liveness. It has a usable `sesshin.json` with an `id`: one whose `id` is still `null` doesn't count, since the hook that completes it may still adopt a [reservation](design-spec.md#reservations) and write its `extra` ([Creating `sesshin.json`](hooks-spec.md#creating-sesshinjson)). Refusing it means that write never meets an `extra` someone changed, so nothing needs merging. Reaching it takes a state-lock timeout at the session's first hook, and for `update self` another at its first prompt, so the refusal is rare.

**Effects:**

1. **Select** the session, reading every session as [`list`](#list) does. A session without a usable `sesshin.json` has no sesshin ID and no job, so only its UUID, a prefix of it, or `self` selects it.
2. **Lock** its directory ([Locks](design-spec.md#locks); else `busy`, `lock`: `session`). Once locked, check that the path still names the directory that was locked, as a hook does ([Recording an event](hooks-spec.md#recording-an-event)): if a [`prune`](#prune) renamed it aside meanwhile, or it is gone, fail `not-found`.
3. **Read** `sesshin.json` again, under the lock: this read, not step 1's, decides.
   - **There, but not readable** (a permission denied, an I/O error, a directory in its place): fail `io`.
   - **Missing or corrupt:** fail `conflict` (`rule`: `no-sesshin-file`, `file`: `missing` or `unusable`). `update` never creates `sesshin.json`: creating it issues a sesshin ID and decides the job and `extra`, which only a hook does ([Creating `sesshin.json`](hooks-spec.md#creating-sesshinjson)).
   - **In another [format](design-spec.md#format-versions):** fail `conflict` (`rule`: `no-sesshin-file`, `file`: `other-format`). Every hook leaves such a file alone, so neither a prompt nor a `resume` changes it: an older one waits for [`migrate`](#migrate), and a newer one for a newer `sesshin`.
   - **Its `id` is `null`:** fail `conflict` (`rule`: `no-sesshin-file`, `file`: `pending`), as Preconditions says.
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
      "items": { "type": "string", "examples": ["extra"], "description": "Open set: the names of sesshin.json fields update can change." },
      "description": "The fields whose value changed, compared as JSON values (see Effects); empty if none did, in which case the file was not rewritten."
    }
  }
}
```

**Errors,** in this order:

| Kind | When |
|---|---|
| `invalid-input` | A bad `session` or `extra`: `replace_all` with another form, `merge` and `remove` sharing a key, an empty `extra`, `merge`, or `remove`, or a value past `extra`'s limits. |
| `environment` | `HOME` is unusable. |
| `not-found` | (`selectors`) `session` selects no session, or its directory was pruned while `update` waited for its lock (the selector as given; the message names the session). |
| `ambiguous` | `session` selects several sessions. |
| `busy` | (`lock`: `session`) A hook held the session's lock past the wait. |
| `io` | `sesshin.json` is there but can't be read (`path`, `code`). |
| `conflict` | (`rule`: `no-sesshin-file`) The session has no `sesshin.json` it can change: `file` is `missing`, `unusable`, `other-format`, or `pending`, `path` names it, and `sessions` names the session. The message says why and what to do, by `file` and the session's liveness (below). |
| `conflict` | (`rule`: `extra-too-large`) The result would break `extra`'s limits. `sessions` names the session. |

**No `sesshin.json` to change.** sesshin writes `sesshin.json`, with its ID, at a session's first hook, so this is rare, and each case has its own way out. The message names it; `file` and the session's `liveness` let a script decide without parsing the message:

| `file` | Liveness | Why | What to do |
|---|---|---|---|
| `missing` | `live` or `unknown` | Its first hook hasn't written the file yet, e.g. while it waits at the workspace-trust dialog, or that hook couldn't. | Retry after its next prompt: that hook writes it. |
| `missing` | `ended` | It ended before any hook wrote the file, or the file was removed by hand. No hook of an ended session will write it. | `sesshin resume <uuid>`: its `session-start` writes the file. Then retry. |
| `unusable` | `live` or `unknown` | The file is corrupt ([Format versions](design-spec.md#format-versions)). | Retry after its next prompt: that hook writes it afresh, with a new sesshin ID and `extra` `{}`. |
| `unusable` | `ended` | As above, and no hook of an ended session will rewrite it. | `sesshin resume <uuid>`, then retry. |
| `other-format` | any | The file is in an older format, which no hook rewrites, or a newer one, which this `sesshin` can't read. | Older: `sesshin migrate`, then retry. Newer: upgrade `sesshin`. |
| `pending` | `live` or `unknown` | Its ID couldn't be issued yet ([When the ID can't be issued](hooks-spec.md#when-the-id-cant-be-issued)), and the hook that issues it may still hand it its reservation's `extra`. | Retry after its next prompt: that hook completes it. |
| `pending` | `ended` | As above, and no hook of an ended session will complete it. | `sesshin resume <uuid>`: its `session-start` completes it. Then retry. |

For example: `session 0b6c5a3e has no sesshin.json yet (it is live, and its first hook hasn't written one): retry after its next prompt`.

**Warnings:**

| Kind | When |
|---|---|
| `unusable-file` | A session file read while selecting, or for the output, couldn't be used, as [`list`](#list) reports it. |
| `migration-pending`, `migration-ahead` | [Migration status](#migration-status). |

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

Then reservations: each visible regular file in `reservations/` whose name ends in `.json` is read; other entries are ignored. One whose name doesn't parse as `<key>_<token>.json` or `<token>.json` ([Reservations](design-spec.md#reservations)), such as one named before tokens (`api.json`), is unusable. For each launched reservation not already stale by age, the backend is asked whether its window exists, with no lock held. Then `prune` tries the state lock once; if it is held, no reservation is judged further, and `reservations_locked` is `true`. Under the lock, each reservation is read again, and removed when it is unusable, or stale by age, or its window was found gone and it still holds the `token` and `placement` it was asked about. Its `reason` is the first that applies of `unusable`, `expired`, `stranded`, and `window-gone`. A missing `reservations/` is no reservations, and `prune` never creates it. A missing `sessions/` ends the run before reservations too: the state lock is `sessions/`, and nothing creates a reservation before it exists.

When the clock is [unusable](design-spec.md#retention), nothing is removed, sessions or reservations, and `cutoff` is `null`. `state.json` is never written. With `dry_run`, nothing changes and the output says what would: it goes through the same locks and stops before each removal.

**Output schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "prune-output",
  "type": "object",
  "required": ["dry_run", "cutoff", "headless_cutoff", "pruned", "kept_ended", "skipped_locked", "reservations_removed", "reservations_locked"],
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
        }
      }
    },
    "kept_ended": { "type": "integer", "description": "Ended sessions inside the window." },
    "skipped_locked": { "type": "integer", "description": "Prunable sessions skipped because their lock was held; the next run judges them again." },
    "reservations_removed": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["file", "job", "created_at", "reason"],
        "properties": {
          "file": { "type": "string", "description": "The reservation's file name, in reservations/." },
          "job": { "type": ["string", "null"], "description": "The stored job, case kept; null for a reservation with no job, and for an unusable one." },
          "created_at": { "type": ["string", "null"], "description": "null for an unusable reservation." },
          "reason": { "type": "string", "examples": ["stranded", "window-gone", "expired", "unusable"], "description": "Open set. stranded, window-gone, expired: the reasons a reservation is stale (design-spec Reservations); unusable: design-spec Reservations." }
        }
      }
    },
    "reservations_locked": { "type": "boolean", "description": "The state lock was held, so no reservation was removed; the next run judges them again." }
  }
}
```

**Order:** `pruned` by last seen, oldest first; ties by `session_id`. `reservations_removed` by `file`.

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
| `unusable-file` | A session couldn't be judged (its `lifecycle.json` is unusable, or missing: `reason` `missing`, whatever its age); it is kept. Or a reservation is unusable; it is removed (with `dry_run`, it would be). Raised as the reservation is judged under the state lock, so not when the lock was held. |
| `migration-pending`, `migration-ahead` | [Migration status](#migration-status). |


**Retry safety:**

- After anything, a crash included: safe. A crash between rename and removal leaves a hidden directory that reads ignore; cleaning those up is deferred with `repair`.

### migrate

Bring the state directory's files to this binary's formats: run every [migration step](design-spec.md#migrations) after the one `state.json` records, in order, then record the latest. sesshin never runs it on its own: run it right after replacing the binaries.

**Kind:** write. Waits for each session's lock in turn, then for the state lock, never holding both, waiting as [Locks](design-spec.md#locks) says.

**Input schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "migrate-input",
  "type": "object",
  "properties": {
    "dry_run": { "type": "boolean", "default": false }
  },
  "additionalProperties": false
}
```

**Additional validation:** none.

**Preconditions:** `state.json` is neither in a newer [format](design-spec.md#format-versions) nor records a step past this binary's latest.

**Effects:**

1. **Read `state.json`,** with no lock, for the recorded step, `from`: its `migration`; 0 at schema 1; and 0 when it is missing or corrupt while `sessions/` holds a session (a hook rebuilds it so, too: see [Migrations](design-spec.md#migrations)). Missing or corrupt with no session, there is nothing to convert, and `from` is the latest. When `from` is the latest, nothing further is read or written.
2. **Convert each session,** in UUID order: each visible directory in `sessions/` with a UUID name. Wait for its session lock ([Locks](design-spec.md#locks)); past that, fail `busy` (`lock`: `session`). Once locked, check that the path still names the directory locked, as a hook does: a session [pruned](#prune) meanwhile is skipped. Then, for each file of the session that a pending step covers: a file in an older format has the steps from its own `schema` applied in memory, and is written once, atomically, if the result validates as this binary's format; it is listed in `converted`. A file in this binary's format, missing, or corrupt is left alone; one in a newer format too, with an [`unusable-file`](#warning-kinds) warning. A file in an older format that the steps can't read, or whose result doesn't validate, is left alone and listed in `unconverted`, with an `unusable-file` warning. A file that can't be read fails the run with `io`, since converting the rest and advancing the number would strand it.
3. **Record the latest,** after every session lock is released. Wait for the state lock ([Locks](design-spec.md#locks)); past that, fail `busy` (`lock`: `state`). Under it, read `state.json` again: still at `from` or behind (another `migrate` may have finished first), apply the steps that cover it, set `migration` to the latest, and write it, flushed. One missing or corrupt is written afresh, `last_id` rebuilt from the highest `id` in any `sesshin.json`, as a hook rebuilds it ([Sesshin IDs](design-spec.md#sesshin-ids)), unless a `sesshin.json` is still in another format after step 2 (newer, or older and unconverted): its `id` can't be read, so a rebuild could reissue it. Then `state.json` is left as it is, such a file is listed in `unconverted` (one in a newer format is added there, with its `unusable-file` warning from step 2), and the number is not advanced, as for an unconvertible `state.json`. Found newer now: `unsupported-format`. One in an older format that the steps can't convert is listed in `unconverted` and left as it is, and the number is not advanced, since it is recorded in that file; removing `state.json` lets the next hook or `migrate` rebuild it.

Hooks run alongside it. A session already converted records as usual; one not yet converted has its older files left alone, as before `migrate` ran, and a new session gets a pending `id` until step 3 is done ([Format versions](design-spec.md#format-versions)). With `dry_run`, nothing changes and the output says what would: it goes through the same locks (none with no `sessions/`) and stops before each write.

**Output schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "migrate-output",
  "type": "object",
  "required": ["dry_run", "from", "to", "applied", "converted", "unconverted"],
  "properties": {
    "dry_run": { "type": "boolean" },
    "from": { "type": "integer", "minimum": 0, "description": "The step state.json recorded (Effects step 1)." },
    "to": { "type": "integer", "minimum": 0, "description": "This binary's latest step. state.json records it (with dry_run, would record it) unless state.json is listed in unconverted, which holds its number, or a rebuild of it was held by a sesshin.json in another format (listed in unconverted)." },
    "applied": {
      "type": "array",
      "description": "The steps after from, up to to; empty when nothing was pending.",
      "items": {
        "type": "object",
        "required": ["step", "name"],
        "properties": {
          "step": { "type": "integer", "minimum": 1 },
          "name": { "type": "string", "description": "The step's name, e.g. extra." }
        }
      }
    },
    "converted": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["session_id", "id", "files"],
        "properties": {
          "session_id": { "type": "string" },
          "id": { "type": ["integer", "null"], "description": "Its sesshin ID, when sesshin.json could be read, before or after conversion." },
          "files": { "type": "array", "items": { "type": "string" }, "description": "The file names converted, e.g. sesshin.json." }
        }
      }
    },
    "unconverted": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["path", "detail"],
        "properties": {
          "path": { "type": "string" },
          "detail": { "type": "string", "description": "Human-readable: why the steps couldn't convert it." }
        }
      }
    }
  }
}
```

**Order:** `applied` by step; `converted` by `session_id`, and each `files` in the order [State directory layout](design-spec.md#state-directory-layout) lists them; `unconverted` by `path`.

**Errors,** in this order:

| Kind | When |
|---|---|
| `invalid-input` | A bad field. |
| `environment` | `HOME` is unusable. |
| `unsupported-format` | `state.json` is in a newer format, or records a step past this binary's latest: at step 1, or again at step 3. Nothing was converted when raised at step 1. |
| `busy` | A session lock (`lock`: `session`) or the state lock (`lock`: `state`) was held past the wait. Sessions already converted stay converted; `state.json` still records `from`. |

`io` from a file that can't be read, or a write that fails, stops the run the same way.

**Warnings:**

| Kind | When |
|---|---|
| `unusable-file` | A file the steps couldn't convert (`reason`: `unsupported-format`), also listed in `unconverted`; or a session file in a newer format, left alone. |

**Retry safety:**

- After anything, a crash included: safe. Each file is written once, atomically; a file already converted is left alone; and `state.json` records the latest only once every session is done. A rerun converts what is left, and one with nothing pending changes nothing.
