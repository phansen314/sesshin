# Deferred: cli-spec.md

The commands still deferred from [cli-spec.md](../cli-spec.md): `doctor`, `repair`, `info`, and the picker `watch`. (`update` came back to the main spec.) Each runs the operation of the same name in this folder's [operations.md](operations.md). Bring them back as [README](README.md#bringing-a-command-back) says.

The text is as it stood when the scope was cut, minus what has since come back to the main spec or been dropped. The global rules (output, input, selectors, exit codes) are the main spec's.

---

## Commands

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

The pickers are commands for people at a terminal. They belong in [picker-spec.md](../picker-spec.md), beside `restart` and `jump`. `watch` is specified no further than this table, and `jump`'s preview no further than [design-spec.md](design-spec.md#a-preview-for-jump-and-watch).

| Command | What it does | Built on |
|---|---|---|
| `sesshin watch` | A live, refreshing [`jump`](../picker-spec.md#jump): the same lines in the same order, re-sorted as sessions change and caches expire. | [`list`](../operations.md#list), [`focus`](../operations.md#focus) |


## Planned commands

## Not included

These items of the main spec's [Not included](../cli-spec.md#not-included) wait on the pickers:

- **A human-readable mode** for `list` or `show`. People get `jump` and `watch`, and anything else goes through `jq`. herd's `ls` printed a table, and its scripts scraped it.
- **Shell completion.** Completing a `<session>` means a full read of `sessions/` on every Tab. It is deferred until the pickers show whether it's needed. herd's `complete` and `tcomplete` commands are not carried over.
