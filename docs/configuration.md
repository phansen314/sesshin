# Configuration

sesshin works with no configuration at all. It has two optional files, one for each binary, a directory of [spawn templates](#templates), and a few environment variables that only restyle the pickers. The rules themselves are under [design-spec.md, Configuration](../specs/design-spec.md#configuration).

## Where the files live

Both files, and the templates, are in the config directory:

| | Config directory |
|---|---|
| Linux | `$XDG_CONFIG_HOME/sesshin`, else `~/.config/sesshin` |
| macOS | `~/Library/Application Support/sesshin` |

`XDG_CONFIG_HOME` counts only when it is an absolute path, and only on Linux: macOS ignores the XDG variables. What sesshin records goes in the state directory instead: on Linux `$XDG_STATE_HOME/sesshin`, else `~/.local/state/sesshin`; on macOS `~/Library/Application Support/sesshin/state` (see [Locations](../specs/design-spec.md#locations)).

Each process finds these from its own environment, and hooks get Claude's. If you set `XDG_CONFIG_HOME` or `XDG_STATE_HOME` in a shell profile that the program starting Claude doesn't read, the hooks and your commands can end up using different directories. Set them where both will see them, or not at all.

## `config.toml`: for the `sesshin` commands

Read by `prune`, `spawn`, `resume`, `restart`, `jump`, and `install`. Every key is optional, and a missing file means every default.

```toml
retain_days           = 30
retain_headless_hours = 24
spawn_shell           = ["/bin/zsh", "-l", "-i"]
```

| Key | Default | Allowed | What it changes |
|---|---|---|---|
| `retain_days` | `30` | an integer, 0 or more | How long `sesshin prune` keeps an ended session, counted from when it was last seen. `0` means never prune. |
| `retain_headless_hours` | `24` | an integer, 0 or more | The same for **headless** sessions: `claude -p` runs, and sessions started by another session, which can pile up by the hundred and are never resumed. `0` means keep them as long as any other session. |
| `spawn_shell` | your `$SHELL` with `-l -i`, or `/bin/sh -l -i` | a non-empty list of non-empty strings, the first an absolute path | The shell `spawn` and `resume` start `claude` through. A login, interactive shell gives `claude` the same `PATH` as a tab you open yourself. |

sesshin never prunes on its own, so the two retention keys matter only when you run `sesshin prune`. Run it by hand or from a timer ([examples](../specs/cli-spec.md#prune)). `sesshin prune --dry-run` shows what would go.

**A bad file stops the commands that read it.** An unknown key, a value of the wrong type, or one out of range makes the file corrupt. Those commands then fail with `corrupt`, and the message names the problem. Fix the file, or remove it to get the defaults.

## `hooks.properties`: for the hooks

Read by `sesshin-hook` every time Claude Code runs it. A missing file means the default.

```properties
# How long a hook waits for a lock, in milliseconds.
hook_lock_wait_ms=2000
```

| Key | Default | Allowed | What it changes |
|---|---|---|---|
| `hook_lock_wait_ms` | `2000` | an integer, 0 to 4000 | How long a hook waits for a lock another hook of the same session holds, before giving up and losing that event. A hook waits at most twice this for all its locks together. |

One `key=value` per line, with no spaces around `=`. Lines starting with `#`, and blank lines, are ignored.

You shouldn't need to change it. Hooks hold their locks for a few milliseconds, so a wait only runs out on a machine that is badly overloaded. Raise it if `hooks.log` shows waits running out; lower it if hooks are making Claude Code feel slow.

**A bad file is ignored silently.** An unknown key, a repeated key, a malformed line, or a value out of range makes every hook use the default and say nothing. A log line on every hook would bury everything else.

**Why a separate file?** The hook runs on every tool call, so it skips the time it would take to parse TOML ([Hook cost](../specs/design-spec.md#hook-cost)).

## Templates

A template is a JSON file that holds part of what you would type on every `spawn`: the model and permission flags, a standing prompt, a directory. `sesshin spawn --template opus` merges what you give it over `templates/opus.json`, so the file says what stays the same and the command line says what changes. Only the `sesshin` commands read templates, and no environment variable moves them.

A template meant only to pin the model, `templates/opus.json` in the config directory:

```json
{ "description": "Claude on Opus, auto permission mode", "args": ["--model", "opus", "--permission-mode", "auto"] }
```

```sh
sesshin spawn --template opus --prompt 'run the test suite and fix failures'   # in the working directory
sesshin spawn --template opus --cwd ~/code/web --job web-auth --prompt 'add the login form'
sesshin spawn --template opus --prompt-file /tmp/prompt-foobar.md -- --effort high   # a one-off prompt file, and more claude args
```

A template meant for many sessions at once shouldn't set `job`: a job is held by one session at a time.

**The file name is the template's name:** lowercase letters, digits, and hyphens, at most 64, not starting or ending with a hyphen, and not all digits, then `.json`. Lowercase only, so `Review` and `review` can't be one file on macOS. Only top-level regular files and symlinks count (a symlink is followed, wherever it leads; one that leads nowhere is listed by `sesshin templates` as a problem): anything else in `templates/`, including a file whose name isn't valid, is ignored.

| Key | What it holds |
|---|---|
| `description` | Free text, shown by `sesshin templates`. `spawn` ignores it. |
| `job`, `type`, `name`, `start_timeout_secs` | As the `spawn` options of the same names. |
| `cwd` | Where to start: an absolute path, or `~/` and a path, expanded against your home directory. |
| `prompt` | The first prompt. |
| `prompt_file` | A file holding the first prompt, relative to `templates/` and inside it (no `..`, no absolute path). Not with `prompt`. Suggested: `prompts/<name>.md`. |
| `args` | Arguments for `claude`, as after `--`. |
| `vars` | User variables for the new window. |

There is no `extra`, and a template can't name another template. An unknown key makes the template corrupt.

**How `spawn` merges it.** What you give wins:

| | |
|---|---|
| `job`, `cwd`, `type`, `name`, `start_timeout_secs` | Yours, else the template's, else the default. |
| `prompt` | Yours replaces the template's whole, whether it came from `prompt` or `prompt_file`. Nothing is appended. |
| `args` | The template's, then yours. |
| `vars` | Both, yours winning per key. |

`cwd` falls back last to the directory you ran `sesshin` in, so `--template review` in a repo starts there unless the template pins a directory. A `-i` file has no such fallback: it says where to start itself, or its template does. A `-i` file names its template with `"template"`; it has no `prompt_file`, so put the prompt's text in `prompt`:

```sh
jq -n --rawfile p /tmp/prompt-foobar.md '{template: "opus", prompt: $p}' | sesshin spawn -i -
```

**Two prompt files.** They are easy to mix up:

| | Path | Read by | Use |
|---|---|---|---|
| `--prompt-file` (the `spawn` option) | any path, or `-` for stdin | the command | a one-off prompt for this spawn |
| `prompt_file` (a template key) | relative to `templates/`, must stay inside it | `spawn`, when the template loads | a prompt saved with the template |

**Finding and checking them.** `sesshin templates` lists every template with its description and content. One that `spawn` would refuse is listed with a `problem` instead, so a bad file doesn't hide the rest:

```sh
sesshin templates | jq -r '.result.templates[] | [.name, .description // "-", .problem.detail // "ok"] | @tsv'
```

**When a template is wrong,** `spawn` fails before it opens anything: `not-found` when there is no such template (or its `prompt_file` is missing), and `corrupt` when the file, or the prompt file's contents, can't be used. The message names the file. Nothing was launched, so fix it and run again.

The session records the template it came from, by name: `sesshin show 12 | jq -r .result.session.template`. Not its contents, which you may have edited since.

## Checking your configuration

```sh
sesshin install --dry-run | jq '{ok, error}'
```

`install` reads both files and fails `corrupt` on either one if it is bad. That makes it the way to check a `hooks.properties` edit, since the hooks themselves won't tell you. With `--dry-run` it changes nothing.

## Environment variables

sesshin takes no settings from the environment. Every setting is in the two files, so your commands and the hooks see the same ones. The exceptions only change how the pickers look:

| Variable | What it does |
|---|---|
| `FZF_DEFAULT_OPTS`, `FZF_DEFAULT_OPTS_FILE` | Your fzf defaults apply to `restart` and `jump`: colors, borders, history. The pickers undo the few options that would change what gets picked ([fzf options](../specs/picker-spec.md#fzf-options)). |
| `SESSHIN_PICK_OPTS` | fzf options for sesshin's pickers alone, applied last, so they win: `SESSHIN_PICK_OPTS='--height 60% --layout reverse'`. |
| `XDG_CONFIG_HOME`, `XDG_STATE_HOME` | Where the config and state directories are (above). |
| `CLAUDE_CONFIG_DIR` | Where Claude Code's `settings.json` is, for `install` and `uninstall`; else `~/.claude`. |

`SESSHIN_JOB` and `SESSHIN_TOKEN` also appear in a spawned session's environment. They aren't settings: `spawn` and `resume` use them to hand a job to the session they launch, so leave them alone.
