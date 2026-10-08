# Deferred

What sesshin has specified, or planned, but not built yet. Nothing here is current spec, and it is not kept in step with the main specs. Everything that came back (`list`, `show`, `spawn`, `resume`, `send`, `focus`, `prune`, `restart`, `jump`, attention, reservations, selectors, the session view, the user-owned `extra`) is in the main specs, which supersede the old text; git history has it. So is the format-version upgrade path's one lasting rule, never downgrade; the rest of it (carrying fields forward as hooks rewrite files) was superseded by [migrations](../design-spec.md#migrations).

## What is here

| File | What it holds | Why it waits |
|---|---|---|
| [operations.md](operations.md) | The operations `doctor`, `repair`, and `info`, and the planned `update` (changing a session's `extra`), with the findings, kinds, and rules only they use. | Not needed yet. `update` comes back when something needs to change `extra` after a session starts. |
| [cli-spec.md](cli-spec.md) | Their commands, the picker `watch`, and the Not included items that wait on the pickers. | As above; `watch` once `jump` has been used for a while. |
| [design-spec.md](design-spec.md) | What they add to the data model: what `doctor` and `repair` rely on, and a preview for `jump` and `watch`. | It serves the commands above; the preview was left out of `jump` to start small. |
| [review-operations.md](review-operations.md), [proposed-fixes-high.md](proposed-fixes-high.md), [triage-operations.md](triage-operations.md) | The operations review, its proposed fixes, and its triage, untouched. None of their findings were applied. Many concern operations that have since come back; check each against the main specs before acting on it. | Kept with `doctor`, `repair`, and `info`, which they also cover. Its `focus` findings were settled when `focus` came back: no ack, no lock, and no repair; an unknown pid falls back to the stored window. |

## Dropped: armed attention and ack

[Attention](../design-spec.md#attention) is back, derived at read time for [`jump`](../picker-spec.md#jump). What stays dropped is herd's machinery around it: armed marks, the four grace keys (`wait`, `approval`, `stuck`, `self_wake`), and the ack `focus` wrote to `sesshin.json`. They served notifications, which sesshin doesn't send; a picker shows the state when you look. They were left out to keep the design small, not because they failed. Their text is in herd-new's history, before it became sesshin.

## Dropped: wait

`wait` was to block until a session reached a state (`waiting`, `idle`, ended) or a timeout passed, so that an agent driving other sessions could `send` a prompt, `wait`, then read the result. It is dropped, not deferred, because the question it answers can't be answered from sesshin's data. Don't propose it again.

Two questions hide in "wait until it's done":

- **Did the turn end?** Mechanical, and only roughly: a `Stop` after the `event_seq` that `send` returned. Right after `send`, the session still reads `waiting` from its previous turn until `UserPromptSubmit` arrives, so "until `waiting`" returns at once with the old answer. An interrupt fires no hook at all. A permission dialog reads `working` for its first 6 seconds or so, until its notification arrives ([Attention](../design-spec.md#attention)).
- **Is the work done?** A judgment. A turn can end on a question, a half-finished plan, "I'll carry on once the tests run", a confident wrong answer, or an API error (`stalled`). `self_waking` isn't necessarily unfinished, and `your_turn` isn't necessarily finished. `blocked` means only the user can move it on, so waiting for it is wrong too. Answering takes reading what the session said against what it was asked.

So any `wait` worth calling would encode a policy about when work is done, and that policy would be wrong often enough to mislead. That is the trap: it looks like a small poll on `event_seq` and `status`, but its name promises the second answer while it can only give the first. It also strains design-spec's first principle, [the data is the product](../design-spec.md): sesshin records what Claude Code reports and derives the rest at read time, and whether the work is done can't be derived from what Claude Code reports.

Deciding that work is done is the coordinator's job: the session or script that handed the work out, or a checker agent it spawns for that. The worker signals; the coordinator judges. The pattern is in the [skill](../claude/skills/sesshin/SKILL.md#watching-a-spawned-session): the prompt asks the session to write a completion signal, such as a report file, and the coordinator polls `show` on its own schedule and reads the result. A bare "block until `event_seq` passes N" would be honest, but it only saves polling turns. Build it only if polling ever actually hurts, and never call it `wait`.

## Bringing a command back

1. Move its operation and command sections from this folder's `operations.md` and `cli-spec.md` into the main specs, with the kinds and design rules it uses from this folder.
2. Restore the data it writes. That is a format change: it bumps the file's `schema` and ships a [migration](../design-spec.md#migrations).
3. Check the moved text against the main specs: much of it was written before the shared rules it uses came back changed.
4. Work through the review findings that apply.
