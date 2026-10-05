# Deferred: design-spec.md

What the deferred commands add to [design-spec.md](../design-spec.md). Each section names the commands it serves. Bring it back with them (see [README](README.md#bringing-a-command-back)).

The text is as it stood when the scope was cut, minus what has since come back to the main spec or been dropped. Check it against the main spec before merging.

---

## Placement: verifying and repairing a window

For `focus`. The main spec's [Placement](../design-spec.md#placement) records the window and calls it a cache. [`send`](../operations.md#send) came back finding the window by pid on the stored socket, then the caller's, with no repair and no sibling search. `focus` adds both:

- **Verified, not trusted.** Before every [`focus`](operations.md#focus), sesshin finds the window from `kitten @ ls` whose foreground process is the session's `pid`. When that differs from the stored `socket` and `window_id`, `focus` repairs them in `sesshin.json`, under the session lock, and warns `placement-repaired`.
- **Searched socket by socket.** `kitten @ ls` answers for one kitty instance, and each OS instance has its own socket (`listen_on unix:/tmp/kitty` gives `/tmp/kitty-<pid>`). sesshin asks, stopping at the first that has the window: the stored `socket`; then the caller's own `KITTY_LISTEN_ON`, if set and different; then, if the stored path ends in `-<digits>`, every sibling matching `<prefix>-*` that accepts a connection, each with a short connect timeout. That finds the session after kitty restarted, or in another instance, without parsing `kitty.conf`, and works from a CLI outside kitty (ssh, cron). The siblings are tried only on a miss, so a normal `focus` asks one socket.
- **A fallback for focus only.** When no window matches, `focus` may fall back to the stored `window_id`, since focusing the wrong window is harmless. `send` never does: typing into an unverified window could submit the text to a shell.

---

## Format versions: the upgrade path

For when sesshin has users. The main spec's [Format versions](../design-spec.md#format-versions) treats a file in any other version as unusable and replaces it from scratch. The upgrade path replaces that rule:

**Never downgrade.** A file with a *newer* `schema` than the binary supports is left alone: a hook logs it and records nothing for that file, and an operation warns. Two binaries can run at once (the hooks pinned to the path `install` recorded, the CLI from `PATH`), and an older one rewriting a newer one's files would make them fight. [`doctor`](operations.md#doctor) reports a hooks binary older than the CLI (`hooks-binary-mismatch`); running `sesshin install` again fixes it. A `state.json` in a newer format is never rewritten: the hook leaves the session's `id` `null`, logs, and a binary that can read the file issues it later.

**Writes replace whole files, and repair as they go.** Every write already replaces the whole file (see [Files](../design-spec.md#files)), so a hook that finds its file in an older format, or corrupt, simply writes it in the current one. It carries forward every field it can still read by name and type, and starts the rest as a new session would, then records its event as usual. There is no migration step and no command to run: after you install a new `sesshin`, each running session's files are rewritten by its next hook.

What a field that can't be carried forward costs:

- **`event_seq`** restarts from 1. A reader holding a cursor sees it go backwards, which it can only read as "missed everything", and should re-read the transcript.
- **`started_at`** and **`last_start_at`** become the time of the rewrite.
- **`pid`**, **`pid_started_at`** are captured again by the hook's lookup of Claude's process (see [Liveness](../design-spec.md#liveness)), as `SessionStart` does.
- **`id`** in `sesshin.json` is kept if it can be read. If not, the session is issued a fresh ID from `last_id`, never an old one.

From 1.0, every format change bumps `schema`, and this rule applies. A file changed this way is never an error for a hook, and never stops a session being recorded. The hooks' side is in [hooks-spec.md](hooks-spec.md).

---

## What doctor and repair rely on

For `doctor` and `repair`. The main spec dropped these points with the commands:

- **Outside changes.** Any change under the state directory not made by sesshin is outside the contract ([Assumptions](../design-spec.md#assumptions)); `doctor` finds what it leaves behind.
- **Location mismatch.** Every entry point resolves the [locations](../design-spec.md#locations) from its own environment, so a CLI run from cron, ssh, or an IDE can resolve different ones and silently read other state. `install` records the locations it resolved in `install.json`, and `doctor` reports a mismatch (`location-mismatch`).
- **Hooks binary.** `install.json`'s `binary` is the binary every hook runs; `doctor` reports `hooks-binary-mismatch` when it is gone or older than the CLI.
- **The hook log.** `doctor` shows `hooks.log`'s recent entries (`hook-errors`), including a `last_id rebuilt from N` line.
- **Sesshin IDs.** A duplicated sesshin ID can come only from an outside change; `doctor` reports it, and an operation that writes refuses it with `conflict` (`rule`: `duplicate-id`).
- **Leftovers.** A hidden temp file, a hidden directory from an interrupted prune, or a session directory with no `lifecycle.json` is also what a write in progress looks like. `repair` removes one only once it is more than 60 seconds old ([Files](../design-spec.md#files)). `repair` takes the state lock, then only *tries* each session lock, and skips a session whose lock is held, as `prune` does.

---

## Prompt cache in the pickers

For `jump` and `watch`. The [pickers](../picker-spec.md) show the [prompt cache](../design-spec.md#prompt-cache) in each row (e.g. `cache 3m` while warm, `cold ~45k` once cold) and in full in the preview, with the miss count and last miss cause.

---

## User-owned extra

An `extra` object in `sesshin.json` for whoever uses sesshin, as koan's `extra` is for tasks: labels, notes, or an agent recording "this session is reviewing PR 142". sesshin would store it and hand it back, and never read, validate, or act on it. It would need a way to set it: a `--extra` on [`spawn`](../operations.md#spawn), and an operation to change it on a session already running.

It is deliberately separate from [`placement`](../design-spec.md#sesshinjson), which looks similar, a JSON object sesshin doesn't fully define, but is the opposite: sesshin's own working state, written only by sesshin and read by `focus`, `send`, and `resume`. User data under a key sesshin depends on would be one edit away from sending text to the wrong window. Nothing needs `extra` yet.
