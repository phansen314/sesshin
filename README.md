# sesshin

Record your Claude Code sessions on one machine — which are working, which are waiting on you, where they live in [kitty](https://sw.kovidgoyal.net/kitty/), and what they have cost — with the filesystem as the database. No daemon, no SQLite, no server: each session is a directory of small JSON files written by Claude Code's hooks, and everything else (is it alive? who holds which job?) is derived when you look.

Inside a session, you see it in Claude Code's status line ([what it shows](docs/statusline.md)):

```text
#12 | ⬢ api refactor | 🧠 35% 351k/1M | ♨️ until 3:04PM | 📁 sesshin | 🌿 main | 🤖 Opus 5.5 | 💰 $1.20 | 🔥 $3.40/h | ⌛ 12m API
⏱️ 5h 22% resets 3:00PM | 7d 41% resets 10/6 9:00AM
```

Across sessions, `sesshin list` and `sesshin show` report every one as JSON, for `jq` and agents; `sesshin jump` takes you to the one that needs you; and after a reboot, `sesshin restart` brings them all back. Agents can `spawn` sessions under a job, `send` them prompts, and tag them with their own data.

Linux, with kitty, for now; macOS is planned.

## Install

Install both binaries, `sesshin` and `sesshin-hook`, into the same directory, by one of these:

- **A release** (Linux amd64 or arm64): download `sesshin_<version>_linux_<arch>.tar.gz` from [Releases](https://github.com/phansen314/sesshin/releases), check it against `SHA256SUMS`, and copy both binaries onto your `PATH` (`~/.local/bin`, say).
- **With Go** (1.26 or later): `go install github.com/phansen314/sesshin/cmd/...@latest`. Both land in `$(go env GOPATH)/bin`.
- **From a clone:** `go install ./cmd/sesshin ./cmd/sesshin-hook`.

Then let `sesshin install` propose the change to Claude Code's `settings.json`. sesshin never writes that file: you review the proposal and apply it.

```sh
sesshin install --dry-run | jq .result.changes    # what it would change
sesshin install | jq -r '.result.apply[]'         # the diff to review, and the cat that applies it
```

It affects sessions started afterwards. `sesshin uninstall` undoes it the same way. kitty needs remote control on for `spawn`, `resume`, `send`, `focus`, and the pickers ([how](docs/troubleshooting.md#spawn-resume-send-or-focus-fails-terminal-with-unavailable)), and must not be started from inside a Claude session ([why](docs/troubleshooting.md#dont-start-kitty-from-a-claude-session)). To upgrade later, see [Upgrading](docs/upgrading.md).

## A quick tour

```sh
sesshin list | jq '.result.sessions[] | {id, job, status, cwd}'   # the live sessions
sesshin show 12                                                   # one session, in full
sesshin spawn --job api --cwd ~/code/api --prompt 'run the tests'  # a new session in a new tab
sesshin send 12 --text 'carry on'                                 # type into a waiting one
sesshin jump                                                      # pick the one that needs you
sesshin restart                                                   # bring back what a reboot ended
sesshin prune --dry-run                                           # what pruning would remove
```

Every command but the pickers prints one line of JSON. `sesshin <command> --help` has the rest.

## Guides

- [What the statusline shows](docs/statusline.md)
- [The pickers](docs/pickers.md): `restart` after a reboot, and `jump` with a kitty key binding
- [Use it from Claude Code and OpenCode](docs/agents.md): the skill, the plugin, and permission rules
- [Configuration](docs/configuration.md): `config.toml`, `hooks.properties`, and the environment variables sesshin reads
- [Your data on disk](docs/data.md): the state directory, what's yours, `jq` recipes, and pruning
- [Upgrading](docs/upgrading.md)
- [Troubleshooting](docs/troubleshooting.md)

## Stability

From 1.0.0, sesshin follows semantic versioning. What scripts and agents rely on is stable until 2.0: the JSON envelope, error and warning kinds, the output schemas, command names, flags, and exit codes, and the files in the state directory, which an upgrade converts with `migrate` instead of replacing; the keys of your `config.toml` and `hooks.properties`; and the hook commands `install` puts in Claude Code's `settings.json`. The `extra` you store stays as you wrote it, and what sesshin copies from Claude Code is Claude Code's to change. What is drawn for a person is not: the statusline, the pickers' lines, help text, and messages may change in any release. The details are under [Versioning](specs/operations.md#versioning), and what each release changed is in the [CHANGELOG](CHANGELOG.md).

## Specs

| Spec | What it covers | Status |
|---|---|---|
| [design-spec.md](specs/design-spec.md) | The data model, liveness, concurrency, and what changed from herd. | Matches the code |
| [hooks-spec.md](specs/hooks-spec.md) | Each Claude Code hook, and the statusline: what it reads, what it writes, what it renders, and the exit-0 contract. | Matches the code |
| [operations.md](specs/operations.md) | `list`, `show`, `version`, `install`, `uninstall`, `spawn`, `resume`, `send`, `focus`, `update`, `prune`, `migrate`: input, output, errors, and retry safety. | Matches the code |
| [cli-spec.md](specs/cli-spec.md) | How `list`, `show`, and the other commands map to operations, and `sesshin-hook`'s command line. | Matches the code |
| [picker-spec.md](specs/picker-spec.md) | `sesshin restart` and `sesshin jump`: the fzf pickers that bring back the sessions a reboot ended, and go to the live one that wants you. | Matches the code |
| [implementation-spec.md](specs/implementation-spec.md) | How it is built and tested. | Matches the code |
| [deferred/](specs/deferred/README.md) | What stays cut (`doctor`, `repair`, `info`, `watch`) and why `wait` was dropped, plus the unapplied operations review. | Parked |

## History

sesshin is a spec-first rebuild of [herd](https://github.com/phansen314/herd) without its daemon and database. herd's hook entries in `settings.json` aren't sesshin's: remove them by hand.

## License

[MIT](LICENSE).
