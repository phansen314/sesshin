# Deferred: operations.md

The operations still deferred from [operations.md](../operations.md): [`focus`](#focus), the diagnostic [`doctor`](#doctor) and [`repair`](#repair), [`info`](#info), and the planned [`wait`](#wait), with the kinds and findings only they use. Bring them back as [README](README.md#bringing-a-command-back) says.

The text is as it stood when the scope was cut, minus what has since come back to the main spec or been dropped. Every shared rule they use (selectors, the session view, the error and warning kinds) is now in the main spec; check each operation against it before merging.

---

## Operation kinds

- ***diagnostic*** — Finds, and repairs, what a crash or an outside change left: [`doctor`](#doctor) and [`repair`](#repair). `doctor` changes nothing and takes no lock; `repair` takes the state lock, and tries each session's lock before touching that session. Neither mistakes a write in progress for a leftover: leftovers are judged by age (see [Files](../design-spec.md#files)).

`focus` is a write operation. It acts on the terminal through the session's [terminal backend](../design-spec.md#placement), and fails with `terminal` when it can't.

## Errors

The deferred operations add these to the main spec's [error kinds](../operations.md#error-kinds):

| Kind | Addition |
|---|---|
| `conflict` | `rule`: `duplicate-id` (the sesshin ID names several sessions; a write must know which one it acts on). |
| `terminal` | For `focus`: `unreachable` (the session's window could not be found or reached), `command-failed` (the backend's command failed otherwise). |

## Warnings

The deferred operations add this to the main spec's [warning kinds](../operations.md#warning-kinds):

| Kind | Meaning | `details` |
|---|---|---|
| `placement-repaired` | The session's stored placement had drifted; the operation found the window again and updated `sesshin.json`. | `uuid`; `from`, `to`: placements. |

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

The classes are ftask's: ***auto*** findings are repaired whenever `repair` runs, unless it is given other kinds; ***on-request*** only when named; ***manual*** never; ***informational*** findings are reported only when named, and never make the state unhealthy.

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

### focus

Bring a live session's window to the front: the non-interactive core of `jump`.

**Kind:** write. Takes the session lock, without waiting, only to write a placement repair.

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

**Preconditions:** the session is live and has a placement.

**Effects:**

- The session's window is focused, and its tab and OS window activated, through the backend. It is found as [Placement: verifying and repairing a window](design-spec.md#placement-verifying-and-repairing-a-window) says. When no window can be verified, `focus` falls back to the stored `window_id`: focusing the wrong window is harmless.
- When the window was found somewhere other than the stored placement, `sesshin.json`'s `socket` and `window_id` are repaired, if the session lock is free.

**Output schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "focus-output",
  "type": "object",
  "required": ["session"],
  "properties": {
    "session": { "$ref": "session-ref" }
  },
  "additionalProperties": false
}
```

**Errors:**

| Kind | When |
|---|---|
| `invalid-input` | `session` is missing or empty. |
| `environment`, `corrupt` | `HOME` is unusable, or the config is corrupt. |
| `not-found`, `ambiguous` | `session` matches no live session, or several. |
| `conflict` | `not-live`, `no-placement`, `duplicate-id`. |
| `terminal` | The backend is unavailable, or the window can't be reached or focused. |

A held session lock is not an error: the window is already focused, which is what was asked. The repair is skipped.

**Warnings:**

| Kind | When |
|---|---|
| `placement-repaired` | The window had moved; `sesshin.json` was updated. |

**Retry safety:** safe. Focusing twice is focusing once.

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

### wait

Block until a session reaches a state (`waiting`, `idle`, ended) or a timeout passes. For an agent driving other sessions: [`send`](../operations.md#send) a prompt, then `wait` for the turn to end, then read the transcript. Polls the session's files (no lock, no daemon), using `event_seq` to know when anything changed.
