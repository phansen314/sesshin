# sesshin picker spec

sesshin's pickers: commands for a person at a terminal, built on [fzf](https://github.com/junegunn/fzf). There are two: [`restart`](#restart) and [`jump`](#jump). They are specified on top of the [CLI spec](cli-spec.md) and the [operations](operations.md), and run no operation of their own: they compose [`list`](operations.md#list) for what they show with the operations they run on the selection, each as its own call. Everything the CLI spec says holds for a picker except where this document says otherwise; those places are collected in [Departures from the CLI spec](#departures-from-the-cli-spec).

They follow koan's [pick spec](https://github.com/phansen314/koan/blob/main/pick-spec.md) where they overlap: the fzf version check, the options undone, the error kinds, and the `actions` report. They are much smaller: one fzf run, no keys that act inside it, no callbacks into sesshin.

## Goals

- **Bring back what a reboot took.** Pick the sessions that were running, in one fzf, and have each reopened in its own tab, in its own directory, under its own tab title and job.
- **Go to the session that wants you.** With many sessions open, find the one blocked on a dialog, or finished and waiting, without walking the tabs: the ones that want you first, next to the prompt where the cursor starts, warm caches first, and fzf's filter for the rest.
- **Nothing hidden from a caller.** Every operation a picker runs is reported in its output, failures included.

## Non-goals

- **Agents.** A picker is for a person at a terminal, and fails without one (see [Errors](#errors)). Agents use [`resume`](operations.md#resume) and [`focus`](operations.md#focus), which are what the pickers run.
- **Acting inside the picker.** No key does more than move, mark, and accept. fzf ends, then the picker acts.
- **A configurable keymap.** fzf's own options restyle the picker (see [fzf options](#fzf-options)).

## Requirements

- **A terminal.** `/dev/tty` must open for reading and writing. stdin and stdout may be anything.
- **fzf** on `PATH`, version 0.63.0 or later, checked as koan's [pick spec](https://github.com/phansen314/koan/blob/main/pick-spec.md#requirements) checks it: `fzf --version` without `FZF_DEFAULT_OPTS` and `FZF_DEFAULT_OPTS_FILE` in its environment, the first word without any `-` suffix, compared as three numbers. 0.63.0 is koan's minimum, so one fzf serves both.
- **A terminal backend,** for `restart` only: the caller runs where [`resume`](operations.md#resume) can open tabs (kitty with remote control, outside tmux and screen). Checked before fzf starts, so a selection is never made only to fail.

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

One line per candidate, in [session order](operations.md#session-order) (most recently seen first, so after a reboot the sessions it took come first, next to the prompt), tab-delimited, starting with a hidden **key**, the session's UUID. fzf shows and searches the rest; only the key identifies a line.

```
#12  api   killed                  3m ago  ~/code/api      api review   ticket=auth-4
#9   —     exited                  2h ago  ~/code/sesshin  spec resume
#7   docs  cleared                 1d ago  ~/notes         #7           ticket=docs-12
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
| Extra | Its [`extra`](design-spec.md#user-owned-extra), as the [Extra column](#extra-column) renders it. |

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

Columns are joined by two spaces, and padded to their widest value, except Name and Extra. Name is padded to the widest name, but to at most 32 columns: a longer one is never cut, and pushes only its own Extra further right, so one long title doesn't move every line's tags. Extra, the last column, is never padded, and a line whose Extra is empty ends at its Name, with no trailing spaces. Every field is scrubbed of tabs, newlines, and other control characters (each replaced by a space) before it is written, so a title can't split a line or forge a key.

### Extra column

A session's [`extra`](design-spec.md#user-owned-extra), as both pickers show it: so typing a tag (`auth-3`) finds the session that worked it. sesshin only displays it: every key, in stored order, none treated specially, and no setting to choose keys (a tool that wants a narrower view filters `list` with `jq`).

One `key=value` pair per top-level key, separated by single spaces:

| Part | Rendered as |
|---|---|
| Key | As stored when it is non-empty and only ASCII letters, digits, `_`, `.`, and `-`; else JSON-quoted (`"two words"`, `""`). |
| String value | As stored when it is non-empty and holds no `=`, no `"`, no control character, and no character Unicode counts as space (U+2028, U+2029, and NBSP among them); else JSON-quoted, so where a value ends is always visible. |
| Number | As written (`extra` keeps numbers' text: `1.10` stays `1.10`). |
| `true`, `false`, `null` | As is. |
| Object or array | Compact JSON (`tags=["db","api"]`). |
| `{}`, or `null` (no usable `sesshin.json`) | Nothing: the column is empty. |

JSON quoting and compact JSON are as the [File format](design-spec.md#file-format) escapes strings, on one line: `<`, `>`, and `&` as themselves. A string renders like the number or literal with the same text (`"57"` and `57` are both `task=57`); `sesshin show` tells them apart.

| `extra` | Column |
|---|---|
| `{}` | (empty) |
| `{"ticket":"auth-3"}` | `ticket=auth-3` |
| `{"ticket":"auth-4","note":"waiting on review"}` | `ticket=auth-4 note="waiting on review"` |
| `{"task":57,"tags":["db","api"]}` | `task=57 tags=["db","api"]` |

- **Nothing can break the line.** A control character or line separator in a key or string quotes it, so a tab shows as `\t` and U+2028 as `\u2028`. The rendered column is then [scrubbed](#lines) as every field is, as a backstop, before the cap below measures it: a control character JSON leaves raw (DEL, C1) has no width until it is a space.
- **Capped at 200 columns** of display width, measured as [jump's lines](#jump-lines) are (every emoji two). A longer rendering is cut on a grapheme boundary, so an emoji keeps its U+FE0F or ZWJ sequence, and ends with `…`. fzf searches only what the line holds: a tag past the cut is found in the [preview](#preview) and `sesshin show`, not by typing it.
- **Past the window's edge.** fzf clips a line wider than its window, and scrolls it sideways to show the match when the query hits text past the edge (its `hscroll`, on by default).
- **No ranking effect.** `restart` breaks ties by session order (`--tiebreak index`), and `jump` doesn't sort: a long Extra never moves a line.

Unicode format characters (a right-to-left override, say) pass through `scrub`, in `extra` as in names: they can garble how a line looks, never split it.

### Preview

The session's details, one per line: sesshin ID, name, job, `cwd`, `git_branch`, `model`, `permission_mode`, `started_at`, last seen, `ended_at`, `end_reason`, compactions, cost, `transcript_path` and whether it exists, and the placement's tab title. Last, `extra:` on its own line, then the whole [`extra`](design-spec.md#user-owned-extra), uncut, as indented JSON as the [File format](design-spec.md#file-format) writes it, with DEL and U+0080–U+009F escaped too (`\u009b`: the 8-bit CSI, which some terminals act on); or, when it is `{}` or `null`, an `extra` row of `—` like any other row's. The pane doesn't scroll sideways or wrap, so a one-line JSON would be clipped. Every other value is scrubbed as the [lines'](#lines) are; the `extra` block isn't, since its line breaks are its own, and no control character is left raw in it. Written by `restart` before fzf starts, one file per candidate, named by its key, in a private temp directory (mode `0700`, under `$XDG_RUNTIME_DIR` if set, else the system temp directory), removed when `restart` exits. fzf's preview command is `cat -- <dir>/{1}`, with the directory quoted for `sh`, and fzf quoting `{1}`. So the preview needs no call back into sesshin, and a key, a UUID, is safe as a file name.

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

## jump

Pick a live session and bring its window to the front, with the sessions that want you first. Runs [`list`](operations.md#list) once for the candidates, then [`focus`](operations.md#focus) on the pick.

**Synopsis:** `sesshin jump [--query <text>]`, or `sesshin jump -i <file>`.

**Arguments:** none.

**Options:**

| Option | Field | Default |
|---|---|---|
| `--query <text>` | `/query` | `""`. The initial search text, in fzf's syntax: `--query "'blocked"` matches `blocked` exactly, where `blocked` alone is fuzzy. |

**Input:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "jump-input",
  "type": "object",
  "properties": {
    "query": { "type": "string", "default": "" }
  },
  "additionalProperties": false
}
```

**Additional validation:** none.

**Steps:**

1. **Check** the input, `HOME`, `config.toml`, and fzf, in the [Errors](#errors) order. There is no terminal backend check: `focus` reaches a session's window from anywhere, and its failure is reported in `actions`.
2. **Load** the candidates: one `list`, with `liveness` `live`, which includes liveness `unknown`. Its error, if any, is passed through, and fzf never opens. [Headless](design-spec.md#terms) sessions are left out, as `list` leaves them out by default.
3. **No candidates:** return at once, with `actions` empty and fzf never opened.
4. **Pick** one in fzf, with every candidate as a [line](#jump-lines), in [jump order](#jump-order). See [Outcomes](#jump-outcomes).
5. **Focus** the pick, as `focus` with `{"session": <uuid>}`, recording it in `actions`.
6. **Show a failure:** after writing the envelope to stdout, so a caller reading it has it whole however long the wait, when the envelope is a failure other than `cancelled` (an `fzf-missing` included: the likeliest failure in the overlay, from kitty's `PATH`), or `focus` failed, and `/dev/tty` opens, write the error's message to `/dev/tty` as one line, and wait for a key, read in raw mode, so ctrl-c is a key too. In the overlay of the key binding below, jump's window closes as it exits, and the envelope with it, so you would otherwise be left where you were with no word of why. Not when `focus` succeeded with `verified` `false`: the focus has already taken you to another window, and an overlay waiting in the one you left would sit there unseen. Not after `cancelled` either: you pressed Esc, and know why.

Every live session is a candidate, whatever it wants: fzf's filter is how you get to the rest (type a job, a directory, or `working`).

**Bound to a key.** jump is meant to be one keystroke away, in an overlay over whichever window you're in, which closes when jump exits: in `kitty.conf`, `map kitty_mod+j launch --type=overlay sesshin jump`. kitty runs it with its own environment, whose `PATH` may lack `sesshin` or `fzf` when kitty was started from a desktop launcher: name `sesshin` by its full path, and make sure kitty's `PATH` has `fzf` (kitty's `env` option sets it).

### Jump order

The candidates are sorted once, when they are loaded: a cache that expires while fzf is open doesn't move its line. By these keys, in order:

1. **Tier,** by [attention](design-spec.md#attention): `blocked`, `stalled`, and `your_turn` first; then `idle`, which wants you only weakly; then `self_waking`, which will resume by itself; then `working`; then `unknown`.
2. **In the first tier, the prompt cache** (`prompt_cache.state`): `warm`, then `cold`, then `unknown` (a `null` `prompt_cache` counts as `unknown`).
3. **Warm:** the earliest `expires_at` first, whatever the session wants. Answering it before then saves the re-cache. So a warm `your_turn` sorts before a cold `blocked`: the blocked one has already lost its cache and costs no more to answer later, while the warm one costs more once it expires.
4. **Within cold, and within unknown:** `blocked`, `stalled`, then `your_turn`. `blocked` and `stalled` by the earliest `last_event_at` first, the longest waiting; `your_turn` by the latest first, so a turn that just ended sorts before the ones you left days ago. Nothing is acknowledged, so a session you parked stays `your_turn`, and oldest first would bury new work under it.
5. **`idle`:** the latest `last_event_at` first, as `your_turn`.
6. **The other tiers:** the earliest `last_event_at` first, so a `working` session that has gone quiet for a long time, perhaps stuck, is first in its tier.
7. **Ties** break by [session order](operations.md#session-order).

fzf keeps this order while you type (`--no-sort`), so a filter narrows the list without reordering it.

### Jump lines

One line per candidate, tab-delimited, starting with a hidden **key**, the session's UUID, as [restart's](#lines). fzf shows and searches the rest.

At 2:00PM, with a 1-hour cache, in order (fzf's default layout draws them bottom up, the first next to the prompt):

```
🙋  #9   —     your_turn    ♨️ until 2:48PM  12m  ~/code/sesshin  attention design  ticket=auth-3
🔐  #12  api   blocked      ♨️ until 2:56PM  4m   ~/code/api      fix auth          ticket=auth-4 note="waiting on review"
⛔  #3   docs  stalled      🧊 ~80k          3h   ~/notes         #3
🙋  #7   —     your_turn    🧊 ~45k          2h   ~/code/koan     triage
🙋  #2   —     your_turn    🧊 ~120k         2d   ~/code/herd     parked
💤  #8   —     idle         —                5m   ~/code/shingi   #8
⏳  #5   —     self_waking  ♨️ until 2:57PM  3m   ~/code/shingi   nightly
🏃  #4   ci    working      ♨️ until 2:22PM  38m  ~/code/api      run e2e           task=57
```

| Column | Content |
|---|---|
| Mark | The attention's mark: `blocked` 🔐, `stalled` ⛔, `your_turn` 🙋, `idle` 💤, `self_waking` ⏳, `working` 🏃, `unknown` ❓. |
| ID | `#<id>`, or `—` without one. |
| Job | The job the session reports, or `—`. |
| Attention | Its attention, as the session view spells it, so fzf can match it. |
| Cache | Its prompt cache, as the [statusline](hooks-spec.md#rendering) shows it: warm `♨️ until <time>`, cold `🧊 ~<tokens>` or `🧊 cold`, and `—` when unknown or `null`. |
| Quiet | How long since its `last_event_at`: `<n>s`, `<n>m`, `<n>h`, `<n>d`. |
| cwd | Its `cwd`, with the home directory as `~`; `—` when `null`. |
| Name | Its [name](design-spec.md#terms). |
| Extra | Its [`extra`](design-spec.md#user-owned-extra), as the [Extra column](#extra-column) renders it. |

Columns are joined and padded as [restart's](#lines) are, Name and Extra included, but by display width, every emoji counting two columns, and every field is scrubbed as restart's are. No preview: [`sesshin show`](cli-spec.md#show) has the details.

### Jump outcomes

fzf's stdout is the selection, read as [restart's](#outcomes) is.

| fzf | Outcome |
|---|---|
| Enter, exit `0` | Focus the line under the cursor. |
| Enter with nothing matching, exit `1` | Nothing picked: `ok: true`, `actions` empty. |
| Esc or ctrl-c, exit `130` | `cancelled`: nothing was focused. |
| Any other exit | `unavailable` (`fzf-failed`). |

**Keys:** fzf's own, single-select: Enter accepts.

**Output schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "jump-output",
  "type": "object",
  "required": ["actions"],
  "properties": {
    "actions": {
      "type": "array",
      "maxItems": 1,
      "description": "The focus, or empty when nothing was picked or there were no candidates.",
      "items": {
        "type": "object",
        "required": ["operation", "input", "output"],
        "properties": {
          "operation": { "const": "focus" },
          "input": { "$ref": "focus-input", "description": "As passed." },
          "output": { "$ref": "envelope", "description": "focus's envelope, unchanged: success or failure." }
        },
        "additionalProperties": false
      }
    }
  },
  "additionalProperties": false
}
```

The envelope's `warnings` are the load's; `focus`'s own stay in its `output`. `jump` exits `0` when it ran, whatever its `focus` did.

## Errors

The pickers add two CLI-only error kinds to [`usage`](cli-spec.md#usage-errors), as koan's `pick` does. No operation raises them.

| Kind | When | `details` |
|---|---|---|
| `unavailable` | The picker can't run: no terminal, or no usable fzf. | `reason`: `no-terminal` (`/dev/tty` doesn't open), `fzf-missing`, `fzf-too-old` (with `found` and `required`), or `fzf-failed` (with `status`, fzf's exit status, if it exited: a bad option in `FZF_DEFAULT_OPTS`, say). |
| `cancelled` | The person pressed Esc or ctrl-c. Nothing was resumed or focused. | none (`{}`). |

`restart`'s, in this order: `invalid-input`; `environment`; `corrupt` (`config.toml`); `unavailable` (`fzf-missing`, `fzf-too-old`, `fzf-failed` from `fzf --version`); `terminal` (`unavailable`), as `resume` would raise it; the load's errors; `unavailable` (`no-terminal`), only when there are candidates; then fzf's outcome. Once fzf has accepted, `restart` raises nothing more: each `resume`'s failure is in its `actions` entry.

`jump`'s, in this order: `invalid-input`; `environment`; `corrupt` (`config.toml`); `unavailable` (`fzf-missing`, `fzf-too-old`, `fzf-failed` from `fzf --version`); the load's errors; `unavailable` (`no-terminal`), only when there are candidates; then fzf's outcome. Once fzf has accepted, `jump` raises nothing more: `focus`'s failure is in its `actions` entry.

**stderr** follows the CLI spec's one-line rule, except that fzf's own stderr (a message about a bad option) passes through to the terminal.

**Signals.** While fzf runs, a picker catches SIGINT and SIGQUIT and discards them: ctrl-c is fzf's key, and cancels. Any other signal, or SIGINT outside fzf (during `restart`'s `resume`s, or `jump`'s `focus`), is a crash, as the CLI spec's [Exit codes](cli-spec.md#exit-codes) say, and the tabs already opened stay open, with no report.

## fzf options

- **`FZF_DEFAULT_OPTS`** (and `FZF_DEFAULT_OPTS_FILE`) are honored: colors, layout, borders, history.
- **The first line is next to the prompt.** The pickers pass no layout, so in fzf's default the lines are drawn bottom up: the first, which the cursor starts on, sits right above the prompt, and what you type stays next to the lines that match it. `--layout reverse` (in `SESSHIN_PICK_OPTS` or `FZF_DEFAULT_OPTS`) puts the prompt and the first line at the top instead.
- **Options undone.** After `FZF_DEFAULT_OPTS` and before `SESSHIN_PICK_OPTS`, the picker passes `--no-select-1 --no-exit-0 --no-expect --no-tmux --no-read0 --no-header-lines --no-print0 --no-print-query --accept-nth ..`, and its own options: `restart`'s `--multi`, `--delimiter '\t'`, `--with-nth 2..`, `--with-shell 'sh -c'`, `--preview`, and `--bind ctrl-a:select-all`; `jump`'s `--no-multi`, `--no-sort`, `--delimiter '\t'`, and `--with-nth 2..`. The first two would accept or abort without the person; `--expect`, `--print0`, `--print-query`, and `--accept-nth` change what fzf prints, which is the selection; `--read0` and `--header-lines` change what it reads; `--tmux` would run it in a popup the picker's terminal check wasn't made for.
- **`SESSHIN_PICK_OPTS`** is appended last, so it wins: e.g. `SESSHIN_PICK_OPTS='--height 60% --layout reverse'`. It is split as fzf splits `FZF_DEFAULT_OPTS`. One that doesn't split is `fzf-failed`, before fzf runs.
- **Rebinding is at your own risk.** An option that undoes `--multi`, `--no-sort`, the delimiter, or the fields can break the picker, which doesn't detect it.
- **`FZF_DEFAULT_COMMAND`** is never used: the picker writes every line to fzf's stdin.

## Departures from the CLI spec

- **The intended user is a person** at a terminal, and a picker fails fast without one.
- **Rendering for people,** but only on the terminal: fzf, and `jump`'s message when it or its `focus` fails. stdout is still one JSON envelope, the same whether it is a terminal, a pipe, or a file.
- **An outside program.** fzf is a runtime dependency of the pickers alone.
- **Two CLI-only error kinds,** `unavailable` and `cancelled`, besides `usage`.
- **Interrupts.** In fzf, ctrl-c cancels with an envelope, and SIGINT is discarded (see [Errors](#errors)).
- **stderr.** fzf's own stderr reaches the terminal.
- **Operations without passthrough.** `restart`'s `resume`s and `jump`'s `focus` write nothing to stdout; their envelopes are in `actions`.
- **`SESSHIN_PICK_OPTS`** is an environment override, which the CLI spec's [Not included](cli-spec.md#not-included) otherwise rules out. It only restyles fzf.

## Testing

- **Without fzf.** Lines, End words, preview files, the options passed, and the outcome table are tested with a fake fzf: a script on `PATH` that records its arguments, stdin, and environment, and prints a chosen selection with a chosen exit status. The `resume`s run against a fake launch, as `spawn`'s tests do. Hostile titles (a tab, a newline, a forged UUID) stay on one line under their own key.
- **The Extra column.** Its rendering: `{}` and `null`; each scalar kind; numbers' text kept (`1.10`); nested objects and arrays; keys needing quotes (a space, `=`, a non-ASCII letter, empty); values needing quotes (a space, NBSP, U+2028, `=`, `"`, empty, control characters); `<>&` unescaped; stored key order. A hostile value (a tab, a newline, ESC, U+2028, a forged UUID) stays on one line under its own key. The cap: a long ASCII value, and cuts next to `♨️` and next to a ZWJ emoji, each end within 200 columns with `…`, and so does one whose DEL and C1 characters widen it only once scrubbed. In both pickers' lines: Extra last, two spaces after Name; a name over 32 columns pushes only its own Extra; a line with an empty Extra, and every line when no candidate has one, has no trailing spaces. In `restart`'s preview: the `extra` block last, whole and indented for a value past the cap; an `extra` row of `—` for `{}` and `null`; a newline in a value as `\n`, not a line break; DEL, U+0085, and U+009B as `\u007f`, `\u0085`, and `\u009b`, none of them raw in the file.
- **With fzf, end to end.** One smoke test drives a real fzf in a pseudo-terminal: type a query, ctrl-a, Enter; and Esc. Against 0.63.0 and the current release, as koan's does. `FZF_DEFAULT_OPTS='--select-1 --exit-0 --expect=esc --print-query'` changes nothing.
- **jump without fzf.** With the fake fzf: the [jump order](#jump-order) over a table of sessions crossing every attention, cache state, and quiet time (ties included), the line columns and marks, the options passed (`--no-sort` among them), each outcome, and a `focus` against a fake `kitten`: its failure in `actions`, with the message on the terminal and the wait for a key, after the envelope; the same for a failure before fzf (`fzf-missing`, a load error); and no wait when it succeeds unverified, or after `cancelled`.
- **jump for real.** A manual check in a scratch kitty: sessions at a dialog, finished, and working, with the overlay binding; the right one first, under the cursor, and Enter brings it to the front: in another tab, another OS window, and a second kitty instance, a split in the overlay's own tab, and the window the overlay covers. Closing the overlay must leave the picked window focused.
- **The reboot.** A manual check, as the hooks' verifications are done: sessions in a scratch kitty instance, killed with it, come back with `restart`, `killed` and first.
