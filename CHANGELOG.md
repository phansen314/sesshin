# Changelog

Every release of sesshin, newest first. Versions follow [Semantic Versioning](https://semver.org/); what counts as a breaking change is in [operations.md](specs/operations.md#versioning).

Each release's heading is `## <version> — <YYYY-MM-DD>`, dated in the commit that is tagged: the release workflow refuses a section still marked unreleased.

**Upgrading:** see [Upgrading](docs/upgrading.md); any release, minor ones included, may need `sesshin migrate` (a new [migration](specs/design-spec.md#migrations) step, marked **Needs `migrate`** here), and until it runs, hooks leave files in the older format alone.

## 1.0.0 — 2026-10-08

The first release. sesshin records every Claude Code session on one machine in plain JSON files, written by Claude Code's hooks, with no daemon and no database.

- **Statusline:** each session's sesshin ID, name, context, prompt cache, directory, branch, model, cost, burn rate, API time, and rate limits ([what it shows](docs/statusline.md)).
- **Commands:** `list` and `show` for reading sessions as JSON; `spawn`, `resume`, `send`, `focus`, and `update` for driving them; `install` and `uninstall`, which propose changes to Claude Code's `settings.json` for you to apply; `prune` and `migrate` for upkeep; `version`.
- **Pickers:** `restart` brings back the sessions a reboot ended, and `jump` goes to the live session that most needs you.
- **Agents:** a Claude Code plugin with the sesshin skill, and `scripts/opencode.sh` for OpenCode.
- Linux, with kitty as the terminal.

Before 1.0.0, sesshin was built and used from source as a rebuild of [herd](https://github.com/phansen314/herd); its history is in git.
