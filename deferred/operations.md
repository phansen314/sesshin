# Deferred: operations.md

The operations still deferred from [operations.md](../operations.md): the diagnostic [`doctor`](#doctor) and [`repair`](#repair), [`info`](#info), and the planned [`update`](#update), with the kinds and findings only they use. Bring them back as [README](README.md#bringing-a-command-back) says.

The text is as it stood when the scope was cut, minus what has since come back to the main spec or been dropped. Every shared rule they use (selectors, the session view, the error and warning kinds) is now in the main spec; check each operation against it before merging.

---

## Operation kinds

- ***diagnostic*** — Finds, and repairs, what a crash or an outside change left: [`doctor`](#doctor) and [`repair`](#repair). `doctor` changes nothing and takes no lock; `repair` takes the state lock, and tries each session's lock before touching that session. Neither mistakes a write in progress for a leftover: leftovers are judged by age (see [Files](../design-spec.md#files)).

## Errors

The deferred operations add these to the main spec's [error kinds](../operations.md#error-kinds):

| Kind | Addition |
|---|---|
| `conflict` | `rule`: `duplicate-id` (the sesshin ID names several sessions; a write must know which one it acts on). For [`update`](#update): `no-sesshin-file` (the session has no usable `sesshin.json`; also `path`: the file, and `file`: `missing` or `unusable`), and `extra-too-large` (the result would break `extra`'s [limits](../design-spec.md#user-owned-extra)). |
| `busy` | For [`update`](#update): `lock`: `session`. |

## Findings

A finding is one problem [`doctor`](#doctor) found, and what [`repair`](#repair) would do about it.

### Finding kinds

| Kind | Class | What it is | What `repair` does |
|---|---|---|---|
| `temp-leftover` | auto | A hidden temp file sesshin left in a session directory or `sessions/`, from a crash between writing and renaming, more than 60 seconds old. | Removes it. |
| `prune-leftover` | auto | A hidden directory in `sessions/` left by a [prune](../design-spec.md#retention) interrupted between its rename and its removal. | Removes it. |
| `stale-reservation` | auto | A reservation that is [stale](../design-spec.md#reservations): never launched within `stranded_secs`, its launched window gone, or more than a day old. | Removes it. |
| `id-above-last-id` | auto | A session's sesshin ID is above `last_id`, so the next ID issued would repeat it. | Raises `last_id` to the highest ID found. |
| `state-missing` | auto | `state.json` is missing while sessions exist. | Writes it with `last_id` = the highest sesshin ID found, as the next hook that issues an ID would. IDs pruned since that one may be issued again (see [Sesshin IDs](../design-spec.md#sesshin-ids)). |
| `incomplete-session` | on-request | A session directory with no `lifecycle.json`, more than 60 seconds old. | Removes the directory, if its session lock is free. |
| `duplicate-id` | manual | Several sessions share a sesshin ID. | Nothing; `doctor` names them. |
| `unusable-file` | manual | A session file is unusable. Its session's next hook rewrites it; an ended session's never will. | Nothing; suggests [`prune`](../operations.md#prune). |
| `state-unusable` | auto | `state.json` is corrupt or in an older format. | Rewrites it as for `state-missing`, keeping what can be read. |
| `state-newer-format` | manual | `state.json` is in a format newer than this binary's, so this binary issues no IDs. | Nothing; suggests upgrading sesshin and running [`install`](../operations.md#install). |
| `config-corrupt` | manual | The config is corrupt; hooks are running with defaults. | Nothing. |
| `hooks-not-wired` | manual | One or more of sesshin's [registrations](../hooks-spec.md#registration) is missing from `settings.json`, or points at a binary that doesn't exist. | Nothing; suggests [`install`](../operations.md#install). |
| `status-line-not-sesshin` | manual | Claude Code's `statusLine` runs something other than sesshin, so no metrics are recorded. | Nothing; suggests [`install`](../operations.md#install), whose proposal replaces it. |
| `terminal-unavailable` | manual | Inside a known terminal whose remote control is off (for kitty, `KITTY_WINDOW_ID` set without `KITTY_LISTEN_ON`). | Nothing; says what to enable. |
| `hooks-binary-mismatch` | manual | The binary sesshin's registrations in `settings.json` run is older than this one, or gone, so hooks and CLI may disagree about file formats. | Nothing; suggests running [`install`](../operations.md#install) again. |
| `location-mismatch` | manual | This process resolves a different config directory, state directory, or Claude Code settings file than `install` recorded, so it may be reading other state than the hooks write, or checking a `settings.json` Claude doesn't read (an XDG variable or `CLAUDE_CONFIG_DIR` set differently under cron, ssh, or an IDE). | Nothing; names both. |
| `hook-errors` | informational | The hook log has entries in the last 24 hours. | Nothing; `doctor` lists the last ten when asked. |

The classes are koan's: ***auto*** findings are repaired whenever `repair` runs, unless it is given other kinds; ***on-request*** only when named; ***manual*** never; ***informational*** findings are reported only when named, and never make the state unhealthy.

### Finding schema

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "finding",
  "type": "object",
  "required": ["kind", "class", "message", "details", "suggestion"],
  "properties": {
    "kind": { "type": "string" },
    "class": { "enum": ["auto", "on-request", "manual", "informational"] },
    "message": { "type": "string" },
    "details": { "type": "object", "description": "Per kind: `path`, `paths`, `id`, `sessions`, `events`, as applicable." },
    "suggestion": { "type": ["string", "null"], "description": "A command that would resolve it, for manual findings; null otherwise." }
  },
  "additionalProperties": false
}
```

## Session operations

## Diagnostic operations

### doctor

Find what is wrong with sesshin on this machine — the state directory, the config, the wiring into Claude Code, the terminal — and say what would fix it.

**Kind:** diagnostic. Takes no lock.

**Input schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "doctor-input",
  "type": "object",
  "properties": {
    "kinds": { "type": "array", "items": { "type": "string" }, "uniqueItems": true, "description": "Only these finding kinds; informational kinds are reported only when named." }
  },
  "additionalProperties": false
}
```

**Additional validation:** each of `kinds` is a [finding kind](#finding-kinds).

**Preconditions:** none. A missing state directory, a corrupt config, and an unusable `state.json` are findings, not errors: `doctor` is how you find out about them.

**Effects:** none.

**Output schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "doctor-output",
  "type": "object",
  "required": ["healthy", "findings"],
  "properties": {
    "healthy": { "type": "boolean", "description": "No finding other than informational ones." },
    "findings": { "type": "array", "items": { "$ref": "finding" } }
  },
  "additionalProperties": false
}
```

**Order:** by the [finding kinds](#finding-kinds) table's order, then by path.

**Errors:** `invalid-input`, `environment`.

**Warnings:** none: everything it finds is a finding.

**Retry safety:** safe.

### repair

Fix what `doctor` finds that is safe to fix.

**Kind:** diagnostic. Takes the state lock.

**Input schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "repair-input",
  "type": "object",
  "properties": {
    "kinds": { "type": "array", "items": { "type": "string" }, "uniqueItems": true, "description": "Only these kinds; default: every auto kind. On-request kinds are repaired only when named." },
    "dry_run": { "type": "boolean", "default": false }
  },
  "additionalProperties": false
}
```

**Additional validation:** each of `kinds` is an auto or on-request finding kind.

**Preconditions:** none.

**Effects:** each finding of the chosen kinds is repaired as the [finding kinds](#finding-kinds) table says. A session directory is removed only by renaming it aside first, as [`prune`](../operations.md#prune) does.

**Output schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "repair-output",
  "type": "object",
  "required": ["dry_run", "repaired", "remaining"],
  "properties": {
    "dry_run": { "type": "boolean" },
    "repaired": { "type": "array", "items": { "$ref": "finding" } },
    "remaining": { "type": "array", "items": { "$ref": "finding" }, "description": "What doctor would still report." }
  },
  "additionalProperties": false
}
```

**Errors:** `invalid-input`, `environment`, `busy`.

**Warnings:** none.

**Retry safety:** safe; running it again repairs nothing new.

---

### info

Report where sesshin keeps things on this machine, the effective config, and how much it has recorded — enough to orient without walking every session.

**Kind:** read. Takes no lock.

**Input schema:** an empty object.

**Additional validation:** none.

**Preconditions:** none. Unlike other reads, a corrupt config is reported here, not an error.

**Effects:** none.

**Output schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "info-output",
  "type": "object",
  "required": ["locations", "config", "state", "terminal", "installed"],
  "properties": {
    "locations": {
      "type": "object",
      "required": ["config_dir", "state_dir", "claude_settings"],
      "properties": {
        "config_dir": { "type": "string" },
        "state_dir": { "type": "string" },
        "claude_settings": { "type": "string" }
      }
    },
    "config": {
      "type": "object",
      "required": ["status", "values"],
      "properties": {
        "status": { "enum": ["absent", "ok", "corrupt"] },
        "values": { "type": "object", "description": "Every key, with its effective value (defaults filled in)." }
      }
    },
    "state": {
      "type": "object",
      "required": ["exists", "last_id", "sessions", "reservations"],
      "properties": {
        "exists": { "type": "boolean" },
        "last_id": { "type": ["integer", "null"] },
        "sessions": { "type": "integer", "description": "Session directories, live and ended; liveness isn't checked." },
        "reservations": { "type": "integer" }
      }
    },
    "terminal": { "type": ["string", "null"], "description": "The backend that recognizes the caller's environment, if any." },
    "installed": { "type": ["object", "null"], "description": "install.json's contents (design-spec install.json), or null." }
  },
  "additionalProperties": false
}
```

**Errors:** `environment`.

**Warnings:** none.

**Retry safety:** safe.

## Planned operations

### update

Planned and fully specified; deferred until something needs to change `extra` after a session starts ([User-owned extra](../design-spec.md#user-owned-extra)). Bringing it back also brings back: `update` among the operations that take a [selector](../operations.md#selecting-a-session), selecting among all sessions as `show` does; the `conflict` rules and `busy` lock above; and in design-spec [Locks](../design-spec.md#locks), `update` as the one operation that holds a session lock (never the state lock), waiting up to 500 ms for it. `extra`'s field table then names `update` as its writer once the file exists.

Change a session's user-owned [`extra`](../design-spec.md#user-owned-extra), live or ended: replace it, or set and remove keys. The one operation that writes a session file.

**Kind:** write. Takes the session's lock, waiting up to 500 ms ([Locks](../design-spec.md#locks)); never the state lock.

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

**Additional validation:** `extra.merge` and `extra.remove` share no key. `replace_all` and `merge` are each within `extra`'s [limits](../design-spec.md#user-owned-extra), and their numbers are exempt from the integer-literal rule, kept as given. With these rules, the order in which `merge` and `remove` apply doesn't matter.

**Preconditions:** `session` selects one session ([Selecting a session](../operations.md#selecting-a-session)), whatever its liveness. It has a usable `sesshin.json`: one whose `id` is still `null` counts.

**Effects:**

1. **Select** the session, reading every session as [`list`](../operations.md#list) does. A session without a usable `sesshin.json` has no sesshin ID and no job, so only its UUID or a prefix selects it.
2. **Lock** its directory, waiting up to 500 ms (else `busy`, `lock`: `session`). Once locked, check that the path still names the directory that was locked, as a hook does ([Recording an event](../hooks-spec.md#recording-an-event)): if a [`prune`](../operations.md#prune) renamed it aside meanwhile, or it is gone, fail `not-found`.
3. **Read** `sesshin.json` again, under the lock: this read, not step 1's, decides.
   - **There, but not readable** (a permission denied, an I/O error, a directory in its place): fail `io`.
   - **Missing, unusable, or in another format:** fail `conflict` (`rule`: `no-sesshin-file`, `file`: `missing` or `unusable`). `update` never creates `sesshin.json`: creating it issues a sesshin ID and decides the job, which only a hook does ([Creating `sesshin.json`](../hooks-spec.md#creating-sesshinjson)).
4. **Change `extra`:**
   - `replace_all`: it becomes exactly the given object.
   - `merge`: each given key is set to the given value, replacing any earlier value whole (no recursive merge into objects); `null` is an ordinary value, not a deletion.
   - `remove`: each given key is deleted; a key that isn't there changes nothing.

   Existing keys keep their position, and new keys are appended in the order given ([File format](../design-spec.md#file-format)). A result past `extra`'s limits fails `conflict` (`rule`: `extra-too-large`), and nothing is written.
5. **Write** `sesshin.json` (temp file, rename), every key but `extra` as step 3 read it, unless `extra` is unchanged: equal *as a JSON value* to what was stored (objects regardless of key order, numbers by numeric value). Then the file isn't rewritten, and its layout is untouched.
6. **Unlock,** then read the session, with no lock, for the output.

`update` changes `extra` only. `id`, `job`, `source`, and `placement` belong to the hooks ([`sesshin.json`](../design-spec.md#sesshinjson)); `lifecycle.json`'s clocks don't move, since nothing happened in the session.

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
| `unusable` | `live` or `unknown` | The file is corrupt, or from another sesshin build ([Format versions](../design-spec.md#format-versions)). | Retry after its next prompt: that hook writes it afresh, with a new sesshin ID and `extra` from `SESSHIN_EXTRA`. |
| `unusable` | `ended` | As above, and no hook of an ended session will rewrite it. | `sesshin resume <uuid>`, then retry. |

For example: `session 0b6c5a3e has no sesshin.json yet (it is live, and its first hook hasn't written one): retry after its next prompt`.

**Warnings:**

| Kind | When |
|---|---|
| `unusable-file` | A session file read while selecting, or for the output, couldn't be used, as [`list`](../operations.md#list) reports it. |

**Retry safety:** safe. Every form is idempotent: run again with the same input, it leaves `extra` as it is and returns `changed: []`. After `busy`, retry at once. After `conflict` (`no-sesshin-file`), retry as the table above says. A crash leaves `sesshin.json` old or new, never part of either.

**Implementation notes,** for implementation-spec's [Extra](../implementation-spec.md#extra) when it comes back: `ops.Update(in, env)` mirrors koan's `update` for `extra` (koan `internal/ops/update.go`), with `env` a `ReadEnv`.

- **The edit:** `replace_all` sets the object; `merge` calls `Object.Set` per key in input order (existing keys keep their position, new ones append); `remove` calls `Object.Delete`.
- **Equality** as koan's (`internal/jsonio/equal.go`, brought over into sesshin's `jsonio`): objects regardless of key order, arrays in order, numbers compared exactly with `math/big.Rat` (`SetString` parses decimal and exponent forms), never `float64`. An equal result writes nothing.
- **The lock:** the session directory through `fsys`, `Lock(500ms)`, then the same identity check a hook makes after locking (the path still names the directory locked, else `not-found`). The re-read under the lock decides `no-sesshin-file`: `fsys`'s not-exist is `missing`; a read that succeeds but fails validation (or another `schema`) is `unusable`; any other read error is `io`.
- **The message** for `no-sesshin-file` is built from `file` and the session's liveness, as the [table](#update) gives it, naming the session by its UUID's first 8 characters and, for an ended one, the `sesshin resume <uuid>` to run.
- **The CLI.** `update`'s table has the `session` argument, `--extra-merge` and `--extra-replace-all` (JSON values, as koan's: `json.Valid`, then the strict reader; a bad token is `invalid-input` at its field), and `--extra-remove` (repeatable, one key each). `spawn` gains `--extra`. With none of the three on `update`, the CLI builds `{"session": …, "extra": {}}`, and the operation reports the empty `extra` as `invalid-input`.
- **Tests,** in `internal/ops/update_test.go`, over `spawn`'s fixture: every input check and the schema's agreement (both `oneOf` forms, a shared key, empties, the limits counted in bytes and depth); each form's effect and key order; `changed` empty with no rewrite (the file's mtime and bytes unchanged) for an equal result, including `1.0` against `1` and keys reordered; numbers round-tripped (`1.10`, `-0`, `1e400`, a 20-digit integer); `extra-too-large` from a `merge`; the Errors table's order; `no-sesshin-file` for each row of the table (missing and unusable, live and ended) with its `file`, `path`, and message; a file that turns unusable between selection and the lock; a directory pruned while waiting (`not-found`); `busy` with the lock held; `io` for an unreadable file; a pending `id` updated; every other key of `sesshin.json` untouched. `internal/record` tests: `SESSHIN_EXTRA` copied on a fresh write, `{}` when unset, malformed, not an object, or past a limit (logged only by `session-start`), none for a nested session, and kept on completion. `e2e`: `sesshin spawn --extra` (with the fake `kitten` recording `--env`), a session made by `sesshin-hook` with `SESSHIN_EXTRA` set, then `sesshin update`, `sesshin list --fields extra`, and a resume, `extra` surviving each.
