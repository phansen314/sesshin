# Proposed fixes: review-operations.md, High findings

Proposals for the seven High findings in [review-operations.md](review-operations.md), as of 2026-10-02. No spec has been edited. Each proposal gives:

- a verdict on the finding;
- a one-line rationale;
- the exact replacement text for every spec section it touches, as **Replace** (the current text, verbatim) and **With**;
- alternatives worth considering;
- anything still to verify.

Where two proposals touch the same section (4 and 5 both rewrite Selecting a session; 6 and 7 both touch `terminal`), the later one says so and gives the merged text.

**Summary of verdicts.** All seven findings are real, and none is already settled by an earlier decision. Four of the review's suggested fixes need changes:

- **1:** Recording `backup_path` adds little. The ordering fix is what matters, and one path to the same loss remains, through a different state directory.
- **2:** kitty 0.49.1 probably already strips the paste-end sequence. The spec should still own the rule.
- **3:** `exec "$@"` doesn't work in fish. Also, `--` probably doesn't stop `claude` from treating a one-word prompt such as `purge` as a subcommand.
- **4:** "ambiguous" also has to apply across scopes, or a live-only operation silently picks one of the copies.
- **7:** Saying `send` and `focus` can only fail `unreachable` goes too far. They can also find no backend for the placement's tag, or find no `kitten`.

---

## 1. A crash during `install` can lose the user's own `statusLine`

**Verdict:** agree on the ordering. Recording `backup_path` adds little.

**Rationale:** write `install.json` first, so that no crash can leave a sesshin `statusLine` without a record of what it replaced.

Write-ahead ordering closes the crash window completely:

- If sesshin crashes after writing `install.json` but before writing `settings.json`, the record names a `statusLine` that is still in place.
- A re-install then sees a `statusLine` that isn't sesshin's. That needs `--replace-status-line` again, which records the same value.
- An `uninstall` in that state restores the value over itself, which is harmless.

Recording `backup_path` helps only when the record is lost, and then the path is lost along with it. `replaced_status_line` is already stored verbatim.

One way to reach "sesshin's `statusLine`, no record" remains, and it doesn't involve a crash. The user re-installs with a different state directory (an XDG variable set differently, or the state directory deleted, which `uninstall` invites: "what sesshin recorded is yours to delete"). The proposal covers that case with a warning instead of a silent `null`.

### operations.md, `install`

**Replace** (Kind):

```markdown
**Kind:** setup. Takes no sesshin lock; writes `settings.json` atomically, after a backup.
```

**With:**

```markdown
**Kind:** setup. Takes no sesshin lock. Writes `install.json`, then `settings.json`, each atomically, after a backup of `settings.json`.
```

**Replace** (Effects 2–4):

```markdown
2. **Back up** `settings.json` to `settings.json.sesshin-bak.<timestamp>`.
3. **Register** each hook, with this binary's absolute path (`os.Executable`, symlinks resolved), replacing any entry sesshin owns (any command ending in `sesshin hook <verb>`) and leaving every other entry, of any tool, exactly as it was. Set `statusLine` to `sesshin hook statusline`.
4. **Record** what uninstall and `doctor` need in `<state>/install.json`: the binary path and version, the time, the config and state directories and the `settings.json` it resolved, and the `statusLine` it replaced, if any. When the `statusLine` was already sesshin's (a re-install), `replaced_status_line` is carried forward from the existing `install.json`, so the one the first install replaced is still the one `uninstall` restores.
```

**With:**

```markdown
2. **Back up** `settings.json` to `settings.json.sesshin-bak.<timestamp>`.
3. **Record,** before changing `settings.json`, what uninstall and `doctor` need in `<state>/install.json`: the binary path and version, the time, the config and state directories and the `settings.json` it resolved, and `replaced_status_line`. That is the `statusLine` about to be replaced, when it isn't sesshin's. When it is already sesshin's (a re-install), the value is carried forward from the existing `install.json`, so the one the first install replaced is still the one `uninstall` restores. If there is no usable `install.json` to carry it from, it is `null`, with a `status-line-unrecorded` warning. Recording first means a crash can never leave sesshin's `statusLine` in place without a record of what it replaced.
4. **Register** each hook, with this binary's absolute path (`os.Executable`, symlinks resolved), replacing any entry sesshin owns (any command ending in `sesshin hook <verb>`) and leaving every other entry, of any tool, exactly as it was. Set `statusLine` to `sesshin hook statusline`, by the same absolute path.
```

(The "by the same absolute path" clause also resolves Low 36's first half. Drop it if Low 36 is handled separately.)

**Replace** (Warnings):

```markdown
**Warnings:** none.

**Retry safety:** safe; it converges. A crash leaves `settings.json` either as it was or fully written, since it is replaced atomically.
```

**With:**

```markdown
**Warnings:**

| Kind | When |
|---|---|
| `status-line-unrecorded` | The `statusLine` is already sesshin's, but no usable `install.json` says what it replaced (the state directory was deleted, or this run resolves a different one). `replaced_status_line` is recorded as `null`, so `uninstall` will remove the `statusLine` rather than restore one. The backups of `settings.json` may still hold the original. |

**Retry safety:** safe; it converges. Each file is replaced atomically, and `install.json` is written before `settings.json`. A crash between the two leaves a record of a `statusLine` that is still in place, and running `install` again rewrites both.
```

### operations.md, Warning kinds table

**Add** a row after `ack-not-written`:

```markdown
| `status-line-unrecorded` | [`install`](#install) found its own `statusLine` already in `settings.json`, with no usable `install.json` to say what it replaced. `uninstall` will remove the `statusLine` instead of restoring one. | `settings_path`; `backups`: the `settings.json.sesshin-bak.*` paths found beside it, newest first. |
```

### design-spec.md, `install.json`

**Replace:**

```markdown
What [`install`](operations.md#install) changed, so [`uninstall`](operations.md#uninstall) can undo it and [`doctor`](operations.md#doctor) can check it. Written only by `install`, atomically, with no lock: it changes only when you run `install`.
```

**With:**

```markdown
What [`install`](operations.md#install) changed, so [`uninstall`](operations.md#uninstall) can undo it and [`doctor`](operations.md#doctor) can check it. Written only by `install`, atomically, with no lock, and *before* `install` changes `settings.json`, so it never records less than `settings.json` holds. It changes only when you run `install`.
```

**Replace** (the `replaced_status_line` row's Source and Notes):

```markdown
| `replaced_status_line` | object or `null` | The `statusLine` that was in `settings.json`, if sesshin replaced one; carried forward from the previous `install.json` when the `statusLine` was already sesshin's | Restored by `uninstall`; `null` if there was none. A re-install must not overwrite it with `null`: the user's own statusline would then be lost at uninstall. |
```

**With:**

```markdown
| `replaced_status_line` | object or `null` | The `statusLine` in `settings.json` that this install is about to replace; carried forward from the previous `install.json` when the `statusLine` was already sesshin's | Restored by `uninstall`; `null` if there was none. Written before `settings.json` is changed, so a crash can't separate the two. A re-install must not overwrite it with `null`. When there is nothing to carry forward from (the state directory is gone, or resolves elsewhere), it is `null`, and `install` warns (`status-line-unrecorded`). |
```

**Alternatives:**

- **Recover from the backups.** When there is nothing to carry forward from, take `replaced_status_line` from the newest `settings.json.sesshin-bak.*` whose `statusLine` isn't sesshin's, and warn that it was recovered. That covers a deleted state directory. The risk is restoring a `statusLine` older than one the user set by hand and then replaced with sesshin's again. A warning that lists the backups is the more honest choice.
- **Also record `backup_path`, as the review suggests.** It is cheap, and a person may find it useful. It does nothing for `uninstall`, which already has the value verbatim.

---

## 2. `send`'s single bracketed paste can be broken out of by its own text

**Verdict:** agree. The concrete failure probably doesn't reproduce on current kitty, but the spec should own the guarantee.

**Rationale:** the only way out of a bracketed paste is an escape sequence. If `text` can't contain ESC or a C1 control, the text can't break out. Refusing those characters keeps "byte for byte" true for every text that is accepted.

**Evidence.** In kitty 0.49.1 (installed here), the frozen `kitty/rc/send_text.py` imports `sanitize_for_bracketed_paste` from `kitty.utils`. That function removes `ESC [201~` from pasted text, so `kitten @ send-text --bracketed-paste` probably already strips the end sequence. It doesn't touch other C0 controls. This is to verify by reading the source, and it isn't a contract the spec can cite.

**Refuse, don't strip.** Silently changing the text would break "delivered byte for byte", and a caller couldn't tell what arrived. Refusing is what an agent can act on. In practice it hits text with ANSI color codes, which would show up as junk in the prompt anyway.

### operations.md, `send`

**Replace** (the `text` property in the Input schema):

```json
    "text": { "type": "string", "minLength": 1, "maxLength": 1048576, "description": "Delivered byte for byte: backslashes, quotes, and line breaks survive, and several lines arrive as one prompt." },
```

**With:**

```json
    "text": { "type": "string", "minLength": 1, "maxLength": 1048576, "description": "Delivered byte for byte, as one bracketed paste: backslashes, quotes, tabs, and line breaks survive, and several lines arrive as one prompt. No control characters other than tab, line feed, and carriage return (see Additional validation)." },
```

**Replace** (Additional validation):

```markdown
**Additional validation:** `text` is valid UTF-8.
```

**With:**

```markdown
**Additional validation:** `text` is valid UTF-8. It contains no control character other than tab (U+0009), line feed (U+000A), and carriage return (U+000D). In particular it contains no ESC (U+001B), no C1 control (U+0080–U+009F), and no DEL (U+007F). The paste is the guarantee that nothing in `text` acts as a keystroke, and an escape sequence is the one way out of a paste: `ESC [201~`, or the 8-bit `U+009B 201~`, would end it early, and everything after would arrive as keys, a line break among them pressing Enter. sesshin enforces this itself, whatever the backend does. One problem is reported, at `/text`, naming the first such character and its byte offset.
```

**Replace** (Effects, the sentence about pasting):

```markdown
Otherwise it pastes `text` as one bracketed paste, then, if `submit`, sends Enter as a separate keystroke.
```

**With:**

```markdown
Otherwise it pastes `text` as one bracketed paste, bracketed whatever paste mode the window reports, then, if `submit`, sends Enter as a separate keystroke.
```

**Replace** (Errors, `invalid-input` row):

```markdown
| `invalid-input` | `session` or `text` is missing or empty, or `text` is too long or not UTF-8. |
```

**With:**

```markdown
| `invalid-input` | `session` or `text` is missing or empty, or `text` is too long, not UTF-8, or contains a control character other than tab, line feed, or carriage return. |
```

### design-spec.md, Placement (kitty backend)

**Add** a bullet after **Searched socket by socket**:

```markdown
- **Pasted with bracketing forced on.** `send` pastes with `kitten @ send-text --bracketed-paste enable`, not `auto`. Claude Code's prompt accepts bracketed paste, and with `auto` a window whose paste mode reads as off would get the text unbracketed, where every line break presses Enter. kitty also strips the paste-end sequence from text it brackets, but sesshin doesn't rely on that: [`send`](operations.md#send) refuses text that holds an escape character at all.
```

### cli-spec.md, `send`

**Replace** (Input):

```markdown
**Input:** `--text-file` reads the file's contents exactly, as ftask's `--notes-file` does. That includes a trailing newline, which arrives as part of the one bracketed paste rather than as an extra Enter. `--text-file -` suits text produced by another command and needs no shell escaping.
```

**With:**

```markdown
**Input:** `--text-file` reads the file's contents exactly, as ftask's `--notes-file` does. That includes a trailing newline, which arrives as part of the one bracketed paste rather than as an extra Enter. `--text-file -` suits text produced by another command and needs no shell escaping. Text holding other control characters, such as terminal color codes, is refused (`invalid-input`, `/text`), so strip them first, e.g. `git --no-pager diff --no-color | sesshin send api --text-file -`.
```

**Alternatives:**

- **Strip instead of refuse.** Remove the forbidden characters, and report the count in a new warning. This is friendlier to piped tool output, but it gives up "byte for byte", and an agent may not notice the warning.
- **Forbid only ESC and U+009B.** These are the minimum that closes the escape, and the review's "at minimum". Other C0 controls inside a paste can't act as keys. How Claude Code's input renders them is unspecified, though, and nothing legitimate sends them.
- **Keep `--bracketed-paste auto`.** Choose this if `enable` turns out to misbehave in some Claude Code state, such as a full-screen dialog that turns paste mode off. In that state, though, the text shouldn't be sent at all without `force`.

**To verify:** that kitty 0.49's `send-text` applies `sanitize_for_bracketed_paste`, and with which `--bracketed-paste` values. This is for the design-spec sentence only; the operations rule doesn't depend on it.

---

## 3. How `spawn` and `resume` build the shell command is unspecified, and a prompt can be read as a flag

**Verdict:** agree, with two problems the review's fix misses:

- **fish.** `$SHELL -l -i -c 'exec "$@"' sesshin claude …` is a syntax error in fish, which has no `$@` or `$0` slot and puts the arguments after `-c`'s script in `$argv`. sesshin has to know which family the shell belongs to.
- **Subcommands.** `claude --help` (2.1.288) shows `Usage: claude [options] [command] [prompt]`, with the subcommands `agents attach auth auto-mode doctor gateway import install logs mcp plugin plugins purge respawn rm setup-token stop kill ultrareview update upgrade`. `claude` uses commander, and commander's default puts everything after `--` into the operands, then still dispatches a subcommand when the first operand names one. So `-- <prompt>` probably protects `-v is broken`, but not a prompt that is exactly `purge`, which "Delete[s] all Claude Code state for a project", or `update`. This is to verify. The proposal makes the rejection rule conditional on that check.

**Rationale:** the shell is there only to supply the login environment (`PATH`), so it must never parse what sesshin or the caller supplies. `kitten @ launch` already takes an argument vector, so the only thing left to fix is the shell's `-c` script.

### operations.md, a new shared rule

**Add** a subsection under Shared rules, after [Session order](operations.md#session-order):

```markdown
### Launching `claude`

[`spawn`](#spawn) and [`resume`](#resume) start `claude` in the new window through the user's login shell, so that it gets the same `PATH` and environment as a tab opened by hand. No shell ever parses any part of the command line:

- **The arguments are passed out of band.** The backend launches `spawn_shell` (see [Configuration](design-spec.md#configuration)) with its own arguments, then `-c`, a fixed script, then the argument vector:
  - a POSIX-family shell: `<spawn_shell…> -c 'exec "$@"' sesshin claude <arg…>` (`sesshin` fills `$0`);
  - `fish`, judged by the basename of `spawn_shell`'s first word: `<spawn_shell…> -c 'exec $argv' claude <arg…>`.

  Nothing sesshin or the caller supplies is put into the script, so `$(…)`, quotes, globs, and `~` in a prompt or an argument reach `claude` exactly as given.
- **The prompt follows `--`.** `spawn` passes `<template args…> <input args…> -- <prompt>`, or no `--` and no prompt when there is no prompt. A prompt that begins with `-` is then never read as an option, and an `args` list that ends in an option taking a value (`--model`) can't consume it. `resume` passes `--resume <uuid>` and nothing else.
- **`args` are claude's business.** sesshin passes them in order and never interprets them. An `args` list that ends in an option taking a value makes `claude` take `--` as that value and fail visibly in the new window.
```

### operations.md, `spawn`

**Replace** (Effects 2):

```markdown
2. Open a new tab, split, or OS window through the backend, in `cwd`, titled `title`, with `SESSHIN_JOB=<job>` and `SESSHIN_TOKEN=<token>` in its environment and `vars` set as the window's user variables (kitty's `--var`, for matching windows; not environment variables), running `claude <args…> [prompt]` through `spawn_shell` (default `$SHELL -l -i`), so it gets the same `PATH` as a tab opened by hand.
```

**With:**

```markdown
2. Open a new tab, split, or OS window through the backend, in `cwd`, titled `title`, with `SESSHIN_JOB=<job>` and `SESSHIN_TOKEN=<token>` in its environment and `vars` set as the window's user variables (kitty's `--var`, for matching windows; not environment variables). It runs `claude <args…> [-- <prompt>]` as [Launching `claude`](#launching-claude) says: through `spawn_shell`, with no argument ever parsed by a shell.
```

**Replace** (Additional validation):

```markdown
**Additional validation:** `job` follows the [job name](#selecting-a-session) rule. `cwd` is absolute. `template` is a name (the job name rule), not a path. After merging the template (below), `cwd` is set.
```

**With** (the last sentence only if the verification below shows `--` doesn't stop subcommand dispatch):

```markdown
**Additional validation:** `job` follows the [job name](#selecting-a-session) rule. `cwd` is absolute. `template` is a name (the job name rule), not a path. After merging the template (below), `cwd` is set. The merged `prompt` is not a single word of lowercase letters and hyphens (`^[a-z][a-z-]*$`): `claude` would run a one-word prompt that names one of its subcommands (`purge`, `update`, `install`, …) as that subcommand, even after `--`, and sesshin doesn't keep a list of them. Add a word.
```

### operations.md, `resume`

**Replace** (Effects 2, last sentence):

```markdown
It runs `claude --resume <uuid>` through `spawn_shell`.
```

**With:**

```markdown
It runs `claude --resume <uuid>` as [Launching `claude`](#launching-claude) says.
```

### design-spec.md, Configuration

**Replace** (in the TOML block):

```toml
spawn_shell           = ""      # "" means $SHELL -l -i
```

**With:**

```toml
spawn_shell           = ""      # "" means "$SHELL -l -i"; see below
```

**Add** a paragraph after the TOML block, before "An unknown key…":

```markdown
**`spawn_shell`** is the login shell that [`spawn`](operations.md#spawn) and [`resume`](operations.md#resume) start `claude` through, so it gets the `PATH` a tab opened by hand would have. It is split on whitespace, with no quoting. `""` means `$SHELL -l -i`, or `/bin/sh -l -i` when `SHELL` is unset or not absolute. sesshin appends `-c`, a fixed script, and the argument vector ([Launching `claude`](operations.md#launching-claude)). The script is POSIX `exec "$@"`, or `exec $argv` when the first word's basename is `fish`. A login shell of neither family (nushell, xonsh, elvish) needs `spawn_shell = "bash -l -i"` or similar.
```

### cli-spec.md, `spawn`

**Replace:**

```markdown
The job is an option, not an argument, because it is optional: `sesshin spawn` with no job is a valid, unnamed session. Everything after `--` goes to `claude` untouched, so sesshin never has to know its flags.
```

**With:**

```markdown
The job is an option, not an argument, because it is optional: `sesshin spawn` with no job is a valid, unnamed session. Everything after `--` goes to `claude` untouched, so sesshin never has to know its flags. sesshin puts its own `--` before the prompt, so `--prompt '-v is broken'` reaches `claude` as a prompt, and no shell expands `$(…)` or quotes in it.
```

**Alternatives:**

- **POSIX single-quote each argument into the `-c` string** instead of passing positional parameters. This works in any POSIX shell, but it is still wrong for fish, whose quoting rules differ (`\'` inside single quotes). Passing positional parameters avoids quoting rules altogether.
- **Make `spawn_shell` a TOML array** (`["bash", "-l", "-i"]`). That would be exact even for a shell path that contains spaces, but it changes the key's type. Splitting a string on whitespace is enough for real shell paths.
- **For the subcommand risk,** if verification shows `claude -- purge` dispatches `purge`, a narrower rule would reject only a prompt that is one word and also appears in `claude --help`'s command list at spawn time. It is precise, but it costs a `claude --help` run per spawn and parses text that `claude` doesn't promise to keep stable. The regex rule above is blunt and cheap.

**To verify** on the target Claude Code version, in a scratch directory:

- that `claude -- '-v is broken'` starts a session with that prompt;
- whether `claude -- doctor` runs the subcommand. Use `doctor`, never `purge`.

---

## 4. A duplicated sesshin ID has three contradictory outcomes

**Verdict:** agree. The review's rule needs one addition: count the copies across scopes.

Consider the review's rule without it. ID 12 names a live session A and an ended session B. A live-only operation (`send 12`) sees one match in scope and acts on A, though the `12` in someone's note may have meant B. A duplicate exists only after an outside change, and at that point the handle no longer identifies a session. Every use of it should then stop and ask.

**Rationale:** "which one did you mean?" is exactly what `ambiguous` and its `candidates` say. A second kind (`conflict` `duplicate-id`) for the same question only adds a precedence clash.

### operations.md, Error kinds

**Replace** (the `ambiguous` row):

```markdown
| `ambiguous` | A selector matched more than one session. | `selector`; `candidates`: the matching sessions as [session refs](#session-ref), at most 20. |
```

**With:**

```markdown
| `ambiguous` | A selector matched more than one session, or is a sesshin ID that several sessions carry. | `selector`; `candidates`: the matching sessions as [session refs](#session-ref), at most 20. For a duplicated sesshin ID, every copy, live or ended, and the message suggests [`doctor`](#doctor). |
```

**Replace** (in the `conflict` row's `rule` list):

```markdown
`no-placement` (sesshin doesn't know where the session runs), `duplicate-id` (the sesshin ID names several sessions), `status-line-taken`
```

**With:**

```markdown
`no-placement` (sesshin doesn't know where the session runs), `status-line-taken`
```

### operations.md, Warning kinds

**Replace:**

```markdown
| `duplicate-id` | Several sessions carry the same sesshin ID. Each is still shown. | `id`; `sessions`: session refs. |
```

**With:**

```markdown
| `duplicate-id` | Several sessions carry the same sesshin ID. [`list`](#list) still shows each; selecting that ID is `ambiguous`. | `id`; `sessions`: session refs. |
```

### operations.md, Selecting a session

**Replace** the last two bullets:

```markdown
- **One or `ambiguous`.** Several matches in scope are `ambiguous`, listing them. A job names at most one live session, as readers report jobs (see [Reservations](design-spec.md#reservations)), so `job:` within live scope never is.
- **A duplicated sesshin ID** refuses a write with `conflict` (`rule`: `duplicate-id`): it must know which session it acts on. A read reports every copy, with a `duplicate-id` warning.
```

**With:**

```markdown
- **One or `ambiguous`.** Several distinct sessions matching in scope are `ambiguous`, listing them. A job names at most one live session, as readers report jobs (see [Reservations](design-spec.md#reservations)), so `job:` within live scope never is.
- **A sesshin ID is judged across every session, whatever the scope.** A sesshin ID that several sessions carry (only an [outside change](design-spec.md#assumptions) does that; see [Sesshin IDs](design-spec.md#sesshin-ids)) is `ambiguous` for every operation, read or write, even when only one copy is in scope: the handle no longer says which session was meant. Every copy is a candidate; select one by UUID. [`list`](#list) still shows every copy, with a `duplicate-id` warning.
```

### operations.md, `show`

**Replace** (Warnings):

```markdown
| `unusable-file` | One of the session's files is unusable. |
| `duplicate-id` | The selected sesshin ID names several sessions. |
```

**With:**

```markdown
| `unusable-file` | One of the session's files is unusable. |
```

### operations.md, `send`, `focus`, `resume` Errors

**Replace** (`send`):

```markdown
| `conflict` | `not-live` (an ended session's ID or UUID), `no-placement`, `busy` (without `force`), `duplicate-id`. |
```

**With:**

```markdown
| `conflict` | `not-live` (an ended session's ID or UUID), `no-placement`, `busy` (without `force`). |
```

**Replace** (`focus`):

```markdown
| `conflict` | `not-live`, `no-placement`, `duplicate-id`. |
```

**With:**

```markdown
| `conflict` | `not-live`, `no-placement`. |
```

**Replace** (`resume`):

```markdown
| `conflict` | `live` (the session is running), `job-taken` (the job is held by another live session or a fresh reservation; its message suggests `job`), `duplicate-id`. |
```

**With:**

```markdown
| `conflict` | `live` (the session is running), `job-taken` (the job is held by another live session or a fresh reservation; its message suggests `job`). |
```

In the `show`, `send`, `focus`, and `resume` tables, the `not-found`, `ambiguous` row's "or several" already covers a duplicated ID, so it needs no change.

### design-spec.md, Sesshin IDs

**Replace:**

```markdown
- **Unique** across the state directory. A duplicate can come only from an outside change, such as a restored `state.json` with a lower `last_id`; [`doctor`](operations.md#doctor) reports it, and an operation given a duplicated ID refuses it with `conflict`.
```

**With:**

```markdown
- **Unique** across the state directory. A duplicate can come only from an outside change, such as a restored `state.json` with a lower `last_id`. [`doctor`](operations.md#doctor) reports it, and an operation given a duplicated ID fails with `ambiguous`, listing every copy, so it is selected by UUID instead ([Selecting a session](operations.md#selecting-a-session)).
```

**Alternative:** keep `conflict` (`duplicate-id`) and move it ahead of `ambiguous` in Precedence, so a duplicate is never reported as an ordinary ambiguity. That makes sense only if a caller needs to tell "the state is damaged" apart from "be more specific". The `ambiguous` message pointing to `doctor`, plus the `duplicate-id` warning from `list` and the `doctor` finding, already say that.

---

## 5. The selector forms overlap, and nothing says which one wins

**Verdict:** agree. I adopt the review's suggested rule, with three additions:

- A full UUID is its own form.
- A prefixed selector such as `job:x` always means that form, and a title that itself starts with `job:` is selected as `name:job:x`.
- The all-digits UUID prefix (about 2% of UUIDs, `(10/16)^8`) is reached with `uuid:`.

**Rationale:** decide the form by shape, then match the union. Every selector then has exactly one reading, and an overlap between kinds shows up as `ambiguous` instead of a silent choice.

### operations.md, Selecting a session (the whole section through the bullets, merged with proposal 4)

**Replace** from the table through the last bullet (the current table, and the bullets **Exact, never fuzzy** through **A duplicated sesshin ID**) **with:**

```markdown
| Form | Matches |
|---|---|
| `12`: all digits | Only the session whose [sesshin ID](design-spec.md#sesshin-ids) is 12. Never a UUID prefix or a name. `#12` is the display form, not a selector. |
| `0b6c5a3e-1f7e-4c2b-9a51-6d2f0e8b7c11`: a full UUID | Only the session with that UUID. |
| Anything else: `api`, `0b6c5a3e`, `api refactor` | Every session it names in any of these ways: its UUID starts with it (when it is 8 or more characters of hex digits and dashes); its job; its `/rename` title; its statusline session name. |
| `id:12`, `uuid:0b6c5a3e`, `job:api`, `name:api` | Only that kind of match. `id:` takes a sesshin ID; `uuid:` a full UUID or a prefix of 8 or more characters; `job:` a job; `name:` a `/rename` title or statusline session name. For scripts, and for a value another form would claim: a title that is all digits (`name:2026`), or a UUID prefix that is (`uuid:12345678`). |

- **The form is decided by shape, then matched as a union.** A selector beginning with `id:`, `uuid:`, `job:`, or `name:` is always that form, so a title that itself begins with one is selected as `name:job:x`. Otherwise, all digits is a sesshin ID and a full UUID is a UUID. Anything else is matched every way the table lists, and the distinct sessions found are its matches: `cafe1234` is both a possible job and a UUID prefix, and is `ambiguous` when the two name different sessions.
- **Exact, never fuzzy.** A selector matches whole values, or a UUID prefix. Fuzzy matching is the pickers' job (fzf), not an operation's.
- **Case.** UUIDs and UUID prefixes match without regard to case, since directories are named in lowercase. Jobs, titles, and session names match exactly, case included.
- **Jobs match as readers report them.** A live session whose stored job a live session that started earlier holds reports `job` `null` (see [Reservations](design-spec.md#reservations)), and no job selector selects it.
- **Scope.** Each operation says whether it selects among live sessions, ended ones, or both. Live includes liveness `unknown`, everywhere. A selector that matches nothing in scope is `not-found`, even if it matches outside it — except that a live-only operation given a sesshin ID or UUID of an ended session fails with `conflict` (`rule`: `not-live`), which says more.
- **One or `ambiguous`.** Several distinct sessions matching in scope are `ambiguous`, listing them. A job names at most one live session, as readers report jobs, so `job:` within live scope never is.
- **A sesshin ID is judged across every session, whatever the scope.** A sesshin ID that several sessions carry (only an [outside change](design-spec.md#assumptions) does that; see [Sesshin IDs](design-spec.md#sesshin-ids)) is `ambiguous` for every operation, read or write, even when only one copy is in scope: the handle no longer says which session was meant. Every copy is a candidate; select one by UUID. [`list`](#list) still shows every copy, with a `duplicate-id` warning.
```

### cli-spec.md, Selectors on the command line

**Replace:**

```markdown
- **A sesshin ID is bare digits:** `sesshin show 12`. `#12` is how sesshin displays an ID, not an input form. It would be a poor one anyway: at the start of a word, `#` begins a shell comment, so `sesshin show #12` runs `sesshin show` with no argument.
```

**With:**

```markdown
- **A sesshin ID is bare digits:** `sesshin show 12`. `#12` is how sesshin displays an ID, not an input form. It would be a poor one anyway: at the start of a word, `#` begins a shell comment, so `sesshin show #12` runs `sesshin show` with no argument. Bare digits are always an ID, so a UUID prefix or a title that is all digits needs its prefix: `sesshin show uuid:12345678`, `sesshin show name:2026`.
```

**Replace** (in the `show` examples):

```sh
sesshin show job:12-factor           # a job that looks like a number, typed
```

**With:**

```sh
sesshin show name:2026               # a /rename title that is all digits
```

(`12-factor` isn't all digits, so under either the old rule or this one it never needed `job:`. The old comment teaches the wrong lesson.)

**Alternatives:**

- **Priority order instead of a union.** Try ID, then UUID prefix, then job, then title, then name, and take the first form that matches. It never reports `ambiguous` across forms, but `cafe1234` would silently pick a UUID prefix over a job someone named `cafe1234`. A union is safer for `send`.
- **`name:` as the derived `name`,** which also covers job, `#id`, and the UUID prefix. This would let a person paste whatever a picker displays, but `#12` would then be a selector by the back door. Keep `name:` to the two Claude-reported names.

---

## 6. The precedence model can't be followed by `spawn` or `resume`

**Verdict:** agree. I also propose moving `terminal` (`unavailable`) for `spawn` and `resume` up to step 2.

The caller's environment is known before anything else. If `spawn` checks it last, it has already claimed a reservation it can't launch, and it then has to remove the reservation again. Checked early, a `spawn` from ssh or cron creates nothing. This depends on proposal 7's definition of `unavailable`.

**Rationale:** name the resolution phase that runs before any lock. Everything an operation can learn without a lock (the input, the environment, the selected session, the template) is then judged first, and only what the lock protects is judged under it.

### operations.md, Error kinds

**Replace** (the `invalid-input` row's Meaning):

```markdown
| `invalid-input` | Input failed validation. Raised before any lock is sought or any file is read. Reports every problem, not just the first. |
```

**With:**

```markdown
| `invalid-input` | Input failed validation. Raised before any lock is sought. A rule on the input alone is judged before any file is read. A rule that holds only after merging a template (for [`spawn`](#spawn)) is judged after the template's own errors (see [Precedence](#precedence)). Reports every problem found at that point, not just the first. |
```

### operations.md, Precedence

**Replace** (the whole section body):

```markdown
An operation reports one error. When several apply, it reports the first of:

1. `invalid-input`.
2. `environment`, then `corrupt` for the config.
3. `busy`.
4. `not-found`, `ambiguous` — the selector or template the input names.
5. `corrupt` for any other file the operation needs.
6. `conflict`.
7. `terminal`.

Steps 4–7 are checked under the operation's lock, when it takes one. Setup and diagnostic operations define their own order.
```

**With:**

```markdown
An operation reports one error. When several apply, it reports the first of:

1. `invalid-input` on the input alone.
2. `environment`, then `corrupt` for the config, then `terminal` (`unavailable`) for an operation that opens a window in the caller's terminal ([`spawn`](#spawn), [`resume`](#resume)).
3. **Resolution,** with no lock: `not-found` and `ambiguous` for the selector; `not-found` and `corrupt` for a template; `invalid-input` on the input merged with the template; `not-found` for a path the input names.
4. `busy`.
5. `corrupt` for any other file the operation needs, and `not-found` for a selected session gone when it is read again under the lock.
6. `conflict`.
7. `terminal`, any other reason.

Steps 5–7 are judged under the operation's lock, when it takes one, so a check the lock protects (a job's holder) is never judged on a stale read. Whether an operation takes the lock may depend on what resolution found (a [`resume`](#resume) takes it only to claim a job). An operation that takes no lock judges steps 5–7 right after resolution. Setup and diagnostic operations define their own order.
```

### operations.md, `spawn` Errors

**Replace** the whole table:

```markdown
| Kind | When |
|---|---|
| `invalid-input` | A bad `job`, `template` name, `cwd`, `type`, or `start_timeout_secs`; or no `cwd` after merging. |
| `environment`, `corrupt` | `HOME` is unusable, or the config is corrupt. |
| `busy` | (`lock`: `state`) Another `spawn`, a `prune`, or a `repair` held the state lock for longer than `cli_lock_wait_ms`. |
| `not-found` | The template, or `cwd`, doesn't exist. |
| `corrupt` | The template is corrupt. |
| `conflict` | (`rule`: `job-taken`) A live session or fresh reservation holds `job`. |
| `terminal` | No backend recognizes the caller's terminal (`unavailable`), or the launch failed (`launch-failed`). |
```

**With:**

```markdown
| Kind | When |
|---|---|
| `invalid-input` | A bad `job`, `template` name, `cwd`, `type`, or `start_timeout_secs`. |
| `environment`, `corrupt` | `HOME` is unusable, or the config is corrupt. |
| `terminal` | (`reason`: `unavailable`) No backend recognizes the caller's terminal. |
| `not-found` | (`templates`) The template doesn't exist. |
| `corrupt` | The template is corrupt. |
| `invalid-input` | (`/cwd`) No `cwd` after merging the template. |
| `not-found` | (`paths`) `cwd` doesn't exist. |
| `busy` | (`lock`: `state`) Another `spawn`, a `prune`, or a `repair` held the state lock for longer than `cli_lock_wait_ms`. |
| `conflict` | (`rule`: `job-taken`) A live session or fresh reservation holds `job`. |
| `terminal` | (`reason`: `launch-failed`) The launch failed. |
```

The template is read before the state lock. That is what makes a template's `job` available to the claim, and it is why template errors precede `busy`.

### operations.md, `resume` Errors

**Replace** the whole table:

```markdown
| Kind | When |
|---|---|
| `invalid-input` | `session` is missing or empty, `job` isn't a job name, or a bad `start_timeout_secs`. |
| `environment`, `corrupt` | `HOME` is unusable, or the config is corrupt. |
| `not-found`, `ambiguous` | `session` matches no ended session, or several. |
| `busy` | (`lock`: `state`) It resumes under a job, and the state lock was held for longer than `cli_lock_wait_ms`. |
| `conflict` | `live` (the session is running), `job-taken` (the job is held by another live session or a fresh reservation; its message suggests `job`), `duplicate-id`. |
| `terminal` | No backend here (`unavailable`), or the launch failed (`launch-failed`). |
```

**With** (merged with proposal 4, so without `duplicate-id`):

```markdown
| Kind | When |
|---|---|
| `invalid-input` | `session` is missing or empty, `job` isn't a job name, or a bad `start_timeout_secs`. |
| `environment`, `corrupt` | `HOME` is unusable, or the config is corrupt. |
| `terminal` | (`reason`: `unavailable`) No backend recognizes the caller's terminal. |
| `not-found`, `ambiguous` | `session` matches no ended session, or several. |
| `busy` | (`lock`: `state`) It resumes under a job, and the state lock was held for longer than `cli_lock_wait_ms`. |
| `not-found` | The session was pruned between being selected and the lock. |
| `conflict` | `live` (the session is running), `job-taken` (the job is held by another live session or a fresh reservation; its message suggests `job`). |
| `terminal` | (`reason`: `launch-failed`) The launch failed. |
```

### operations.md, `resume` Kind

**Replace:**

```markdown
**Kind:** write. Takes the state lock while it claims the session's job, as [`spawn`](#spawn) does, and holds no lock across the launch.
```

**With:**

```markdown
**Kind:** write. Selects the session with no lock. Then, if it resumes under a job, it takes the state lock while it claims the job, as [`spawn`](#spawn) does, and holds no lock across the launch.
```

(The rest of that paragraph, "It writes no session file: …", stays.)

**Alternatives:**

- **Keep `terminal` (`unavailable`) at step 7.** It is still checked before the claim, so no reservation is created, but it is reported only when nothing else applies. This keeps the precedence list purely "cheap checks first", at the cost of an implementation that checks early and reports late. I'd rather report what was checked first.
- **Keep the review's numbering,** with `busy` at 4 and re-checks at 5. My version above is the same idea. It only adds the pruned-session `not-found` and the post-merge `invalid-input` placement explicitly.

---

## 7. `terminal` (`unavailable`) for `send` and `focus` contradicts the design

**Verdict:** agree. The review's fix (for `send` and `focus`, "the only failure … is `unreachable`") goes too far. `send` and `focus` can't act when:

- the placement's `terminal` tag names a backend this binary doesn't have (a placement written by a newer sesshin with a tmux backend);
- the backend's program can't be run: no `kitten` on `PATH` under cron or ssh, which is a likely case.

Neither is "the window couldn't be found". So `unavailable` keeps a meaning for `send` and `focus`, but that meaning is about the placement and this binary, never about the caller's environment.

**Rationale:** `unavailable` means no backend can act at all; `unreachable` means the backend ran but couldn't find or verify the window. The difference is that the caller's environment matters only to the operations that open a window in it.

### operations.md, Operation kinds

**Replace:**

```markdown
Operations that act on the terminal ([`spawn`](#spawn), [`send`](#send), [`focus`](#focus), [`resume`](#resume)) do so through the session's [terminal backend](design-spec.md#placement), and fail with `terminal` when it can't.
```

**With:**

```markdown
Operations that act on the terminal do so through a [terminal backend](design-spec.md#placement), and fail with `terminal` when it can't. [`spawn`](#spawn) and [`resume`](#resume) open a window in the *caller's* terminal, so they use the backend that recognizes the caller's environment. [`send`](#send) and [`focus`](#focus) act on the session's window, so they use the backend its placement names, wherever the caller runs (ssh and cron included).
```

### operations.md, Error kinds

**Replace** (the `terminal` row):

```markdown
| `terminal` | The terminal backend could not do what was asked. | `reason`: `unavailable` (no backend recognizes this environment: not in kitty, or remote control off), `unreachable` (the session's window could not be found or reached, or, for `send`, could not be verified as the session's), `launch-failed` (a new tab or window could not be opened), `command-failed` (the backend's command failed otherwise). `terminal`: the backend's tag, or `null`; `detail`: human-readable. |
```

**With:**

```markdown
| `terminal` | The terminal backend could not do what was asked. | `reason`: `unavailable` (no backend can act: for `spawn` and `resume`, none recognizes the caller's environment, which is not kitty, or kitty with remote control off; for `send` and `focus`, this binary has no backend for the placement's `terminal`; for any of them, the backend's program, e.g. `kitten`, can't be run), `unreachable` (the backend ran, but the session's window could not be found or reached, or, for `send`, could not be verified as the session's), `launch-failed` (a new tab or window could not be opened), `command-failed` (the backend's command failed otherwise). `terminal`: the backend's tag, or `null`; `detail`: human-readable. |
```

### operations.md, `send` Errors

**Replace:**

```markdown
| `terminal` | The backend is unavailable here, or the window can't be reached, or pasting failed. |
```

**With:**

```markdown
| `terminal` | `unavailable` (no backend for the placement's terminal, or its program can't be run), `unreachable` (no window found through the stored socket or its siblings whose foreground process is the session's), or `command-failed` (pasting failed; see Retry safety). Never because of where the caller runs. |
```

### operations.md, `focus` Errors

**Replace:**

```markdown
| `terminal` | The backend is unavailable, or the window can't be reached or focused. |
```

**With:**

```markdown
| `terminal` | `unavailable` (no backend for the placement's terminal, or its program can't be run), `unreachable` (no socket answers, so not even the stored window can be tried), or `command-failed` (the window couldn't be focused). Never because of where the caller runs. |
```

(`spawn`'s and `resume`'s `terminal` rows are replaced under proposal 6. Their `unavailable` already refers to the caller's terminal.)

### cli-spec.md, `info` examples

**Replace:**

```sh
sesshin info | jq -e '.result.terminal == "kitty"'   # can spawn, send, and focus from here
```

**With:**

```sh
sesshin info | jq -e '.result.terminal == "kitty"'   # can spawn and resume from here; send and focus work from anywhere
```

### design-spec.md

No change is needed. The Placement section's **Searched socket by socket** already says that `send` and `focus` work "from a CLI outside kitty (ssh, cron)". These edits make operations.md agree with it.

**Alternative:** add a separate `reason`, `no-backend`, for an unknown placement tag or a missing `kitten`, and keep `unavailable` strictly for "the caller's environment". That is more precise for a caller branching on `reason`. But no caller would handle those cases differently, since each means "sesshin can't drive that terminal from this process", and it adds a value to the contract.

---

## Not proposed here

Several Medium and Low findings sit next to these proposals:

- 12: `uninstall` restores only a `statusLine` that is still sesshin's.
- 21: the spawned window's environment.
- 25: the job name rule's wording.
- 36: `install` order, and herd entries.

The proposals above don't depend on them. Only the "same absolute path" clause in proposal 1 overlaps (with 36), as noted there.
