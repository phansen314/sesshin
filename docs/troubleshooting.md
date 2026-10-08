# Troubleshooting

Each heading below is something you might see, followed by why it happens and what to do. When a command fails, its JSON says why: `.error.kind`, and for most kinds `.error.details`. The kinds are listed in [operations.md, Error kinds](../specs/operations.md#error-kinds), and the warnings in [Warning kinds](../specs/operations.md#warning-kinds).

## "No conversation found" when resuming, or a session missing from `list`

**Cause:** the kitty that session ran in was started from inside a Claude session, by `kitty &` from an agent or from the shell Claude gives its tools. Claude Code marks every process it starts with variables such as `CLAUDECODE=1` and `CLAUDE_CODE_CHILD_SESSION`, and that kitty passes them to every window it opens. A `claude` in such a window:

- saves no transcript, and says so under its prompt, so `claude --resume` and `sesshin resume` find no conversation (`resume` warns `transcript-missing`);
- is recorded as nested, as a `claude -p` run by a tool would be. It gets no job and no window, `list` and `restart` hide it unless you ask for every session, and `prune` removes it after a day.

**Fix:** quit that kitty and start one from your desktop, a launcher, or a login shell. To start one from a shell that might be Claude's, remove the markers first:

```sh
env $(env | sed -n 's/^\(CLAUDE[A-Z_]*\)=.*/-u \1/p') kitty --detach
```

A session that has already run that way can't be fixed: it has no transcript to resume. [Don't start kitty from a Claude session](#dont-start-kitty-from-a-claude-session), below, has the whole story.

## `spawn`, `resume`, `send`, or `focus` fails `terminal` with `unavailable`

**Cause:** sesshin can't reach your terminal. It drives kitty through kitty's remote control, which works only when:

- the command runs inside a kitty window (not over ssh, and not from cron);
- not under tmux or screen;
- kitty has remote control on, with a socket to listen on, so that `KITTY_LISTEN_ON` is set in its windows.

**Fix:** run the command from a kitty window. To turn remote control on, add this to `kitty.conf` and restart kitty, since it reads these settings only at startup:

```
allow_remote_control socket-only
listen_on unix:/tmp/kitty-{kitty_pid}
```

`{kitty_pid}` gives each kitty instance its own socket. `socket-only` accepts commands only through that socket, which is all sesshin uses. Then `echo $KITTY_LISTEN_ON` in a new window should print the socket. A session started while remote control was off has no window recorded, so `send` and `focus` refuse it with `conflict` (`no-placement`) until its next start. See [Placement](../specs/design-spec.md#placement).

## `migration-pending`, or no `#12` in the statusline after an upgrade

**Cause:** you replaced the binaries, but haven't converted the state directory's files to the new formats. Until you do, hooks leave files in the older format alone: a session may show no sesshin ID, miss events, or start without an ID.

**Fix:** run `sesshin migrate`. Running sessions don't need to stop, and `sesshin migrate --dry-run` shows what it would change first. Any IDs that were held back are filled in at each session's next event. See [Upgrading](upgrading.md) and [Migrations](../specs/design-spec.md#migrations).

## `migration-ahead`, or `unsupported-format`

**Cause:** a newer sesshin has already used this state directory. The binary you ran is older, and leaves the newer files alone.

**Fix:** use the newer binary. sesshin never converts files back to an older format ([Format versions](../specs/design-spec.md#format-versions)).

## `job-taken`, but no live session has that job

**Cause:** a reservation holds it. `spawn` and `resume` reserve the job before they launch `claude`, and the new session takes it over at its first hook. When `.error.details.sessions` is empty, the holder is such a reservation, usually a window still waiting at Claude's workspace-trust dialog. The reservation stays valid while its window is open, for up to a day. It lapses by itself 2 minutes after a launch that never opened a window, once its window closes, or after a day.

**Fix:** answer the trust dialog in that window, or close it. To free the job right away, remove its reservation from the state directory (`~/.local/state/sesshin`, or `$XDG_STATE_HOME/sesshin`), where the job is lowercased:

```sh
rm ~/.local/state/sesshin/reservations/api_*.json
```

That is the one change by hand sesshin allows there. See [Reservations](../specs/design-spec.md#reservations).

## Hooks stopped working after a package-manager upgrade

**Cause:** `sesshin install` records `sesshin-hook`'s real path, after following symlinks. A package manager that installs through a symlink into a versioned directory, as Homebrew's Cellar does, moves that path on each upgrade, so Claude Code calls a binary that is gone.

**Fix:** run `sesshin install` again and apply its proposal. New sessions pick up the change; restart running ones. `sesshin install --dry-run` shows every entry as `unchanged` when the wiring is right.

## `busy`

**Cause:** another process held a lock sesshin needed for longer than sesshin waits. That process is usually a hook, which holds a lock for milliseconds. `.error.details.lock` says which lock: `state` or `session`. Nothing was launched or changed, except that a `migrate` keeps the sessions it had already converted.

**Fix:** retry after a moment. If it keeps happening, something is holding the lock: look for a stuck `sesshin` or `sesshin-hook` process. How long each command waits is in the [Locks](../specs/design-spec.md#locks) table.

## The statusline shows only `🧠 0%`

**Cause:** this is the fallback line. sesshin had nothing usable to draw: Claude Code sent no payload, or one without a session ID, or there was no usable `HOME`. With `#12` in front of it, sesshin knew the session but rendering failed. A brand-new session shows `🧠 0%` with its other segments until its first response, and that is normal.

**Fix:** if it persists, look in `hooks.log` (below). The statusline's rules are under [Rendering](../specs/hooks-spec.md#rendering), and [What the statusline shows](statusline.md) explains each segment.

## `jump` or `restart` fails `unavailable`

The picker can't run. `.error.details.reason` says why:

- `fzf-missing`: fzf isn't on `PATH`. In a kitty key binding, kitty's `PATH` is the one it was started with, which from a desktop launcher may not include where fzf lives. Name sesshin by its full path in `kitty.conf`, and give kitty's `env` a `PATH` that has fzf.
- `fzf-too-old`: the pickers need fzf 0.63.0 or later. `found` and `required` give the versions.
- `fzf-failed`: fzf exited with an error, often a bad option in `FZF_DEFAULT_OPTS` or `SESSHIN_PICK_OPTS`. fzf's own message is shown in the terminal.
- `no-terminal`: the pickers draw on `/dev/tty`, so they need a terminal. Agents and scripts use `resume` and `focus` instead.

`restart` also needs kitty remote control, as `resume` does (above). See [picker-spec, Errors](../specs/picker-spec.md#errors).

## `send` refuses with `mid-turn`, but the session is idle

**Cause:** the turn was interrupted with Esc or Ctrl-C. Claude Code runs no hook for an interrupt, so sesshin still sees the session as `working` until its next event. `send` refuses a session mid-turn because a dialog might have the keyboard, and text plus Enter could answer it.

**Fix:** look at the window first (`sesshin focus <id>`), then send with `--force` once you're sure no dialog is up. Or type into the window yourself. The rules are under [`send`](../specs/operations.md#send).

## Where to look: `hooks.log`

Hooks never print anything into Claude Code, so what goes wrong in one is written to `hooks.log` in the state directory:

```sh
tail ~/.local/state/sesshin/hooks.log
```

Each line is `<timestamp> <verb> <session-uuid or -> <message>`: when it happened, which hook (`session-start`, `stop`, `statusline`, …), for which session, and what failed. At 1 MiB the log moves to `hooks.log.1`, replacing the one before, so at most about 2 MiB is kept. A file in an older format, waiting for `migrate`, is logged only at a session's start, not at every event. The messages are written for people and may change between releases. See [Log](../specs/hooks-spec.md#log).

## Don't start kitty from a Claude session

Claude Code puts markers in the environment of every process it starts: `CLAUDECODE=1`, `CLAUDE_CODE_CHILD_SESSION`, `CLAUDE_PID`, and others. A process passes a copy of its environment to everything it starts. So a kitty started from a Claude session (`kitty &` run by an agent, or by you from the shell Claude gives its tools) carries those markers for as long as it runs, and puts them into every window it opens, `sesshin spawn`'s included. Every `claude` in that kitty then looks like a child of another session:

- **No transcript.** A `claude` that inherits `CLAUDE_CODE_CHILD_SESSION` saves none, and says so under its prompt ("Transcript saving is off — inherited CLAUDE_CODE_CHILD_SESSION marker"). Without one, `claude --resume` (and `sesshin resume`, which warns `transcript-missing`) fails with "No conversation found".
- **Headless to sesshin.** `CLAUDECODE=1` in its environment makes sesshin record it as nested, as it does a `claude -p` run by a tool: no job, no placement, hidden from `list` and `restart` by default, and pruned after `retain_headless_hours`.

Start kitty from your desktop, a launcher, or a login shell instead. To start one from a shell that might be Claude's, remove the markers first:

```sh
env $(env | sed -n 's/^\(CLAUDE[A-Z_]*\)=.*/-u \1/p') kitty --detach
```

`sesshin spawn` itself is safe, whoever runs it. It doesn't start a kitty. It asks your running kitty to open a window, which gets kitty's environment, never the caller's. So an agent can spawn sessions freely as long as your kitty was started cleanly. Claude Code also names `CLAUDE_CODE_FORCE_SESSION_PERSISTENCE=1` as a way to keep transcripts in a marked kitty. That fixes only the first problem: sesshin would still record the sessions as nested.
