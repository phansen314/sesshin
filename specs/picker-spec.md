# sesshin picker spec

sesshin's pickers: commands for a person at a terminal, built on [fzf](https://github.com/junegunn/fzf). There are two: [`restart`](#restart) and [`jump`](#jump). They are specified on top of the [CLI spec](cli-spec.md) and the [operations](operations.md), and run no operation of their own: they compose [`list`](operations.md#list) for what they show with the operations they run on the selection, each as its own call. Everything the CLI spec says holds for a picker except where this document says otherwise; those places are collected in [Departures from the CLI spec](#departures-from-the-cli-spec).

They are small: one fzf run, no keys that act inside it, no callbacks into sesshin. What they share is under [How the pickers run](#how-the-pickers-run); each section after it says only what differs.

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
- **fzf** on `PATH`, version 0.63.0 or later, checked with `fzf --version` without `FZF_DEFAULT_OPTS` and `FZF_DEFAULT_OPTS_FILE` in its environment, the first word without any `-` suffix, compared as three numbers.
- **A terminal backend,** for `restart` only: the caller runs where [`resume`](operations.md#resume) can open tabs (a [backend](design-spec.md#terminal-backends) that can launch one: kitty with remote control, outside tmux and screen). Checked before fzf starts, so a selection is never made only to fail.

## How the pickers run

Both pickers run the same steps.

1. **Check** the input, `HOME`, `config.toml`, and fzf, in the [Errors](#errors) order, and whatever else the picker adds.
2. **Load** the candidates: one `list`, with the picker's `liveness`. Its error, if any, is passed through, and fzf never opens. [Headless](design-spec.md#terms) sessions are left out, as `list` leaves them out by default.
3. **No candidates:** return at once, with `actions` empty and fzf never opened.
4. **Pick** in fzf, with every candidate as a line, in the picker's order, and the picker's preview. See [Outcomes](#outcomes).
5. **Act** on the pick, recording each action in `actions`.

### Outcomes

fzf's stdout is the selection. A picker undoes the options that would change it (see [fzf options](#fzf-options)), and reads it as lines, taking each line's key. A key it didn't offer is ignored.

| fzf | Outcome |
|---|---|
| Enter, exit `0` | Act on the pick: `restart`'s marked lines, or, with none marked, the line under the cursor; `jump`'s line under the cursor. |
| Enter with nothing matching, exit `1` | Nothing picked: `ok: true`, `actions` empty. |
| Esc or ctrl-c, exit `130` | `cancelled`: nothing was resumed or focused. |
| Any other exit | `unavailable` (`fzf-failed`). |

### Output

The output is `{"actions": [...]}`: one entry per action, in line order, each with the `operation`, its `input` as passed, and its `output`, the operation's envelope unchanged, success or failure. Each picker's schema is under it. `jq '.result.actions[] | select(.output.ok | not)'` finds the failures. The envelope's `warnings` are the load's; each operation's own warnings stay in its `output`. A picker exits `0` when it ran, whatever its operations did.

### Preview files

The preview of the line under the cursor is a file written before fzf starts, once there are candidates and a terminal: one file per candidate, named by its key, in a private temp directory named `sesshin-<picker>-<random>` (mode `0700`, under `$XDG_RUNTIME_DIR` if set, else the system temp directory). The directory is removed as soon as fzf returns, whatever it returned, before any action. A failure writing the files is `io`, before fzf opens. fzf's preview command is `cat -- <dir>/{1}`, with the directory quoted for `sh`, and fzf quoting `{1}`. So the preview needs no call back into sesshin, and a key, a UUID, is safe as a file name.

Each row is `label:` padded to 16 columns, a space, then the value, `—` when unknown. Every value is scrubbed as the [lines'](#lines) are. The last part is `extra:` on its own line, then the whole [`extra`](design-spec.md#user-owned-extra), uncut, as indented JSON as the [File format](design-spec.md#file-format) writes it, with DEL and U+0080–U+009F escaped too (`\u009b`: the 8-bit CSI, which some terminals act on); or, when it is `{}` or `null`, an `extra` row of `—` like any other row's. The pane doesn't scroll sideways or wrap, so a one-line JSON would be clipped. The `extra` block isn't scrubbed, since its line breaks are its own, and no control character is left raw in it.

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

1. **Check** also the terminal backend ([Requirements](#requirements)).
2. **Load** with `liveness` `ended`. No one resumes an agent's `claude -p`, which is why headless sessions stay out.
3. As [shared](#how-the-pickers-run).
4. **Pick** with every candidate as a [line](#lines), multi-select on, and the [preview](#preview).
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
- **Capped at 200 columns** of display width, measured as [jump's lines](#jump-lines) are (every emoji two). A longer rendering is cut on a grapheme boundary, so an emoji keeps its U+FE0F or ZWJ sequence, and ends with `…`. fzf searches only what the line holds: a tag past the cut is found in the previews ([restart's](#preview), [jump's](#jump-preview)) and `sesshin show`, not by typing it.
- **Past the window's edge.** fzf clips a line wider than its window, and scrolls it sideways to show the match when the query hits text past the edge (its `hscroll`, on by default).
- **No ranking effect.** `restart` breaks ties by session order (`--tiebreak index`), and `jump` doesn't sort: a long Extra never moves a line.

Unicode format characters (a right-to-left override, say) pass through `scrub`, in `extra` as in names: they can garble how a line looks, never split it.

### Preview

The session's details, one row each, in the [shared format](#preview-files): sesshin ID, name, job, `cwd`, `git_branch`, `model`, `permission_mode`, `started_at`, last seen, `ended_at`, `end_reason`, compactions, cost, `transcript_path` and whether it exists, and the placement's tab title. Last, the `extra` block. The directory is `sesshin-restart-<random>`.

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
          "input": { "type": "object", "description": "As passed: valid against resume-input of the release that wrote it. Open here, since a later release may pass an optional field this one's resume-input doesn't know." },
          "output": { "$ref": "envelope", "description": "resume's envelope, unchanged: success or failure." }
        }
      }
    }
  }
}
```

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

1. **Check** nothing more: there is no terminal backend check, since `focus` reaches a session's window from anywhere, and its failure is reported in `actions`.
2. **Load** with `liveness` `live`, which includes liveness `unknown`.
3. As [shared](#how-the-pickers-run).
4. **Pick** one, with every candidate as a [line](#jump-lines), in [jump order](#jump-order), and the [preview](#jump-preview).
5. **Focus** the pick, as `focus` with `{"session": <uuid>}`, recording it in `actions`.
6. **Show a failure:** after writing the envelope to stdout, so a caller reading it has it whole however long the wait, when the envelope is a failure other than `cancelled` (an `fzf-missing` included: the likeliest failure in the overlay, from kitty's `PATH`), or `focus` failed, and `/dev/tty` opens, write the error's `<kind>: <message>` to `/dev/tty` as one line, and wait for a key, read in raw mode, so ctrl-c is a key too. In the overlay of the key binding below, jump's window closes as it exits, and the envelope with it, so you would otherwise be left where you were with no word of why. Not when `focus` succeeded with `verified` `false`: the focus has already taken you to another window, and an overlay waiting in the one you left would sit there unseen. Not after `cancelled` either: you pressed Esc, and know why. When this step will show a failure, the failure's `sesshin: <kind>: <message>` line is left out of stderr ([cli-spec](cli-spec.md#output)), so the overlay doesn't show the message twice; stdout is unchanged. If `/dev/tty` then doesn't open, the line goes to stderr after all. A `cancelled` keeps its line. Input and usage errors (a bad `-i`/`--input`, a bad flag) are not shown by this step: they fail before the picker runs, as any command's do. A fixed key binding passes no input, so it can't raise them.

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
🙋  #7   —     your_turn    🧊 ~45k          2h   ~/code/web      triage
🙋  #2   —     your_turn    🧊 ~120k         2d   ~/code/herd     parked
💤  #8   —     idle         —                5m   ~/code/infra    #8
⏳  #5   —     self_waking  ♨️ until 2:57PM  3m   ~/code/infra    nightly
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

Columns are joined and padded as [restart's](#lines) are, Name and Extra included, but by display width, every emoji counting two columns, and every field is scrubbed as restart's are. The [preview](#jump-preview) has more of the session; [`sesshin show`](cli-spec.md#show) has all of it.

### Jump preview

What the line can't say: whether the session is worth going to now. Only what is the session's own, from its session view, nothing about other sessions. In four parts, most decision-relevant first, since a short pane shows only the top. Rows as in [Preview files](#preview-files), except the few shown only when they say something.

1. **What it wants.** `attention`, with its [mark](#jump-lines); `status`; `liveness`, only when it is `unknown`: its process couldn't be checked; `quiet`, as the Quiet column. Then `stall_reason`, when not `null`, and `pending` (`<n> background, <n> cron`), when not `null` and either count is above `0`.
2. **What going there costs.** Each row's parts stand alone: a part that is `null` is left out, or `—` in its place when a later part is shown, and the row is `—` when every part is `null`.
   - `cache`, as the Cache column.
   - `hit ratio`: `hit_ratio` (a fraction) as a percentage, rounded as the [statusline](hooks-spec.md#rendering)'s percentages are; then, in parentheses and joined with `; `, `misses` (`3 misses`, `1 miss`) and `last: ` with `last_miss_cause` joined with `, ` (an empty one counts as `null`). No parentheses when both are `null`: `92% (3 misses; last: ttl, edit)`, `— (3 misses)`, `92%`.
   - `context`: as the statusline's 🧠 segment writes it, without the emoji: `context_percent` rounded, then `context_tokens`/`context_window`, both counts left out when either is `null`: `56% 112k/200k`, `56%`. Where the statusline writes `0%` for a missing percentage, this row writes `—`.
   - `cost`: `cost_usd` with two decimals, then the burn rate in parentheses: `$4.12 ($1.80/h)`, `$4.12`, `— ($1.80/h)`.
3. **Where it is.** `session` (`#<id> <name>`; the name alone when it is `#<id>`, an untitled session's, or without an ID), `job`, `cwd` with the home directory as `~`, `git_branch`, `model`, `permission_mode`, and `tab title`.
4. **What it is tagged with.** The `extra` block, as [Preview files](#preview-files) writes it.

A blocked session at 2:00PM, the second of the [lines](#jump-lines) above:

```
attention:       🔐 blocked
status:          needs_approval
quiet:           4m
cache:           ♨️ until 2:56PM
hit ratio:       92% (3 misses; last: ttl, edit)
context:         56% 112k/200k
cost:            $4.12 ($1.80/h)
session:         #12 fix auth
job:             api
cwd:             ~/code/api
git_branch:      main
model:           claude-opus-5-5
permission_mode: acceptEdits
tab title:       api review
extra:
{
  "ticket": "auth-4",
  "note": "waiting on review"
}
```

Restart's `started_at`, last seen, `ended_at`, `end_reason`, compactions, and `transcript_path` are left out: every candidate is live, or its liveness unknown, and they don't bear on going there now.

**Files,** as [Preview files](#preview-files) says, in `sesshin-jump-<random>`, written after the sort with the `now` the lines were rendered with. The directory is removed before `focus`, and before step 6's wait for a key.

**A snapshot,** as the [jump order](#jump-order) is: the quiet time doesn't tick, and a cache that expires while fzf is open still reads warm; its `until` time says when.

**Below the list:** `--preview-window 'down,50%'`. jump's lines are wide, and its overlay may cover a narrow split; a pane beside them would clip their cwd and Name. `SESSHIN_PICK_OPTS` moves it, or hides it.

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
          "input": { "type": "object", "description": "As passed: valid against focus-input of the release that wrote it. Open here, since a later release may pass an optional field this one's focus-input doesn't know." },
          "output": { "$ref": "envelope", "description": "focus's envelope, unchanged: success or failure." }
        }
      }
    }
  }
}
```

## Errors

The pickers add two CLI-only error kinds to [`usage`](cli-spec.md#usage-errors). No operation raises them.

| Kind | When | `details` |
|---|---|---|
| `unavailable` | The picker can't run: no terminal, or no usable fzf. | `reason`: `no-terminal` (`/dev/tty` doesn't open), `fzf-missing`, `fzf-too-old` (with `found` and `required`), or `fzf-failed` (with `status`, fzf's exit status, if it exited: a bad option in `FZF_DEFAULT_OPTS`, say). |
| `cancelled` | The person pressed Esc or ctrl-c. Nothing was resumed or focused. | none (`{}`). |

**The order,** for both: `invalid-input`; `environment`; `corrupt` (`config.toml`); `unavailable` (`fzf-missing`, `fzf-too-old`, `fzf-failed` from `fzf --version`); for `restart` only, `terminal` (`unavailable`), as `resume` would raise it; the load's errors; `unavailable` (`no-terminal`), only when there are candidates; then fzf's outcome. Once fzf has accepted, a picker raises nothing more: each operation's failure is in its `actions` entry.

**stderr** follows the CLI spec's one-line rule, except that fzf's own stderr (a message about a bad option) passes through to the terminal.

**Signals.** While fzf runs, a picker catches SIGINT and SIGQUIT and discards them: ctrl-c is fzf's key, and cancels. Any other signal, or SIGINT outside fzf (during `restart`'s `resume`s, or `jump`'s `focus`), is a crash, as the CLI spec's [Exit codes](cli-spec.md#exit-codes) say, and the tabs already opened stay open, with no report.

## fzf options

- **`FZF_DEFAULT_OPTS`** (and `FZF_DEFAULT_OPTS_FILE`) are honored: colors, layout, borders, history.
- **The first line is next to the prompt.** The pickers pass no layout, so in fzf's default the lines are drawn bottom up: the first, which the cursor starts on, sits right above the prompt, and what you type stays next to the lines that match it. `--layout reverse` (in `SESSHIN_PICK_OPTS` or `FZF_DEFAULT_OPTS`) puts the prompt and the first line at the top instead.
- **Options undone.** After `FZF_DEFAULT_OPTS` and before `SESSHIN_PICK_OPTS`, the picker passes `--no-select-1 --no-exit-0 --no-expect --no-tmux --no-read0 --no-header-lines --no-print0 --no-print-query --no-tac --accept-nth ..`, and its own options: `restart`'s `--multi`, `--delimiter '\t'`, `--with-nth 2..`, `--tiebreak index`, `--with-shell 'sh -c'`, `--preview`, `--bind ctrl-a:select-all`, and `--query`; `jump`'s `--no-multi`, `--no-sort`, `--delimiter '\t'`, `--with-nth 2..`, `--with-shell 'sh -c'`, `--preview`, `--preview-window 'down,50%'`, and `--query`. The first two would accept or abort without the person; `--expect`, `--print0`, `--print-query`, and `--accept-nth` change what fzf prints, which is the selection; `--tac` reverses the order the pickers rely on (jump's [order](#jump-order), restart's most recent first); `--read0` and `--header-lines` change what it reads; `--tmux` would run it in a popup the picker's terminal check wasn't made for.
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

See the implementation spec's [Restart](implementation-spec.md#restart) and [Jump](implementation-spec.md#jump).
