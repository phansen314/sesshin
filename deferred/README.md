# Deferred

What sesshin has specified, or planned, but not built yet. Nothing here is current spec, and it is not kept in step with the main specs. Everything that came back (`list`, `show`, `spawn`, `resume`, `send`, `prune`, `restart`, reservations, selectors, the session view, the user-owned `extra`) is in the main specs, which supersede the old text; git history has it. So is the format-version upgrade path's one lasting rule, never downgrade; the rest of it (carrying fields forward as hooks rewrite files) was superseded by [migrations](../design-spec.md#migrations).

## What is here

| File | What it holds | Why it waits |
|---|---|---|
| [operations.md](operations.md) | The operations `focus`, `doctor`, `repair`, and `info`, and the planned `wait` and `update` (changing a session's `extra`), with the findings, kinds, and rules only they use. | Not needed yet. `focus` comes back with `jump` and `watch`; `update` when something needs to change `extra` after a session starts. |
| [cli-spec.md](cli-spec.md) | Their commands, the pickers `jump` and `watch`, and the Not included items that wait on the pickers. | As above. |
| [design-spec.md](design-spec.md) | What they add to the data model: verifying and repairing a window (`focus`), what `doctor` and `repair` rely on, and the prompt cache in the pickers. | It serves the commands above. |
| [review-operations.md](review-operations.md), [proposed-fixes-high.md](proposed-fixes-high.md), [triage-operations.md](triage-operations.md) | The operations review, its proposed fixes, and its triage, untouched. None of their findings were applied. Many concern operations that have since come back; check each against the main specs before acting on it. | Kept with `focus`, `doctor`, `repair`, and `info`, which they also cover. |

## Dropped: Attention and Ack

Attention (the armed marks ⛔ 🙋 🔐 🥱 and their four grace keys) and Ack (`sesshin.json`'s `ack`, which `focus` wrote) are dropped for good, not deferred. herd's attention never earned its keep in months of use. `focus` only brings the window to the front and repairs its placement, and `send`'s check rests on status alone. Their text is in git history at commit f51483e (`deferred/design-spec.md`, `deferred/hooks-spec.md`, `deferred/operations.md`).

## Bringing a command back

1. Move its operation and command sections from this folder's `operations.md` and `cli-spec.md` into the main specs, with the kinds and design rules it uses from this folder.
2. Restore the data it writes. That is a format change: it bumps the file's `schema` and ships a [migration](../design-spec.md#migrations).
3. Check the moved text against the main specs: much of it was written before the shared rules it uses came back changed.
4. Work through the review findings that apply.
