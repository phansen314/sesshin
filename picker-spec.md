# sesshin picker spec

sesshin's pickers: commands for a person at a terminal, built on [fzf](https://github.com/junegunn/fzf). There is one, [`restart`](#restart). They are specified on top of the [CLI spec](cli-spec.md) and the [operations](operations.md), and run no operation of their own: they compose [`list`](operations.md#list) for what they show with the operations they run on the selection, each as its own call. Everything the CLI spec says holds for a picker except where this document says otherwise; those places are collected in [Departures from the CLI spec](#departures-from-the-cli-spec).

They follow koan's [pick spec](https://github.com/phansen314/koan/blob/main/pick-spec.md) where they overlap: the fzf version check, the options undone, the error kinds, and the `actions` report. They are much smaller: one fzf run, no keys that act inside it, no callbacks into sesshin.

## Goals

- **Bring back what a reboot took.** Pick the sessions that were running, in one fzf, and have each reopened in its own tab, in its own directory, under its own tab title and job.
- **Nothing hidden from a caller.** Every operation a picker runs is reported in its output, failures included.

## Non-goals

- **Agents.** A picker is for a person at a terminal, and fails without one (see [Errors](#errors)). Agents use [`resume`](operations.md#resume), which is what `restart` runs.
- **Acting inside the picker.** No key does more than move, mark, and accept. fzf ends, then the picker acts.
- **A configurable keymap.** fzf's own options restyle the picker (see [fzf options](#fzf-options)).

## Requirements

- **A terminal.** `/dev/tty` must open for reading and writing. stdin and stdout may be anything.
- **fzf** on `PATH`, version 0.63.0 or later, checked as koan's [pick spec](https://github.com/phansen314/koan/blob/main/pick-spec.md#requirements) checks it: `fzf --version` without `FZF_DEFAULT_OPTS` and `FZF_DEFAULT_OPTS_FILE` in its environment, the first word without any `-` suffix, compared as three numbers. 0.63.0 is koan's minimum, so one fzf serves both.
- **A terminal backend,** for `restart`: the caller runs where [`resume`](operations.md#resume) can open tabs (kitty with remote control, outside tmux and screen). Checked before fzf starts, so a selection is never made only to fail.

## restart

Pick ended sessions, and resume each in a new tab of the caller's terminal. Runs [`list`](operations.md#list) once for the candidates, then [`resume`](operations.md#resume) once per pick.

**Synopsis:** `sesshin restart [--query <text>] [-- <claude args…>]`, or `sesshin restart -i <file>`.

**Arguments:**

| Argument | Field | Notes |
|---|---|---|
| `<claude args…>` after `--` | `/args` | Optional. Passed to every `resume`, as its `args`: `claude --resume` restores the conversation, not the flags it was started with. |

**Options:**

| Option | Field | Default |
|---|---|---|
| `--query <text>` | `/query` | `""`. The initial search text, e.g. `--query killed`. |

**Input:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "restart-input",
  "type": "object",
  "properties": {
    "query": { "type": "string", "default": "" },
    "args": { "$ref": "resume-input#/properties/args" }
  },
  "additionalProperties": false
}
```

**Additional validation:** as [`resume`](operations.md#resume)'s for `args`.

**Steps:**

1. **Check** the input, `HOME`, `config.toml`, fzf, and the terminal backend, in the [Errors](#errors) order.
2. **Load** the candidates: one `list`, with `liveness` `ended`. Its error, if any, is passed through, and fzf never opens. [Headless](design-spec.md#terms) sessions are left out, as `list` leaves them out by default: no one resumes an agent's `claude -p`.
3. **No candidates:** return at once, with `actions` empty and fzf never opened.
4. **Pick** in fzf, with every candidate as a [line](#lines), multi-select on, and the [preview](#preview). See [Outcomes](#outcomes).
5. **Resume** each pick, in line order (not the order they were marked in), as `resume` with `{"session": <uuid>, "args": <args>, "start_timeout_secs": 0}`, recording each in `actions`. A failure doesn't stop the rest: two picks storing one job resume the first under it, and the second fails `conflict` (`job-taken`), and is resumed by hand with `sesshin resume <id> --job <other>`. No `resume` waits: each tab opens at once, in the background (`--keep-focus`), and a `claude --resume` that can't start shows its error in its own tab.

### Lines

One line per candidate, in [session order](operations.md#session-order) (most recently seen first, so after a reboot the sessions it took are at the top), tab-delimited, starting with a hidden **key**, the session's UUID. fzf shows and searches the rest; only the key identifies a line.

```
#12  api   killed                  3m ago  ~/code/api      api review
#9   —     exited                  2h ago  ~/code/sesshin  spec resume
#7   docs  cleared                 1d ago  ~/notes         #7
#4   —     killed transcript-gone  6d ago  /tmp/x          4d1c9e0a
```

| Column | Content |
|---|---|
| ID | `#<id>`, or `—` without one. |
| Job | The job the session reports, or `—`. |
| End | How it ended, as a word fzf can match (below), then `transcript-gone` when `transcript_exists` is `false`. |
| Seen | Its [last seen](design-spec.md#liveness), relative: `<n>s`, `<n>m`, `<n>h`, `<n>d ago`. |
| cwd | Its `cwd`, with the home directory as `~`; `—` when `null`. |
| Name | Its [name](design-spec.md#terms). |

The **End** word, from `end_reason` and `ended_at`:

| Word | When |
|---|---|
| `killed` | No `SessionEnd` was recorded (`ended_at` `null`, `end_reason` not `superseded`), or its reason is `other`. A signal ends Claude with `other`: a reboot, a closed tab, or a `kill` ([verified](design-spec.md#claude-code-21289)). A `SessionEnd` that never ran, after a `kill -9` or a crash, leaves none. |
| `exited` | `prompt_input_exit`: `/exit`, ctrl-c twice, or ctrl-d twice ([verified](design-spec.md#claude-code-21289)). |
| `cleared` | `clear`. |
| `resumed` | `resume`: an in-session `/resume` switched away from it. |
| `superseded` | `superseded` (derived): another session took over its process. |
| `logout` | `logout`. |
| *the reason* | Any other `end_reason`, as stored, so an [open set](design-spec.md#open-sets) value is still searchable. |
| `ended` | `ended_at` is set, but no reason was stored (one that failed its guard). |

The words annotate and never withhold. A session sesshin guesses you didn't want back is exactly the one you regret clearing. Typing `killed` and pressing ctrl-a picks every session a reboot took, and a few tabs closed by hand, which sit further down by age.

Columns are padded to their widest value. Every field is scrubbed of tabs, newlines, and other control characters (each replaced by a space) before it is written, so a title can't split a line or forge a key.

### Preview

The session's details, one per line: sesshin ID, name, job, `cwd`, `git_branch`, `model`, `permission_mode`, `started_at`, last seen, `ended_at`, `end_reason`, compactions, cost, `transcript_path` and whether it exists, and the placement's tab title. Written by `restart` before fzf starts, one file per candidate, named by its key, in a private temp directory (mode `0700`, under `$XDG_RUNTIME_DIR` if set, else the system temp directory), removed when `restart` exits. fzf's preview command is `cat -- <dir>/{1}`, with the directory quoted for `sh`, and fzf quoting `{1}`. So the preview needs no call back into sesshin, and a key, a UUID, is safe as a file name.

### Outcomes

fzf's stdout is the selection. `restart` undoes the options that would change it (see [fzf options](#fzf-options)), and reads it as lines, taking each line's key. A key it didn't offer is ignored.

| fzf | Outcome |
|---|---|
| Enter, exit `0` | Resume the marked lines, or, with none marked, the line under the cursor. |
| Enter with nothing matching, exit `1` | Nothing picked: `ok: true`, `actions` empty. |
| Esc or ctrl-c, exit `130` | `cancelled`: nothing was resumed. |
| Any other exit | `unavailable` (`fzf-failed`). |

**Keys:** fzf's own, with `--multi`: tab and shift-tab mark, Enter accepts. `restart` adds `ctrl-a:select-all`, which marks every line the query matches.

**Output schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "restart-output",
  "type": "object",
  "required": ["actions"],
  "properties": {
    "actions": {
      "type": "array",
      "description": "One per pick, in line order. Empty when nothing was picked or there were no candidates.",
      "items": {
        "type": "object",
        "required": ["operation", "input", "output"],
        "properties": {
          "operation": { "const": "resume" },
          "input": { "$ref": "resume-input", "description": "As passed." },
          "output": { "$ref": "envelope", "description": "resume's envelope, unchanged: success or failure." }
        },
        "additionalProperties": false
      }
    }
  },
  "additionalProperties": false
}
```

`jq '.result.actions[] | select(.output.ok | not)'` finds the failures. The envelope's `warnings` are the load's; each `resume`'s own warnings stay in its `output`. `restart` exits `0` when it ran, whatever its `resume`s did.

## Errors

The pickers add two CLI-only error kinds to [`usage`](cli-spec.md#usage-errors), as koan's `pick` does. No operation raises them.

| Kind | When | `details` |
|---|---|---|
| `unavailable` | The picker can't run: no terminal, or no usable fzf. | `reason`: `no-terminal` (`/dev/tty` doesn't open), `fzf-missing`, `fzf-too-old` (with `found` and `required`), or `fzf-failed` (with `status`, fzf's exit status, if it exited: a bad option in `FZF_DEFAULT_OPTS`, say). |
| `cancelled` | The person pressed Esc or ctrl-c. Nothing was resumed. | none (`{}`). |

`restart`'s, in this order: `invalid-input`; `environment`; `corrupt` (`config.toml`); `unavailable` (`fzf-missing`, `fzf-too-old`, `fzf-failed` from `fzf --version`); `terminal` (`unavailable`), as `resume` would raise it; the load's errors; `unavailable` (`no-terminal`), only when there are candidates; then fzf's outcome. Once fzf has accepted, `restart` raises nothing more: each `resume`'s failure is in its `actions` entry.

**stderr** follows the CLI spec's one-line rule, except that fzf's own stderr (a message about a bad option) passes through to the terminal.

**Signals.** While fzf runs, `restart` catches SIGINT and SIGQUIT and discards them: ctrl-c is fzf's key, and cancels. Any other signal, or SIGINT outside fzf (during the `resume`s), is a crash, as the CLI spec's [Exit codes](cli-spec.md#exit-codes) say, and the tabs already opened stay open, with no report.

## fzf options

- **`FZF_DEFAULT_OPTS`** (and `FZF_DEFAULT_OPTS_FILE`) are honored: colors, layout, borders, history.
- **Options undone.** After `FZF_DEFAULT_OPTS` and before `SESSHIN_PICK_OPTS`, the picker passes `--no-select-1 --no-exit-0 --no-expect --no-tmux --no-read0 --no-header-lines --no-print0 --no-print-query --accept-nth ..`, and its own `--multi`, `--delimiter '\t'`, `--with-nth 2..`, `--with-shell 'sh -c'`, `--preview`, and `--bind ctrl-a:select-all`. The first two would accept or abort without the person; `--expect`, `--print0`, `--print-query`, and `--accept-nth` change what fzf prints, which is the selection; `--read0` and `--header-lines` change what it reads; `--tmux` would run it in a popup the picker's terminal check wasn't made for.
- **`SESSHIN_PICK_OPTS`** is appended last, so it wins: e.g. `SESSHIN_PICK_OPTS='--height 60% --layout reverse'`. It is split as fzf splits `FZF_DEFAULT_OPTS`. One that doesn't split is `fzf-failed`, before fzf runs.
- **Rebinding is at your own risk.** An option that undoes `--multi`, the delimiter, or the fields can break the picker, which doesn't detect it.
- **`FZF_DEFAULT_COMMAND`** is never used: the picker writes every line to fzf's stdin.

## Departures from the CLI spec

- **The intended user is a person** at a terminal, and a picker fails fast without one.
- **Rendering for people,** but only on the terminal. stdout is still one JSON envelope, the same whether it is a terminal, a pipe, or a file.
- **An outside program.** fzf is a runtime dependency of the pickers alone.
- **Two CLI-only error kinds,** `unavailable` and `cancelled`, besides `usage`.
- **Interrupts.** In fzf, ctrl-c cancels with an envelope, and SIGINT is discarded (see [Errors](#errors)).
- **stderr.** fzf's own stderr reaches the terminal.
- **Operations without passthrough.** The `resume`s write nothing to stdout; their envelopes are in `actions`.
- **`SESSHIN_PICK_OPTS`** is an environment override, which the CLI spec's [Not included](cli-spec.md#not-included) otherwise rules out. It only restyles fzf.

## Testing

- **Without fzf.** Lines, End words, preview files, the options passed, and the outcome table are tested with a fake fzf: a script on `PATH` that records its arguments, stdin, and environment, and prints a chosen selection with a chosen exit status. The `resume`s run against a fake launch, as `spawn`'s tests do. Hostile titles (a tab, a newline, a forged UUID) stay on one line under their own key.
- **With fzf, end to end.** One smoke test drives a real fzf in a pseudo-terminal: type a query, ctrl-a, Enter; and Esc. Against 0.63.0 and the current release, as koan's does. `FZF_DEFAULT_OPTS='--select-1 --exit-0 --expect=esc --print-query'` changes nothing.
- **The reboot.** A manual check, as the hooks' verifications are done: sessions in a scratch kitty instance, killed with it, come back with `restart`, `killed` and on top.
