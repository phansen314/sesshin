# Changelog

Every release of sesshin, newest first. Versions follow [Semantic Versioning](https://semver.org/); what counts as a breaking change is in [operations.md](specs/operations.md#versioning).

Each release's heading is `## <version> — <YYYY-MM-DD>`, dated in the commit that is tagged: the release workflow refuses a section still marked unreleased.

**Upgrading:** see [Upgrading](docs/upgrading.md); any release, minor ones included, may need `sesshin migrate` (a new [migration](specs/design-spec.md#migrations) step, marked **Needs `migrate`** here), and until it runs, hooks leave files in the older format alone.

## 1.1.0 — unreleased

- **macOS**, on amd64 and arm64: release archives `sesshin_<version>_darwin_<arch>.tar.gz`, and Claude's process found through `sysctl`, so a session's pid and start time are recorded, liveness is known while it runs, `self` works, `send` and `focus` find its window by pid, and superseded and nested sessions are told apart, as on Linux. `jump` shows a failed focus and waits for a key. Clear the quarantine flag on binaries a browser downloaded ([Install](README.md#install)).
- **Terminal backends:** kitty now sits behind a contract, so another terminal can be added without changing a file format or a hook. A backend that lacks an ability a command needs fails `terminal` with the new reason `unsupported`, having done nothing; kitty has every ability a command needs, so nothing changes for it ([Terminal backends](specs/design-spec.md#terminal-backends)).
- **iTerm2** (macOS) is a second terminal: `spawn`, `resume`, and the pickers work from an iTerm2 session, and `send` and `focus` work from anywhere on a session placed in iTerm2, driven through AppleScript, with no setup beyond macOS's permission for the app you run `sesshin` from to control iTerm2 (System Settings → Privacy & Security → Automation). `spawn`'s `vars` become `user.<name>` variables. It has no tab-title or variable sync: the pickers show a session's name, and `resume` reopens it under that. `spawn` and `resume` refuse to launch when the path of `sesshin` itself holds a single quote, a backslash, or a control character, which iTerm2's splitting of a command can't be given. A tab iTerm2 restores after a restart shows an error and waits for Enter (reopen the session with `resume` or `restart`). `prune` also removes launch files left in `launches/` and reports them as `launch_files_removed` ([The iTerm2 backend](specs/design-spec.md#the-iterm2-backend)).
- **Upgrading on macOS:** sessions recorded by 1.0.0 on a Mac have no pid in `lifecycle.json`. One still running gets its pid from its statusline's next refresh, which looks Claude up on every tick; one that ended keeps the no-pid rule. No `migrate` is needed.

## 1.0.0 — 2026-10-08

The first release. sesshin records every Claude Code session on one machine in plain JSON files, written by Claude Code's hooks, with no daemon and no database.

- **Statusline:** each session's sesshin ID, name, context, prompt cache, directory, branch, model, cost, burn rate, API time, and rate limits ([what it shows](docs/statusline.md)).
- **Commands:** `list` and `show` for reading sessions as JSON; `spawn`, `resume`, `send`, `focus`, and `update` for driving them; `install` and `uninstall`, which propose changes to Claude Code's `settings.json` for you to apply; `prune` and `migrate` for upkeep; `version`.
- **Pickers:** `restart` brings back the sessions a reboot ended, and `jump` goes to the live session that most needs you.
- **Agents:** a Claude Code plugin with the sesshin skill, and `scripts/opencode.sh` for OpenCode.
- Linux, with kitty as the terminal.

Before 1.0.0, sesshin was built and used from source as a rebuild of [herd](https://github.com/phansen314/herd); its history is in git.
