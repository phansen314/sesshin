# Configuration

sesshin works with no configuration at all. It has two optional files, one for each binary, and a few environment variables that only restyle the pickers. The rules themselves are under [design-spec.md, Configuration](../specs/design-spec.md#configuration).

## Where the files live

Both files are in the config directory:

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
