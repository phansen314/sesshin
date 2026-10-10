# Your data on disk

sesshin keeps everything it records as small JSON files in one directory: no database, no daemon. Every file is readable with `jq`. This page says where they are, what each one holds, which parts are yours, and how to keep the directory from growing for ever. The normative description is [design-spec.md, Data model](../specs/design-spec.md#data-model).

## Where it is

| | Linux | macOS |
|---|---|---|
| **State directory** | `$XDG_STATE_HOME/sesshin`, else `~/.local/state/sesshin` | `~/Library/Application Support/sesshin/state` |

A command finds it from its own environment, and the hooks from Claude Code's. A `sesshin` run from cron, ssh, or an IDE with a different `HOME` or `XDG_STATE_HOME` reads a different directory, and finds no sessions ([Locations](../specs/design-spec.md#locations)).

## What is in it

```text
~/.local/state/sesshin/
  state.json                  the last sesshin ID issued, and the migration step
  install.json                what `sesshin install` proposed, for `uninstall`
  settings.proposed.json      install's or uninstall's proposal, for you to apply
  hooks.log, hooks.log.1      what the hooks had to tell you
  reservations/
    api_<token>.json          a launch's job and extra, waiting for its session to start
  launches/
    <nonce>.json              what an iTerm2 launch is to run, until its window reads it (macOS)
  sessions/
    <uuid>/                   one directory per Claude Code session, named by its UUID
      lifecycle.json          what Claude Code's hooks reported
      statusline.json         the latest statusline data, verbatim
      sesshin.json            the session's sesshin ID, job, window, and extra
```

### One session

Each Claude Code session gets a directory under `sessions/` the moment it starts. Its files are split by who writes them:

| File | What it holds |
|---|---|
| `lifecycle.json` | The session's life as Claude Code's hooks reported it: its directory, model, transcript, its process, when it started and last did something, its status (`idle`, `working`, `waiting`, `needs_approval`), and when and why it ended. ([fields](../specs/design-spec.md#lifecyclejson)) |
| `statusline.json` | The last data Claude Code sent the statusline, **verbatim**, in `payload`: cost, context, rate limits, the prompt cache, and anything else Claude Code reports, whether or not sesshin uses it. Beside it, what sesshin works out from it: the git branch and the burn rate. ([fields](../specs/design-spec.md#statuslinejson)) |
| `sesshin.json` | What sesshin adds: the sesshin ID (`#12`), the job, whether `spawn` launched it, where its window is (`placement`), and your `extra`. ([fields](../specs/design-spec.md#sesshinjson)) |

The first two are facts Claude Code reported, true whether or not sesshin existed. The third is sesshin's own. Nothing from `sesshin.json` is ever copied into the other two ([Two tiers](../specs/design-spec.md#two-tiers)).

### The rest

| File | What it holds |
|---|---|
| `state.json` | The highest sesshin ID ever issued, so IDs are never reused, and the last [migration](../specs/design-spec.md#migrations) step applied. |
| `reservations/` | One file per `spawn`, or per `resume` under a job, between the launch and the session's first hook. It holds the job, so no one else can take it meanwhile, and the `extra` to hand over. The new session takes it and removes it. ([Reservations](../specs/design-spec.md#reservations)) |
| `launches/` | macOS with iTerm2 only: what a `spawn` or `resume` asks its new window to run, until the window reads and removes it, seconds later. `prune` removes any left over. ([The iTerm2 backend](../specs/design-spec.md#the-iterm2-backend)) |
| `install.json` | What `install` proposed and where, so `uninstall` can propose undoing it. |
| `settings.proposed.json` | The `settings.json` that `install` or `uninstall` proposes. sesshin never writes Claude Code's settings itself. |
| `hooks.log` | One line per thing a hook couldn't do: a lock it waited too long for, a file it couldn't read. It is the hooks' only way to tell you anything, since they must stay silent in Claude Code. At 1 MiB it moves to `hooks.log.1`. ([Log](../specs/hooks-spec.md#log)) |

## Read it through sesshin

The files hold what was reported. What it means is worked out when you read it, and never stored:

- **liveness:** whether the session is still running, from its process;
- **attention:** whether it wants you (blocked on a dialog, stalled, your turn), from its status;
- **the prompt cache:** warm, cold, or unknown, from the cache's expiry time;
- **the name**, and who holds a job.

`sesshin list` and `sesshin show` do that work, and report it with everything stored, as one JSON object per session (the [session view](../specs/operations.md#session-view)). So reach for them first, and for the raw files when you want something they don't report.

## What's yours

**`extra`** is yours. It is a JSON object on each session where you, or your tools, keep anything: a ticket, a label, a note. sesshin stores it, hands it back, and shows it in the pickers, but never acts on it. Set it when you launch a session, or at any time after:

```sh
sesshin spawn --job api --extra '{"ticket":"auth-3"}' -- --model opus
sesshin update 12 --extra-merge '{"note":"waiting on review"}'
sesshin update self --extra-merge '{"ticket":"auth-4"}'    # from inside the session
```

It belongs to one session: a `/clear` or `/new` in the same window starts the next session with an empty `extra`. It survives `resume` and upgrades. ([User-owned extra](../specs/design-spec.md#user-owned-extra))

**Everything else is sesshin's.** Don't edit, create, or delete anything under the state directory by hand, apart from the one exception below. sesshin assumes it is the only writer, and a file it can't read is replaced from scratch. A session whose `sesshin.json` is replaced gets a new sesshin ID and loses its `extra`.

**The one exception:** removing a reservation releases its job. If a `spawn` never started (you closed its window at the trust dialog, say) and `spawn --job api` now fails `job-taken`, remove it:

```sh
rm ~/.local/state/sesshin/reservations/api_*.json    # the job, lowercased
```

## Recipes

Each reads with `--fields`, so the output stays small. `list` shows live sessions unless you pass `--liveness ended` or `all`.

Sessions that want you, the way `jump` would order them first:

```sh
sesshin list --fields name,attention \
  | jq -r '.result.sessions[] | select(.attention == "blocked" or .attention == "stalled" or .attention == "your_turn") | [.id, .attention, .name] | @tsv'
```

The five most expensive sessions, ever:

```sh
sesshin list --liveness all --fields name,metrics \
  | jq -r '.result.sessions | sort_by(-(.metrics.cost_usd // 0)) | .[:5][] | [.id, .name, ((.metrics.cost_usd // 0) * 100 | round / 100)] | @tsv'
```

What the sessions seen today have cost in all (each session's whole cost, not just today's share):

```sh
sesshin list --liveness all --fields metrics,last_seen \
  | jq --arg d "$(date -u +%F)" '[.result.sessions[] | select(.last_seen | startswith($d)) | .metrics.cost_usd // 0] | add // 0 | . * 100 | round / 100'
```

Warm prompt caches, and when each expires (answer before then to save the re-cache):

```sh
sesshin list --fields name,prompt_cache \
  | jq -r '.result.sessions[] | select(.prompt_cache.state == "warm") | [.id, .name, .prompt_cache.expires_at] | @tsv'
```

The session you tagged with a ticket:

```sh
sesshin list --liveness all --fields job,extra | jq '.result.sessions[] | select(.extra.ticket == "auth-3")'
```

Where your sessions run, busiest directory first:

```sh
sesshin list --liveness all --fields cwd \
  | jq -r '.result.sessions | group_by(.cwd) | map([(.[0].cwd // "?"), length]) | sort_by(-.[1]) | .[:5][] | @tsv'
```

Anything Claude Code reports that sesshin doesn't show, from the stored payload:

```sh
sesshin show 12 --include-payload | jq '.result.statusline_payload.model'
```

## Pruning

Ended sessions are kept, so `sessions/` only grows: a few kilobytes each, and a little more time for every `list`. **sesshin never deletes anything on its own.** `sesshin prune` removes:

- ended sessions last seen more than 30 days ago (`retain_days` in [config.toml](configuration.md));
- sessions run by tools and scripts (`claude -p`, or one session started by another), ended more than 24 hours ago (`retain_headless_hours`);
- reservations whose launch never finished, whose window is gone, or that are more than a day old;
- iTerm2 launch files more than 120 seconds old.

It never removes a session that is running, or one whose liveness it can't judge. `state.json` is left alone, so pruned sessions' IDs are never issued again.

```sh
sesshin prune --dry-run | jq '.result.pruned | length'    # how many would go
sesshin prune                                             # remove them
sesshin prune --retain-days 7                             # keep only a week this time
```

To run it every day, use a systemd user timer or a crontab line; both are in [cli-spec.md, prune](../specs/cli-spec.md#prune). Give a scheduled run the same `HOME` and `XDG_STATE_HOME` as your Claude sessions, or it prunes a different directory. The rules are under [Retention](../specs/design-spec.md#retention).
