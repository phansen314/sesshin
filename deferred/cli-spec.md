# Deferred: cli-spec.md

The commands still deferred from [cli-spec.md](../cli-spec.md): `focus`, `doctor`, `repair`, `info`, the pickers `jump` and `watch`, and the planned `wait` and `update`. Each runs the operation of the same name in this folder's [operations.md](operations.md). Bring them back as [README](README.md#bringing-a-command-back) says.

The text is as it stood when the scope was cut, minus what has since come back to the main spec or been dropped. The global rules (output, input, selectors, exit codes) are the main spec's.

---

## Commands

### focus

Bring a live session's window to the front. This is the non-interactive core of `jump`. Runs [`focus`](operations.md#focus).

**Synopsis:** `sesshin focus <session>`, or `sesshin focus -i <file>`.

**Operation:** [`focus`](operations.md#focus).

**Arguments:**

| Argument | Field | Notes |
|---|---|---|
| `<session>` | `/session` | Required unless `--input` is given. A [selector](../cli-spec.md#selectors-on-the-command-line), among live sessions. |

**Options:** none.

**Input:** none beyond the Arguments mapping.

**Output:** Passthrough.

**Errors:** none beyond the operation's.

**Examples:**

```sh
sesshin focus api
sesshin focus 12
```

### info

Report where sesshin keeps things on this machine, the effective config, the terminal sesshin recognizes here, and what `install` recorded. Runs [`info`](operations.md#info).

**Synopsis:** `sesshin info`, or `sesshin info -i <file>`.

**Operation:** [`info`](operations.md#info).

**Arguments:** none.

**Options:** none.

**Input:** none. With `--input`, the only valid input is `{}`.

**Output:** Passthrough. A corrupt config is reported as state (`config.status: "corrupt"`), so it exits `0`.

**Errors:** none beyond the operation's.

**Examples:**

```sh
sesshin info | jq .result.locations
sesshin info | jq -e '.result.terminal == "kitty"'   # can spawn, send, and focus from here
```

### doctor

Report everything wrong with sesshin on this machine, and what `repair` would do about each problem. Changes nothing. Runs [`doctor`](operations.md#doctor).

**Synopsis:** `sesshin doctor [--kinds <kinds>]`, or `sesshin doctor -i <file>`.

**Operation:** [`doctor`](operations.md#doctor).

**Arguments:** none.

**Options:**

| Option | Field | Default |
|---|---|---|
| `--kinds <kinds>` | `/kinds` | None: every kind but the informational ones. Comma list of [finding kinds](operations.md#finding-kinds). |

**Input:** none beyond the Options mapping.

**Output:** Passthrough. Findings are reported as state (`ok: true`, `healthy: false`), so it exits `0`. To turn health into an exit status, use `jq -e .result.healthy`.

**Errors:** none beyond the operation's.

**Examples:**

```sh
sesshin doctor                       # healthy when .result.healthy is true
sesshin doctor --kinds hook-errors   # the hook log's recent entries: listed only when asked for
sesshin doctor | jq '.result.findings[] | select(.class == "manual") | .suggestion'
```

### repair

Apply the repairs that are safe, then report what is left. Runs [`repair`](operations.md#repair).

**Synopsis:** `sesshin repair [--kinds <kinds>] [--dry-run]`, or `sesshin repair -i <file>`.

**Operation:** [`repair`](operations.md#repair).

**Arguments:** none.

**Options:**

| Option | Field | Default |
|---|---|---|
| `--kinds <kinds>` | `/kinds` | None: every *auto* kind. Comma list. Naming `incomplete-session` is the only way to repair it. |
| `--dry-run` | `/dry_run` | `false`. |

**Input:** none beyond the Options mapping.

**Output:** Passthrough. Findings left for a person don't fail the command: it exits `0`.

**Errors:** none beyond the operation's.

**Examples:**

```sh
sesshin repair                          # every auto repair
sesshin repair --kinds incomplete-session   # remove session directories with no lifecycle.json; never done by default
```

As in koan, there is no `doctor --fix`. Repairing is its own command, so that agent permission rules, which match a command line by its start, can ask before `sesshin repair`.

## Pickers

The pickers are commands for people at a terminal. They belong in [picker-spec.md](../picker-spec.md), beside `restart`, which says where pickers depart from the CLI's global rules: they draw on `/dev/tty`, and they compose operations. Neither is specified beyond this table.

| Command | What it does | Built on |
|---|---|---|
| `sesshin jump` | Pick a live session and focus its window. | [`list`](../operations.md#list), [`focus`](operations.md#focus) |
| `sesshin watch` | A live, refreshing picker of every session, to jump from. | [`list`](../operations.md#list), [`focus`](operations.md#focus) |


## Planned commands

### wait

Block until a session reaches a state or a timeout passes. Runs the planned [`wait`](operations.md#wait) operation. Its CLI follows once the operation is specified. The expected shape is `sesshin wait <session> --until <states> [--timeout-secs <n>]`, so that an agent can `send` a prompt and then `wait --until waiting`.

### update

Change a session's user-owned `extra`, live or ended. Runs the planned [`update`](operations.md#update) operation. Bringing it back also brings back cli-spec's rules for nested option names (`/extra/merge` is `--extra-merge`) and repeatable list options (`--extra-remove status --extra-remove owner`, each occurrence one item, so `--extra-remove 'a,b'` names the key `a,b`).

**Synopsis:** `sesshin update <session> (--extra-replace-all <json> | [--extra-merge <json>] [--extra-remove <key>]…)`, or `sesshin update -i <file>`.

**Operation:** [`update`](operations.md#update).

**Arguments:**

| Argument | Field | Notes |
|---|---|---|
| `<session>` | `/session` | Required unless `--input` is given. A [selector](../cli-spec.md#selectors-on-the-command-line), among all sessions: a job selects the live session holding it, else the one last seen. |

**Options:**

| Option | Field | Default |
|---|---|---|
| `--extra-merge <json>` | `/extra/merge` | Unchanged. A JSON object: each key set to its value. |
| `--extra-remove <key>` | `/extra/remove` | Unchanged. **Repeatable**, one key per occurrence. |
| `--extra-replace-all <json>` | `/extra/replace_all` | Unchanged. A JSON object; `{}` clears `extra`. |

**Input:** with none of the three options, the input's `extra` is empty, which the operation refuses (`invalid-input`, `/extra`). `--extra-replace-all` with either of the others is likewise the operation's `invalid-input`, not a usage error: an input can be built from it.

**Output:** Passthrough. `result.changed` is `["extra"]`, or `[]` when the value was already so.

**Errors:** none beyond the operation's.

**Examples:**

```sh
sesshin update 12 --extra-merge '{"koan-task": 57}'
sesshin update api --extra-remove shingi-unit --extra-remove koan-task
sesshin update 0b6c5a3e --extra-replace-all '{}'
sesshin update 12 --extra-merge '{"status":"review"}' | jq .result.session.extra
```

A session too new to have its `sesshin.json` fails `conflict` (`no-sesshin-file`); its message says whether to retry after the session's next prompt or `resume` it first:

```sh
sesshin update 0b6c5a3e --extra-merge '{"koan-task": 57}' | jq -r '.error.details | "\(.rule) \(.file)"'
```

## Not included

These items of the main spec's [Not included](../cli-spec.md#not-included) wait on the pickers:

- **A human-readable mode** for `list` or `show`. People get `watch` and `jump`, and anything else goes through `jq`. herd's `ls` printed a table, and its scripts scraped it.
- **Shell completion.** Completing a `<session>` means a full read of `sessions/` on every Tab. It is deferred until the pickers show whether it's needed. herd's `complete` and `tcomplete` commands are not carried over.
