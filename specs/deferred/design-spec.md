# Deferred: design-spec.md

What the deferred commands add to [design-spec.md](../design-spec.md). Each section names the commands it serves. Bring it back with them (see [README](README.md#bringing-a-command-back)).

The text is as it stood when the scope was cut, minus what has since come back to the main spec or been dropped. Check it against the main spec before merging.

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

## A preview for watch

For `watch`: [`jump`'s preview](../picker-spec.md#jump-preview), the same parts and rows, but kept current as `watch` refreshes its lines, where `jump`'s is a snapshot written once.

