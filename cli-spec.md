# sesshin CLI spec

The `sesshin` command-line interface, and the `sesshin-hook` binary's command line: how each command maps to the [operations](operations.md), how input gets in, and what comes out. The CLI adds no behavior of its own beyond parsing arguments and running operations. Everything about the data is specified by the operations and the [design spec](design-spec.md).

Each kind of caller gets its own surface:

- **People and scripts** run `sesshin`: `list`, `show`, `version`, `install`, `uninstall`, `spawn`, `resume`, `send`, `focus`, `update`, `prune`, and `migrate`. Each writes one JSON envelope, and people read it through `jq`.
- **People at a terminal** also have the pickers `sesshin restart` and `sesshin jump`, specified in [picker-spec.md](picker-spec.md), which says where they depart from this document's rules.
- **Claude Code** runs `sesshin-hook <verb>`, a separate binary that follows the [hooks contract](hooks-spec.md#the-contract) rather than this document's global rules (see [sesshin-hook](#sesshin-hook)). It is separate so that no hook pays for what the CLI links (see [Hook cost](design-spec.md#hook-cost)).

`doctor`, `repair`, `info`, and the picker `watch` are [deferred](deferred/cli-spec.md).

## Global behavior

### Output

- **JSON only.** Every invocation that exits `0`, `1`, or `2` writes exactly one [output envelope](operations.md#output-envelope) to stdout, whether it succeeds or fails. `--help` is the exception, and `sesshin-hook` follows its own rules. There is no human-readable output mode: `jq` does the pretty-printing, and the statusline is the human view.
- **Passthrough.** A command that runs one operation writes that operation's envelope unchanged. Where a command's output differs from its operation's, the command's entry says how.
- **Compact.** The envelope is written on a single line, followed by a newline. The format is the same whether or not stdout is a terminal. A complete envelope always ends in that newline.
- **Encoding.** Output is UTF-8, with the string escaping of the design spec's [File format](design-spec.md#file-format).
- **Delivered before exit.** Exit `0`, `1`, or `2` is reported only once the whole envelope has been written. On any other exit status, stdout may hold nothing or an incomplete line (see [Exit codes](#exit-codes)).
- **stderr** gets at most one line from sesshin. It is written after the envelope has been delivered, so a failure stays visible when stdout goes into a pipeline (e.g. `sesshin install --dry-run | jq .result.changes`). The line depends on how sesshin exits:
  - Exit `1` or `2`: `sesshin: <kind>: <message>`, even when the envelope also has warnings.
  - Exit `0` with warnings: `sesshin: N warnings (see .warnings in the output)`, or `1 warning` for one. The warnings themselves are never listed.
  - Exit `0` without warnings, and `--help`: nothing.
  - Exit `3`: only its notice.

  The exception is [`jump`](picker-spec.md#jump) when its step 6 will show the failure on the terminal: the error line is left out, so the message isn't shown twice. stdout is unchanged.

  The line is human-readable and not part of the contract. Control characters in it are escaped, so it stays one line. Callers read the envelope.
- **Exception:** `--help` writes plain-text usage to stdout. It is not an operation.

### Input

- **Flags and arguments** supply operation input for everyday use.
- **`-i, --input <file>`** supplies operation input read from `<file>`, where `-` means stdin. Every command accepts it.
- **stdin is read only when a value names it.** That means `--input -`, `spawn`'s `--prompt-file -`, or `send`'s `--text-file -`. Otherwise sesshin never reads stdin, so it is safe inside loops that feed stdin to something else. (`sesshin-hook` is the exception: its payload always arrives on stdin.)
- **Any readable path.** `<file>` may be any path that can be read to the end, including process substitution and named pipes. A file literally named `-` is given as `./-`.
- **Exactly one JSON object, in UTF-8.** Empty input, a value that is not an object, trailing bytes, a byte-order mark, or invalid UTF-8 is `invalid-input` (`field`: `""`).
- **Operation rules apply.** The input is held to the operation's input schema; violations are `invalid-input`.
- **Unreadable input.** A missing or unreadable `<file>`, or a directory, is `io`.
- **Either `--input` or field arguments, not both.** Giving `--input` together with any argument or option that sets an input field is a [usage error](#usage-errors).
- **`--input` is taken as-is.**

```sh
jq -n '{dry_run: true}' | sesshin install -i -
```

### Command line

The command line is parsed in the GNU style of Go's [cobra](https://github.com/spf13/cobra) and [pflag](https://github.com/spf13/pflag), with these rules:

- **Command names are operation names.** A command that runs one operation has that operation's name. There are no aliases.
- **Option names are field names,** in kebab-case: `dry_run` is `--dry-run`. A field nested in an object is named by its path: `/extra/merge` is `--extra-merge`. Options that set no field under their own name are exceptions, and each command lists them.
- **Arguments are for the one required subject.** The only one is the session [`show`](#show), [`resume`](#resume), [`send`](#send), [`focus`](#focus), or [`update`](#update) acts on. Everything optional is an option, so a bare token always has one meaning. [`spawn`](#spawn)'s and `resume`'s `claude` arguments are the exception: they follow `--`, where nothing is an option.
- **Booleans.** `--<field>` sets `true`, and `--<field>=false` sets `false` (e.g. `--dry-run=false`). A boolean never takes the next token as its value. A boolean's value that is neither `true` nor `false` (`--dry-run=maybe`) is a [usage error](#usage-errors), the one exception to a bad value being `invalid-input`, since the parser rejects it before any input exists.
- **Required options** are a usage error when missing, unless `--input` is given.
- **Short options are rare.** Only `-i` and `-h` have them.
- **Options and arguments follow the command,** in any order. `--` ends options, and a lone `-` is an ordinary argument.
- **Option values** may be given as `--flag value` or `--flag=value`. An option that takes a value always consumes the next token.
- **Exact names.** Commands and options are matched exactly: no abbreviations, no aliases, and no other case.
- **Arguments are single tokens.** A value with spaces is quoted for the shell. Unquoted extra words are a usage error and are never joined.
- **Value formats.** A value that sets an input field is converted by its field's type:
  - *Integers* are decimal, with no `+`, leading zeros, fraction, or exponent.
  - *Maps* (`spawn`'s `--var`) take one `KEY=VALUE` per option, split at the first `=`.
  - *Lists* of items that can't contain a comma (field names) are comma-separated: `--fields id,name,status`. The option may be repeated, and its lists are joined in order. `''` is the empty list.
  - *Repeatable lists* of items that can contain a comma (`update`'s `--extra-remove`, keys) take one item per occurrence: `--extra-remove status --extra-remove owner`, and `--extra-remove 'a,b'` names the key `a,b`.
  - *JSON values* (`spawn`'s `--extra`, `update`'s `--extra-merge` and `--extra-replace-all`) are exactly one JSON value, with no repeated key. One that isn't valid JSON is `invalid-input` at the option's field (`/extra`, `/extra/merge`, `/extra/replace_all`); its type, that it is an object, and its limits are checked by the operation. Numbers in it are kept as written.
  - *Encoding.* Every value is UTF-8. One that is not is `invalid-input` at its field.
  - A value that cannot be converted is `invalid-input`.
- **The CLI rejects only what it cannot build.** A combination is a usage error only when no input can be built from it, such as two options that set the same field. Combinations the operation forbids are left to the operation, which reports them as `invalid-input`.
- **A repeated single-value option:** the last one wins. A list option accumulates.
- **Bare `sesshin`**, with no command, is a usage error.
- **`--help`** (or `-h`) writes help text and exits `0`, running no operation. Help text is not part of the contract.

### Selectors on the command line

A `<session>` argument is a [selector](operations.md#selecting-a-session), passed to the operation unchanged as a string. The CLI neither interprets nor completes it.

- **A sesshin ID is bare digits:** `sesshin show 12`. `#12` is how sesshin displays an ID, not an input form. It would be a poor one anyway: at the start of a word, `#` begins a shell comment, so `sesshin show #12` runs `sesshin show` with no argument.
- **A UUID or a prefix of one,** 8 characters or more: `sesshin show 0b6c5a3e`.
- **A job:** `sesshin resume api`, or `job:deadbeef` for a job that looks like a UUID prefix.
- **`self`:** the session the command runs in, from inside a Claude session (a Bash tool call, a hook): `sesshin show self`, `sesshin update self --extra-merge '{"ticket":"auth-3"}'`. From a plain shell it is `not-found`. A job named `self` is `job:self`.

### Chaining commands

One command's output can pick the session for the next. Pass `session_id`: every output that names a session has it, including `list` with any `--fields`, and it always selects exactly that session. Don't pass `id` or `job`. Either can be `null`, which `jq -r` prints as the job name `null`, and a job that looks like a UUID prefix (`deadbeef`) is read as one unless written `job:deadbeef`.

A command acts on one session, so a list of them goes through `xargs -n1` (or a `while read` loop). sesshin [reads stdin](#input) only when a value names it, so it is safe inside either. Use `[]?` to iterate, so a failed command yields nothing rather than a `jq` error; its own line is on stderr.

```sh
sesshin list --fields attention | jq -r 'first(.result.sessions[]? | select(.attention == "blocked")) | .session_id' | xargs -r sesshin focus
sesshin list --fields cwd,attention | jq -r '.result.sessions[]? | select(.cwd == "/home/me/api" and .attention == "your_turn") | .session_id' | xargs -r -n1 sesshin send --text 'go on'
sesshin spawn --job api | jq -r '.result.session.session_id // empty' | xargs -r sesshin focus
```

### Usage errors

A usage error is a problem with the shape of the command line. Examples:

- an unknown command or option;
- a missing or extra argument;
- an option missing its value;
- two options that set the same field;
- `--input` given together with field arguments.

It is reported as an envelope with error kind `usage`, and exits `2`. A token in the right place whose value is unacceptable is `invalid-input` instead, with `field` the JSON Pointer of the input field it sets. A bad value is therefore the same error whether it arrives as an argument or through `--input`.

`usage` is a CLI-only error kind. `details` is:

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "usage-details",
  "type": "object",
  "required": ["problems"],
  "properties": {
    "problems": {
      "type": "array",
      "minItems": 1,
      "items": {
        "type": "object",
        "required": ["reason"],
        "properties": {
          "argument": { "type": "string", "description": "The offending token, when the parser names one." },
          "reason": { "type": "string", "description": "Human-readable." }
        },
        "additionalProperties": false
      }
    }
  },
  "additionalProperties": false
}
```

### Exit codes

| Code | Meaning |
|---|---|
| `0` | Success (`ok: true`), with or without warnings. Also `--help`. Always, for [`sesshin-hook`](#sesshin-hook). |
| `1` | Operation error. The kind is in the envelope. |
| `2` | Usage error. |
| `3` | Outcome unknown: the envelope could not be written to stdout. |
| any other | Outcome unknown: sesshin was terminated before it finished (e.g. `128+n` for signal `n`). Handle like `3`. |

- **One code for all operation errors.** Callers branch on the envelope's `kind` (e.g. `jq -e '.error.kind == "corrupt"'`), not on the exit code.
- **Outcome unknown.** On `3` or any status outside `0`–`2`, the operation may already have taken effect. Whether to rerun it follows the operation's **Retry safety**. Every remaining command is safe to retry.
- **Unwritable stdout.** When stdout cannot be written, sesshin exits `3` with a notice on stderr, or is terminated by `SIGPIPE`. Either way the outcome is unknown.
- **Interrupts are crashes.** A Ctrl-C, a `kill`, or a timeout in the calling harness ends sesshin like a crash: no envelope, exit `128+n`.

### Global options

| Option | Meaning |
|---|---|
| `-i, --input <file>` | Read operation input from `<file>` (`-` for stdin). See [Input](#input). |
| `-h, --help` | Print plain-text usage. |

## Command template

Every command is specified with these parts, in this order. Every part is always present. An empty part is written `**Part:** none.`

| Part | Content |
|---|---|
| **Summary** | Unlabeled first paragraph: what the command does, and the operation it runs, linked. |
| **Synopsis** | The usage line(s). |
| **Operation** | The operation the command runs. |
| **Arguments** | Table of positional arguments and the input field each sets. |
| **Options** | Table of command-specific options, the input field each sets, and its default. |
| **Input** | Anything about input beyond the Arguments and Options mapping. |
| **Output** | `Passthrough.`, or how the output differs from the operation's. |
| **Errors** | Errors the CLI adds beyond the operation's. Usually none. |
| **Examples** | `sh` examples, with the `jq` side where it helps. |

## Commands

### list

Return the sessions sesshin has recorded, live by default, with their derived liveness, metrics, and prompt cache. Runs [`list`](operations.md#list).

**Synopsis:** `sesshin list [--liveness <live|ended|all>] [--include-headless] [--fields <names>] [--limit <n>]`, or `sesshin list -i <file>`.

**Operation:** [`list`](operations.md#list).

**Arguments:** none.

**Options:**

| Option | Field | Default |
|---|---|---|
| `--liveness <value>` | `/liveness` | `live` (which includes liveness `unknown`). |
| `--include-headless` | `/include_headless` | `false`. Include [headless](design-spec.md#terms) sessions (`claude -p`, and sessions started by other sessions), live or ended. |
| `--fields <names>` | `/fields` | None: whole session views. A comma list of [session view](operations.md#session-view) fields; `id` and `session_id` are always included. |
| `--limit <n>` | `/limit` | None: every session. |

**Input:** none beyond the Options mapping.

**Output:** Passthrough. `result.sessions` is in [session order](operations.md#session-order), most recently seen first, and may be empty.

**Errors:** none beyond the operation's.

**Examples:**

```sh
sesshin list --fields name,status,cwd
sesshin list --fields name,metrics,prompt_cache \
  | jq -r '.result.sessions[] | [.id, .name, .metrics.context_tokens, .prompt_cache.state] | @tsv'
sesshin list --liveness ended --limit 10 --fields name,ended_at,end_reason
sesshin list --fields prompt_cache | jq '[.result.sessions[] | select(.prompt_cache.state == "cold")] | length'
sesshin list --liveness all --include-headless --limit 0 | jq .result.total   # how many sessions sesshin has
sesshin list --liveness all --fields job,extra | jq '.result.sessions[] | select(.extra.ticket == "auth-3")'
# every process's chain, oldest session first
sesshin list --liveness all --fields pid,pid_started_at,last_start_at,last_event_at,end_reason,extra \
  | jq '[.result.sessions[] | select(.pid_started_at)]
        | group_by([.pid, .pid_started_at])
        | map(sort_by([.last_start_at, .last_event_at]) | map({id, end_reason, extra}))'
```

### show

Return one session in full, live or ended, and optionally its raw statusline payload. Runs [`show`](operations.md#show).

**Synopsis:** `sesshin show <session> [--include-payload]`, or `sesshin show -i <file>`.

**Operation:** [`show`](operations.md#show).

**Arguments:**

| Argument | Field | Notes |
|---|---|---|
| `<session>` | `/session` | Required unless `--input` is given. A [selector](#selectors-on-the-command-line). |

**Options:**

| Option | Field | Default |
|---|---|---|
| `--include-payload` | `/include_payload` | `false`. |

**Input:** none beyond the Arguments and Options mapping.

**Output:** Passthrough.

**Errors:** none beyond the operation's.

**Examples:**

```sh
sesshin show 12
sesshin show 12 | jq .result.session.prompt_cache
sesshin show 0b6c5a3e --include-payload | jq .result.statusline_payload.rate_limits
```

### version

Report this binary's version and the file formats it supports. Runs [`version`](operations.md#version). It needs no `HOME`, config, or state directory, so it works anywhere the binary runs.

**Synopsis:** `sesshin version`, or `sesshin version -i <file>`.

**Operation:** [`version`](operations.md#version).

**Arguments:** none.

**Options:** none.

**Input:** none. With `--input`, the only valid input is `{}`.

**Output:** Passthrough.

**Errors:** none beyond the operation's.

**Examples:**

```sh
sesshin version | jq -r .result.version
```


### install

Propose wiring sesshin into Claude Code: a copy of Claude Code's `settings.json` ([Locations](design-spec.md#locations)) with sesshin's hooks and statusline registered and its permission rules added, pointing at the `sesshin-hook` beside this `sesshin`, for you to review and apply. sesshin never writes `settings.json`. Runs [`install`](operations.md#install).

**Synopsis:** `sesshin install [--dry-run]`, or `sesshin install -i <file>`.

**Operation:** [`install`](operations.md#install).

**Arguments:** none.

**Options:**

| Option | Field | Default |
|---|---|---|
| `--dry-run` | `/dry_run` | `false`. |

**Input:** none beyond the Options mapping.

**Output:** Passthrough. `result.apply` holds the commands that review and apply the proposal; `result.hook_binary` is the absolute path every hook runs once it is applied. A `statusLine` the proposal replaces is in the `status-line-replaced` warning, and nowhere else.

**Upgrading:** see [Upgrading](docs/upgrading.md).

**Errors:** none beyond the operation's.

**Examples:**

```sh
sesshin install --dry-run | jq '.result.changes'
sesshin install | jq -r '.result.apply[]'   # print the review and apply commands
diff -uN ~/.claude/settings.json ~/.local/state/sesshin/settings.proposed.json
cat ~/.local/state/sesshin/settings.proposed.json > ~/.claude/settings.json
sesshin install --dry-run | jq -e 'all(.result.changes[]; .action == "unchanged")'   # wired?
```

### uninstall

Propose removing sesshin's hooks, statusline, and permission rules from Claude Code's `settings.json`, for you to review and apply as `install`'s proposal is. Apart from the proposal, the state directory is left alone. Runs [`uninstall`](operations.md#uninstall).

**Synopsis:** `sesshin uninstall [--dry-run]`, or `sesshin uninstall -i <file>`.

**Operation:** [`uninstall`](operations.md#uninstall).

**Arguments:** none.

**Options:**

| Option | Field | Default |
|---|---|---|
| `--dry-run` | `/dry_run` | `false`. |

**Input:** none beyond the Options mapping.

**Output:** Passthrough.

**Errors:** none beyond the operation's.

**Examples:**

```sh
sesshin uninstall | jq -r '.result.apply[]'   # print the review and apply commands
```

### spawn

Launch `claude` in a new tab, split, or OS window of the caller's terminal, optionally under a job name, and wait for it to start. Runs [`spawn`](operations.md#spawn).

**Synopsis:** `sesshin spawn [options] [-- <claude args…>]`, or `sesshin spawn -i <file>`.

**Operation:** [`spawn`](operations.md#spawn).

**Arguments:**

| Argument | Field | Notes |
|---|---|---|
| `<claude args…>` after `--` | `/args` | Optional. Every token after `--`, in order, passed to `claude` before the prompt. |

The job is an option, not an argument, because it is optional: `sesshin spawn` with no job is a valid, unnamed session. Everything after `--` goes to `claude` untouched, so sesshin never has to know its flags.

**Options:**

| Option | Field | Default |
|---|---|---|
| `--job <name>` | `/job` | None. |
| `--cwd <dir>` | `/cwd` | The working directory. See Input. |
| `--type <tab\|split\|os-window>` | `/type` | `tab`. |
| `--name <text>` | `/name` | The job. The session's name, for `claude --name` and the tab title. |
| `--prompt <text>` | `/prompt` | None. Mutually exclusive with `--prompt-file`. |
| `--prompt-file <file>` | `/prompt` | Reads the first prompt from `<file>`; `-` is stdin. Mutually exclusive with `--prompt`. |
| `--var <KEY=VALUE>` | `/vars/KEY` | **Repeatable.** One user variable each. |
| `--extra <json>` | `/extra` | None: the session starts with `{}`. A JSON object, the session's [user-owned extra](design-spec.md#user-owned-extra), handed to it in its reservation. |
| `--start-timeout-secs <n>` | `/start_timeout_secs` | `15`. `0` returns as soon as the window is open. |

**Input:**

- **`cwd` is resolved,** as given to `--cwd`: a leading `~/` is expanded to the home directory, and a relative path is resolved against the working directory, as the shell reports it (`PWD` when it names the working directory, else the `getcwd` path). `..` is left in place for the operation to judge. With `--input`, `cwd` is taken as given.
- **`cwd` defaults to the working directory** when `--cwd` isn't given. With `--input`, no default is filled in, and `cwd` is required.
- **`--var KEY=VALUE`** splits at the first `=`. A value may contain `=` or a comma. A token without `=` or the same `KEY` twice is `invalid-input` (`/vars`); a bad `KEY` is the operation's (`/vars/KEY`).
- **`--prompt-file`** reads the file's contents exactly. `-` reads stdin, which is then read for nothing else.

**Output:** Passthrough. `result.session` is the started session, with its sesshin ID, or `null` with a `not-started` warning.

**Errors:**

| Kind | When |
|---|---|
| `io` | The `--prompt-file` file is unreadable, or the working directory can't be determined when `cwd` needs it. |
| `environment` | (`variable`: `HOME`) `--cwd` begins with `~/` and the home directory can't be determined. |
| `invalid-input` | (`/prompt`) The `--prompt-file` contents are not UTF-8. (`/vars`) A malformed `--var`. (`/cwd`) A `~user/` prefix. |
| `usage` | Both `--prompt` and `--prompt-file`. |

**Examples:**

```sh
sesshin spawn --job api --cwd ~/code/api --prompt 'run the test suite and fix failures'
sesshin spawn --job review-142 -- --model opus                 # a claude flag
sesshin spawn --job api --name 'api review'                    # named other than its job
sesshin spawn --job docs --var PROJECT=docs --start-timeout-secs 0
sesshin spawn --job api | jq .result.session.id                # the new sesshin ID
gh issue view 42 --json body -q .body | sesshin spawn --job issue-42 --prompt-file -
sesshin spawn --job auth-3 --extra '{"ticket":"auth-3"}'     # link it to its work
```

### resume

Reopen an ended session with `claude --resume`, in a new tab, in its last directory, under its tab title and job, and wait for it to be live again. Runs [`resume`](operations.md#resume).

**Synopsis:** `sesshin resume <session> [--job <name>] [--start-timeout-secs <n>] [-- <claude args…>]`, or `sesshin resume -i <file>`.

**Operation:** [`resume`](operations.md#resume).

**Arguments:**

| Argument | Field | Notes |
|---|---|---|
| `<session>` | `/session` | Required unless `--input` is given. A [selector](#selectors-on-the-command-line), among ended sessions: a job selects the one last seen. |
| `<claude args…>` after `--` | `/args` | Optional. Every token after `--`, in order, passed to `claude` after `--resume <uuid>`. `claude --resume` restores the conversation, not the flags it was started with: give them again here. |

**Options:**

| Option | Field | Default |
|---|---|---|
| `--job <name>` | `/job` | The session's stored job. Names another job to resume under, when the stored one is taken. |
| `--start-timeout-secs <n>` | `/start_timeout_secs` | `15`. `0` returns as soon as the tab is open. |

**Input:** none beyond the Arguments and Options mapping.

**Output:** Passthrough. `result.session` is the session, live again, or `null` with a `not-started` warning.

**Errors:** none beyond the operation's.

**Examples:**

```sh
sesshin resume api                                     # the job's last session
sesshin resume 12 --job api-old                        # its job api is held by another session
sesshin resume 12 -- --permission-mode acceptEdits     # a claude flag
sesshin list --liveness ended --limit 5 --fields name,job,end_reason   # find it first
```

### send

Type text into a live session's window, as one paste, and press Enter. Runs [`send`](operations.md#send).

**Synopsis:** `sesshin send <session> (--text <text> | --text-file <file>) [--submit=false] [--force]`, or `sesshin send -i <file>`.

**Operation:** [`send`](operations.md#send).

**Arguments:**

| Argument | Field | Notes |
|---|---|---|
| `<session>` | `/session` | Required unless `--input` is given. A [selector](#selectors-on-the-command-line), among live sessions: a job selects the session holding it. |

**Options:**

| Option | Field | Default |
|---|---|---|
| `--text <text>` | `/text` | One of `--text` or `--text-file` is required unless `--input` is given. |
| `--text-file <file>` | `/text` | Reads the text from `<file>`; `-` is stdin. Mutually exclusive with `--text`. |
| `--submit` | `/submit` | `true`. `--submit=false` leaves the text in the input box. |
| `--force` | `/force` | `false`. Sends even when the session's turn hasn't ended, including to a session at a permission prompt or in another dialog, which the text may answer. |

The text is an option, not a second argument: with the session and the text both named, a swapped `sesshin send 'run tests' api` can't type `api` into a session called `run tests`.

**Input:** `--text-file` reads the file's contents exactly, as `spawn`'s `--prompt-file` does, a trailing newline included (which Claude Code drops). `--text-file -` suits text produced by another command, and needs no shell escaping.

**Output:** Passthrough. `result.permission_mode` shows whether the session acts without asking; `result.event_seq` is where its next turn starts counting.

**Errors:**

| Kind | When |
|---|---|
| `io` | The `--text-file` file is unreadable. |
| `invalid-input` | (`/text`) The `--text-file` contents are not UTF-8. |
| `usage` | Both `--text` and `--text-file`, or neither without `--input`. |

**Examples:**

```sh
sesshin send api --text 'run the tests again'
sesshin send 12 --text-file review-notes.md
git diff | sesshin send api --text-file - --submit=false   # paste it, and leave it for you to send
sesshin send api --text 'yes' --force                       # answer a prompt deliberately
```

To prompt a session that is busy, check its status first: `sesshin show api | jq -r .result.session.status` is `waiting` or `idle` once its turn has ended.

### focus

Bring a live session's window to the front. Runs [`focus`](operations.md#focus).

**Synopsis:** `sesshin focus <session>`, or `sesshin focus -i <file>`.

**Operation:** [`focus`](operations.md#focus).

**Arguments:**

| Argument | Field | Notes |
|---|---|---|
| `<session>` | `/session` | Required unless `--input` is given. A [selector](#selectors-on-the-command-line), among live sessions: a job selects the session holding it. |

**Options:** none.

**Input:** none beyond the Arguments mapping.

**Output:** Passthrough. `result.verified` is `false` when the session's window couldn't be found by its pid, and the stored window was focused instead.

**Errors:** none beyond the operation's.

**Examples:**

```sh
sesshin focus api
sesshin focus 12
sesshin list --fields attention | jq -r 'first(.result.sessions[]? | select(.attention == "blocked")) | .session_id' | xargs -r sesshin focus
```

### update

Change a session's user-owned `extra`, live or ended: how a session is tagged after it starts, including by itself after a `/new`. Runs [`update`](operations.md#update).

**Synopsis:** `sesshin update <session> (--extra-replace-all <json> | [--extra-merge <json>] [--extra-remove <key>]…)`, or `sesshin update -i <file>`.

**Operation:** [`update`](operations.md#update).

**Arguments:**

| Argument | Field | Notes |
|---|---|---|
| `<session>` | `/session` | Required unless `--input` is given. A [selector](#selectors-on-the-command-line), among all sessions: a job selects the live session holding it, else the one last seen; `self` the session the command runs in. |

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
sesshin update self --extra-merge '{"ticket":"auth-3"}'     # from inside a session, after /new
sesshin update 12 --extra-merge '{"ticket":"auth-3"}'
sesshin update api --extra-remove ticket --extra-remove note
sesshin update 0b6c5a3e --extra-replace-all '{}'
sesshin update 12 --extra-merge '{"status":"review"}' | jq .result.session.extra
```

A session too new to have its `sesshin.json`, or whose sesshin ID is still pending, fails `conflict` (`no-sesshin-file`); its message says whether to retry after the session's next prompt or `resume` it first, or, when the file is in another format, to run `migrate` (or upgrade):

```sh
sesshin update 0b6c5a3e --extra-merge '{"ticket":"auth-3"}' | jq -r '.error.details | "\(.rule) \(.file)"'
```

### prune

Remove ended sessions last seen before the retention window, and stale or unusable reservations. Runs [`prune`](operations.md#prune). sesshin never runs it for you: run it by hand, or on a schedule.

**Synopsis:** `sesshin prune [--dry-run] [--retain-days <n>]`, or `sesshin prune -i <file>`.

**Operation:** [`prune`](operations.md#prune).

**Arguments:** none.

**Options:**

| Option | Field | Default |
|---|---|---|
| `--dry-run` | `/dry_run` | `false`. |
| `--retain-days <n>` | `/retain_days` | The config's `retain_days`. Given explicitly, it prunes even when the config's is `0`. |

**Input:** none beyond the Options mapping.

**Output:** Passthrough.

**Errors:** none beyond the operation's.

**Examples:**

```sh
sesshin prune --dry-run | jq '.result.pruned | length'
sesshin prune --retain-days 7
```

**Scheduling it.** Any scheduler works; `prune` is safe to run at any time, alongside running sessions. A daily systemd user timer:

```ini
# ~/.config/systemd/user/sesshin-prune.service
[Unit]
Description=Prune ended sesshin sessions and stale reservations

[Service]
Type=oneshot
ExecStart=%h/.local/bin/sesshin prune
```

```ini
# ~/.config/systemd/user/sesshin-prune.timer
[Unit]
Description=Prune ended sesshin sessions and stale reservations daily

[Timer]
OnCalendar=daily
Persistent=true

[Install]
WantedBy=timers.target
```

```sh
systemctl --user daemon-reload
systemctl --user enable --now sesshin-prune.timer
```

Or a crontab line (`crontab -e`), with the binary's absolute path, since cron's `PATH` is short:

```text
17 4 * * * /home/me/.local/bin/sesshin prune > /dev/null
```

Both name the binary where you installed it (`command -v sesshin`): `~/.local/bin` here, `$(go env GOPATH)/bin` after `go install`. A scheduled run should use the same `HOME` (and on Linux the same XDG variables) as your Claude sessions, or it prunes a different state directory (see [Locations](design-spec.md#locations)).

### migrate

Convert the state directory's files to this binary's formats, by running every pending [migration step](design-spec.md#migrations). Runs [`migrate`](operations.md#migrate). Run it right after replacing the binaries; until then, hooks leave older files alone, and other commands warn `migration-pending`.

**Synopsis:** `sesshin migrate [--dry-run]`, or `sesshin migrate -i <file>`.

**Operation:** [`migrate`](operations.md#migrate).

**Arguments:** none.

**Options:**

| Option | Field | Default |
|---|---|---|
| `--dry-run` | `/dry_run` | `false`. |

**Input:** none beyond the Options mapping.

**Output:** Passthrough.

**Errors:** none beyond the operation's.

**Examples:**

```sh
sesshin migrate --dry-run | jq '.result | {from, to, sessions: (.changed | length), unconverted}'
sesshin migrate
```

Safe to run at any time, alongside running sessions, and again after an interruption: one with nothing pending changes nothing.

### sesshin-hook

Claude Code's entry point into sesshin: `sesshin-hook <verb>`, with the event's payload on stdin. It is a binary of its own, not a `sesshin` command, and it is not for people or agents. [`install`](#install) writes the command lines, and [hooks-spec.md](hooks-spec.md) specifies what each verb does.

**Synopsis:** `sesshin-hook <verb>`, where `<verb>` is one of the [registered verbs](hooks-spec.md#registration).

`sesshin-hook` is exempt from every global rule above. It follows the [hooks contract](hooks-spec.md#the-contract) instead:

- **It always exits `0`** (H1). That includes an unknown verb, a missing verb, extra arguments, and anything that looks like an option. It parses no flags: the first argument is the verb, and the rest are ignored. An unknown verb is logged and does nothing, so a `settings.json` written by a newer sesshin can't break a session run by an older one.
- **It writes no envelope,** and nothing at all to stdout or stderr (H3). `statusline` is the exception, and writes the status line.
- **It reads stdin unasked.** The payload always arrives there. Empty stdin is no payload, and the hook exits at once (`statusline` first prints its fallback line).
- **It has no `--help` and no `--input`.** `sesshin-hook --help` is an unknown verb. Hooks are documented in hooks-spec.md, not in help text, since their only caller can't read it.
- **It reads no `config.toml`.** Its one setting is in [`hooks.properties`](design-spec.md#hook-settings).

To try a hook by hand, give it a payload on stdin and point `HOME` at a scratch directory, as `install`'s self-test and the tests do. `HOME` relocates both directories on Linux and macOS; the XDG variables do on Linux only (see [Locations](design-spec.md#locations)):

```sh
env -u XDG_STATE_HOME -u XDG_CONFIG_HOME HOME=$(mktemp -d) sesshin-hook session-start < payload.json; echo $?   # always 0
```

## Pickers

Commands for people at a terminal, built on fzf, specified in [picker-spec.md](picker-spec.md): they draw on `/dev/tty`, and compose operations.

| Command | What it does | Built on |
|---|---|---|
| `sesshin restart` | Pick ended sessions and resume each in a new tab. | [`list`](operations.md#list), [`resume`](operations.md#resume) |
| `sesshin jump` | Pick a live session, the ones that want you first, and focus its window. | [`list`](operations.md#list), [`focus`](operations.md#focus) |

## Not included

- **`--config` and `SESSHIN_*` overrides.** There is one config per user, and one hook settings file. `SESSHIN_PICK_OPTS` is the one exception, and only restyles the picker's fzf ([fzf options](picker-spec.md#fzf-options)). Another location is reached through `HOME`, or on Linux `XDG_CONFIG_HOME` and `XDG_STATE_HOME`, which every entry point reads alike (see [Configuration](design-spec.md#configuration)).
- **Shell completion.**
- **Internal commands** for a picker to call back into sesshin: fzf's stdout is the selection.
- **`wait`.** Whether a session's work is done is the caller's judgment, not sesshin's data ([why](deferred/README.md#dropped-wait)).

The rest of this list, about the deferred commands and pickers, is in [deferred/cli-spec.md](deferred/cli-spec.md).
