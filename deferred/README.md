# Deferred

What sesshin has specified, or planned, but not built yet. Nothing here is current spec, and it is not kept in step with the main specs. Everything that came back (`list`, `show`, `spawn`, `resume`, `send`, `focus`, `prune`, `restart`, `jump`, attention, reservations, selectors, the session view, the user-owned `extra`) is in the main specs, which supersede the old text; git history has it. So is the format-version upgrade path's one lasting rule, never downgrade; the rest of it (carrying fields forward as hooks rewrite files) was superseded by [migrations](../design-spec.md#migrations).

## What is here

| File | What it holds | Why it waits |
|---|---|---|
| [operations.md](operations.md) | The operations `doctor`, `repair`, and `info`, and the planned `wait` and `update` (changing a session's `extra`), with the findings, kinds, and rules only they use. | Not needed yet. `update` comes back when something needs to change `extra` after a session starts. |
| [cli-spec.md](cli-spec.md) | Their commands, the picker `watch`, and the Not included items that wait on the pickers. | As above; `watch` once `jump` has been used for a while. |
| [design-spec.md](design-spec.md) | What they add to the data model: what `doctor` and `repair` rely on, and a preview for `jump` and `watch`. | It serves the commands above; the preview was left out of `jump` to start small. |
| [review-operations.md](review-operations.md), [proposed-fixes-high.md](proposed-fixes-high.md), [triage-operations.md](triage-operations.md) | The operations review, its proposed fixes, and its triage, untouched. None of their findings were applied. Many concern operations that have since come back; check each against the main specs before acting on it. | Kept with `doctor`, `repair`, and `info`, which they also cover. Its `focus` findings were settled when `focus` came back: no ack, no lock, and no repair; an unknown pid falls back to the stored window. |

## Dropped: armed attention and ack

[Attention](../design-spec.md#attention) is back, derived at read time for [`jump`](../picker-spec.md#jump). What stays dropped is herd's machinery around it: armed marks, the four grace keys (`wait`, `approval`, `stuck`, `self_wake`), and the ack `focus` wrote to `sesshin.json`. They served notifications, which sesshin doesn't send; a picker shows the state when you look. They were left out to keep the design small, not because they failed. Their text is in herd-new's history, before it became sesshin.

## Bringing a command back

1. Move its operation and command sections from this folder's `operations.md` and `cli-spec.md` into the main specs, with the kinds and design rules it uses from this folder.
2. Restore the data it writes. That is a format change: it bumps the file's `schema` and ships a [migration](../design-spec.md#migrations).
3. Check the moved text against the main specs: much of it was written before the shared rules it uses came back changed.
4. Work through the review findings that apply.
