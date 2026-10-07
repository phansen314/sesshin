# sesshin

Record your Claude Code sessions on one machine — which are working, which are waiting on you, where they live in [kitty](https://sw.kovidgoyal.net/kitty/), and what they have cost — with the filesystem as the database. No daemon, no SQLite, no server: each session is a directory of small JSON files written by Claude Code's hooks, and everything else (is it alive? who holds which job?) is derived when you look.

In a session, what you see is Claude Code's status line: that session's sesshin ID, context, cost, burn rate, prompt cache, and rate limits. `sesshin list` and `sesshin show` report every session as JSON, for `jq` and agents, and the files themselves are plain JSON too.

Linux only for now; macOS is planned (task #47).

**Status: built, and in use.** This is a rebuild of [herd](https://github.com/phansen314/herd), spec first, and `sesshin restart` has already brought back a real reboot's sessions. It is two binaries: `sesshin-hook`, which Claude Code runs for every hook, and `sesshin`, whose commands are `list`, `show`, `spawn`, `resume`, `send`, `restart` (a picker), `install`, `uninstall`, `prune`, and `version`. sesshin never deletes anything on its own: run `sesshin prune` by hand, or schedule it with a systemd timer or cron ([examples](cli-spec.md#prune)). `focus` and the pickers `jump` and `watch` are [deferred](deferred/README.md).

## Trying it

Build both binaries into the same directory, then let `sesshin install` propose the change to Claude Code's `settings.json`. sesshin never writes that file: you review the proposal and apply it.

```sh
go install ./cmd/sesshin ./cmd/sesshin-hook       # from this repository; both land in $(go env GOPATH)/bin
sesshin install --dry-run | jq .result.changes    # what it would change
sesshin install | jq -r '.result.apply[]'         # the diff to review, and the cat that applies it
```

Applying it affects only sessions started afterwards. To undo it, run `sesshin uninstall` the same way. herd's hook entries aren't sesshin's: remove them by hand.

### Upgrading

With no Claude sessions running, replace both binaries side by side, run `sesshin install` again, and apply its proposal if it has changes. Running sessions keep calling the binary they started with, and before 1.0 a new binary may not read an old one's files ([why](design-spec.md#format-versions)). `install` records `sesshin-hook`'s path with symlinks resolved, so a package manager that installs through a symlink into a versioned directory (Homebrew's Cellar) breaks the hooks on its next upgrade, until you run `sesshin install` again and apply its proposal.

## Bring sessions back after a reboot

A reboot, or a kitty closed by mistake, ends every session in it. `sesshin restart` lists the ended ones in [fzf](https://github.com/junegunn/fzf) (0.63.0 or later) and resumes the ones you pick, each in its own new tab, in its own directory, under its own tab title and job. Run it from a tab of the kitty you want them in:

```sh
sesshin restart
```

Type `killed`, press ctrl-a to mark every match (sessions that were still running when something killed them), then Enter. Tab and shift-tab mark single lines, and Esc leaves without resuming anything. `sesshin restart --query killed` starts with the query typed. Claude's own flags are not remembered by `claude --resume`; give them after `--`, and every pick gets them:

```sh
sesshin restart --query killed -- --permission-mode acceptEdits
```

It needs a terminal (it draws on `/dev/tty`), and kitty with remote control on, as `sesshin resume` does. Its output is one JSON line of `actions`, one per pick; `jq '.result.actions[] | select(.output.ok | not)'` finds the ones that failed (two picks storing the same job: the second fails `job-taken`, and `sesshin resume <id> --job <other>` brings it back). Style fzf with `FZF_DEFAULT_OPTS` or, for this picker alone, `SESSHIN_PICK_OPTS='--height 60% --layout reverse'`. Agents don't run it: they use `sesshin resume`.

## Don't start kitty from a Claude session

Claude Code puts markers in the environment of every process it starts: `CLAUDECODE=1`, `CLAUDE_CODE_CHILD_SESSION`, `CLAUDE_PID`, and others. A process passes a copy of its environment to everything it starts. So a kitty started from a Claude session (`kitty &` run by an agent, or by you from the shell Claude gives its tools) carries those markers for as long as it runs, and puts them into every window it opens, `sesshin spawn`'s included. Every `claude` in that kitty then looks like a child of another session:

- **No transcript.** A `claude` that inherits `CLAUDE_CODE_CHILD_SESSION` saves none, and says so under its prompt ("Transcript saving is off — inherited CLAUDE_CODE_CHILD_SESSION marker"). Without one, `claude --resume` (and `sesshin resume`, which warns `transcript-missing`) fails with "No conversation found".
- **Headless to sesshin.** `CLAUDECODE=1` in its environment makes sesshin record it as nested, as it does a `claude -p` run by a tool: no job, no placement, hidden from `list` and `restart` by default, and pruned after `retain_headless_hours`.

Start kitty from your desktop, a launcher, or a login shell instead. To start one from a shell that might be Claude's, remove the markers first:

```sh
env $(env | sed -n 's/^\(CLAUDE[A-Z_]*\)=.*/-u \1/p') kitty --detach
```

`sesshin spawn` itself is safe, whoever runs it. It doesn't start a kitty. It asks your running kitty to open a window, which gets kitty's environment, never the caller's. So an agent can spawn sessions freely as long as your kitty was started cleanly. Claude Code also names `CLAUDE_CODE_FORCE_SESSION_PERSISTENCE=1` as a way to keep transcripts in a marked kitty. That fixes only the first problem: sesshin would still record the sessions as nested.

## Use it from Claude Code and OpenCode

The [sesshin skill](claude/skills/sesshin/SKILL.md) teaches the agent the commands: spawning sessions under a job, watching them with `list` and `show`, bringing an ended one back with `resume`, typing into a live one with `send`, and when not to retry. Agents never run `restart`: it is a picker for you. Claude Code gets it from the `sesshin` plugin (the repo is a Claude Code plugin marketplace). Until it is published, add the marketplace from a clone:

```sh
claude plugin marketplace add ~/code/sesshin
claude plugin install sesshin@sesshin
```

`sesshin install` (above) also proposes the permission rules, so they arrive in the same reviewed diff as the hooks: `sesshin` and `jq` run without a prompt, while `install` and `uninstall`, which propose changes to Claude Code's setup, and `prune`, which deletes sessions, still ask. `sesshin uninstall` takes them out again, except `jq`'s, which other tools share. To try an edited skill without updating the plugin, run `claude --plugin-dir .` in a clone; `claude plugin update sesshin@sesshin` picks up changes.

Then ask your agent things like "spawn a session on ~/code/api to run the tests, job api" or "which of my sessions are waiting on me?".

For OpenCode, `scripts/opencode.sh` adds the same rules to `~/.config/opencode/opencode.json` and links the skill into `~/.config/opencode/skills/sesshin` (`--uninstall` takes both out). It needs `jq`, backs the file up first, touches only sesshin's rules, and is safe to rerun. By hand, the rules go after any other rule that matches sesshin, since in OpenCode the last match wins:

```json
{
  "permission": {
    "bash": {
      "sesshin *": "allow",
      "jq *": "allow",
      "sesshin install*": "ask",
      "sesshin uninstall*": "ask",
      "sesshin prune*": "ask"
    }
  }
}
```

Link the skill into OpenCode's directory, not `~/.claude/skills`: OpenCode reads that as well, and Claude Code would load the skill a second time next to the plugin's.

## Specs

| Spec | What it covers | Status |
|---|---|---|
| [design-spec.md](design-spec.md) | The data model, liveness, concurrency, and what changed from herd. | Matches the code |
| [hooks-spec.md](hooks-spec.md) | Each Claude Code hook, and the statusline: what it reads, what it writes, what it renders, and the exit-0 contract. | Matches the code |
| [operations.md](operations.md) | `list`, `show`, `version`, `install`, `uninstall`, `spawn`, `resume`, `send`, `prune`: input, output, errors, and retry safety. | Matches the code |
| [cli-spec.md](cli-spec.md) | How `list`, `show`, and the other commands map to operations, and `sesshin-hook`'s command line. | Matches the code |
| [picker-spec.md](picker-spec.md) | `sesshin restart`: the fzf picker that brings back the sessions a reboot ended. | Matches the code |
| [implementation-spec.md](implementation-spec.md) | How it is built and tested. | Matches the code |
| [deferred/](deferred/README.md) | What stays cut (`focus`, `doctor`, `repair`, `info`, `wait`, `update`, `jump`, `watch`), plus the unapplied operations review. | Parked |
