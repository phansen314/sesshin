# Closed review decisions

Every closed review decision, so a future reviewer doesn't raise them again. Each section is one review: the decisions the author made, then the mechanical fixes, one line each. The full reviews, their evidence, and the earlier data review and design-spec review (round 2) are in git history before commit f51483e.

The operations review is not here: its findings were never applied, and it waits in [deferred/](../deferred/README.md).

## Terminal backends

A design (2026-10-09) to make kitty one terminal backend behind a contract, so another terminal (iTerm2, Ghostty) is a backend and not a change to the data model or the hooks. The data model, `record`, and the schemas were already neutral; the hooks, `ops`, and `pick` call kitty directly.

### Decisions

| # | Decision | Decided |
|---|---|---|
| 1 | Scope | The contract and kitty as its one backend ([Terminal backends](../design-spec.md#terminal-backends)). iTerm2 and Ghostty are specified only when one is built: the author uses kitty. |
| 2 | A backend lacking an ability | `terminal` with the new `reason` `unsupported`, nothing done; `spawn` with `vars` on a backend without user variables is the same refusal, never a silent drop. The ability is named in `detail`, not a new key. A placement whose tag has no backend stays `conflict` `no-placement`, as before. |
| 3 | An override variable (`SESSHIN_TERMINAL`) | Not yet: with one backend it could only turn placement off. Decided with a second backend. |
| 4 | How backends are found | A fixed, ordered list in the binary, detection by environment only (no process on a synchronous hook), never registered by `init`; no Go plugins. A stored placement's tag picks its backend. |

## Code review: since the rename

A review of the code since `abbe89e`: job keys, session-scoped `extra`, token reservations, `update`, migrations, attention, `focus`, and `jump` with its preview.

### Decisions

| # | Decision | Decided |
|---|---|---|
| 1 | A second `SessionStart` on a pending spawned file adopted the spawn's reservation as a resume's, dropping its `extra` | Adopt on resume only a file that had an `id` before the hook; a pending one is left to the Adopt rules. Done. |
| 2 | `migrate` rebuilt `last_id` past `sesshin.json` files in another format, so their IDs could be reissued | Refuse, as a hook does: `state.json` left alone and listed in `unconverted`, the number held. Done. |
| 3 | `resume --job` of a session whose `sesshin.json` is in another format held the job with no session | Fail `conflict` (`other-format`) before any reservation; without a job, resume as before. Done. |
| 4 | Migration 1 refuses a schema-1 `sesshin.json` with `extra`, which only fa0fdae wrote | Leave it: fa0fdae was never released. Noted in design-spec Migrations. Done. |
| 5 | `jump`'s input and usage errors fail before step 6, so the overlay shows nothing | Document it: a key binding passes no input. Done. |
| 6 | A failure step 6 shows was also on stderr, so the overlay showed it twice | Leave it out of stderr's note; with no `/dev/tty`, it goes to stderr after all. Done. |

### Auto-fixed

- **Bugs:** hooks logged "keeps no id" on every event while `state.json` waited for `migrate`; a `migrate --dry-run` with no `sessions/` skipped `state.json`'s checks; `FZF_DEFAULT_OPTS=--tac` reversed the pickers' order (`--no-tac` undone); `cancelled` repeated its kind on stderr; `jsonio.Equal` expanded huge exponents, which could hold a session lock for minutes in `update`.
- **Spec:** `migrate`'s `to` is the latest step, held when `state.json` is unconverted.
- **Tests:** migration status from `focus` and `update`; `state.json` unconvertible, or changed between `migrate`'s steps; `self` through the statusline's pid; `showFailure` on a pty.

## Consistency and duplication review

A review of the main specs, README, and SKILL.md for consistency (data model, command surface) and duplication, after `extra`, `update`, `migrate`, and `jump`'s attention and preview landed.

### Decisions

| # | Decision | Decided |
|---|---|---|
| 1 | implementation-spec names koan about 35 times, as credits and code pointers | Keep them: it is about how the code is built. Every other spec, README, and SKILL.md name neither koan nor shingi. Done. |
| 2 | SKILL.md says to offer `migrate`, but the proposed permission rules let an agent run it without asking | Add `migrate` to SKILL's never-unasked hard rule; the permission rules are unchanged. Done. |

### Auto-fixed

- **Stale lists:** `update` and `migrate` added to the command lists in design-spec, operations, and README; `jump` to `config.toml`'s readers; `update` and `migrate` to `sesshin.json`'s writers; `update` to the migration-status callers; `SESSHIN_JOB`/`SESSHIN_TOKEN` to session-start's reads; `SESSHIN_*` to the e2e harness's removed variables.
- **Contradictions:** the non-goal "judges no session as needing you" (Attention derives it); `migrate` waits a fixed 2 s, not "as a hook does"; migrate converts no `statusline.json`; an unconvertible older `state.json` holds `migration` back; `self` selects a session without `sesshin.json`; `extra` "never interprets", not "never reads".
- **SKILL.md:** reservation file name `<job>_*.json`; `busy` for every lock; `focus`'s refusals; `extra-too-large` and `unsupported-format`; exit-3 rerun safety for `update`, `focus`, `migrate`.
- **One home per rule:** a Locks table in design-spec for every wait; `extra`'s rules (hooks-spec Creating `sesshin.json`, design-spec User-owned extra); reservation naming and staleness (design-spec Reservations); placement `null` (design-spec Placement); another-format logging (hooks-spec Log); the statusline's minute-ahead rule (hooks-spec step 6); upgrade steps (README); verified Claude Code facts (design-spec Settled); picker-spec's shared "How the pickers run", with its tests moved to implementation-spec.
- **Links:** a broken `deferred/operations.md#warnings` link removed; koan/shingi example paths in picker-spec made neutral.

## Cleanup review

A review of everything after spawn, resume, send, and restart landed: docs consistency, spec structure, the command-side code (ops, cli, pick, placement, live, proc), and the core code (record, model, jsonio, fsys, hook, settings, statusline).

### Decisions

| # | Decision | Decided |
|---|---|---|
| 1 | `conflict`'s rule `busy` shares its name with the error kind `busy` | Rename the rule `mid-turn`. Done. |
| 2 | After Esc or Ctrl-C, a session reads `working` until its next event, so `send` refuses it | Document it (operations `send`, SKILL.md); no behavior change. Done. |
| 3 | `reviews/` is all closed; `deferred/` is mostly superseded | Delete the reviews, keep one closed-decisions ledger (this file), trim `deferred/` to what is still deferred. Done. |
| 4 | Dated "verified on 2.1.289" notes are scattered through normative text | Move them into design-spec [Settled](../design-spec.md#settled), grouped by Claude Code and kitty version. Done. |
| 5 | `fsys.Publish`'s no-clobber link branch is used only by tests | Drop `replace`, `atLink`, and `Link`. Done. |
| 6 | `version`'s `formats` leaves out `reservation` | Add it. Done. |
| 7 | `IsText` accepts C1 controls (NEL), which `Scrub` removes | Refuse them in `IsText` and the `text` pattern. Done. |
| 8 | `placement` is defined three times in schemas/, with two different `terminal` types | One `defs#/$defs/placement`. Done. |
| 9 | hooks.properties with CRLF line endings is rejected | Strip one trailing `\r` per line. Done. |
| 10 | list, show, and prune read the OS environment directly; spawn and the others take an `Env` override | Add `Env.Read` for consistency and tests. Done. |
| 11 | Only 3 of the 9 input decoders are checked against their schemas | Run all of them through `schematest.Mutations`. Agreement checks found prune's `retain_days` schema had no maximum; it now has the decoder's int64 bound. Done. |

### Auto-fixed

- **Docs:** Attention removed from current specs; README brought current; one home per rule, linked from the other specs; the operation template applied evenly; implementation-spec and SKILL.md matched to the code.
- **Ops code:** one launch path and wait loop for spawn and resume; shared helpers and constants; `runKitten` keeps kitty's stderr; stale comments fixed.
- **Core code, bug:** a statusline payload with a repeated key was stored, then unusable on every later tick. `jsonio.Payload` now refuses it.
- **Core code, bug:** with stderr closed at start, sesshin-hook's `/dev/null` landed on fd 2 with `O_CLOEXEC`, so `kitten` had no stderr. Fixed.
- **Core code:** dead code removed; verb constants with a registration test; one shared file-status reader; settings' own depth limit; stale comments fixed.

## Core specs review

36 findings on design-spec, hooks-spec, operations, and cli-spec. All closed.

### Decisions

| # | Decision | Decided |
|---|---|---|
| 2 | How `install`/`uninstall` recognize sesshin's entries | By an argv test. `install` removes sesshin's entries under events it no longer registers; herd's shim is left to the user. |
| 5 | Notifications for a session sesshin never saw | Don't adopt. |
| 8 | Burn rate spikes after a sample reset | A 60 s minimum span. |
| 12 | Corrupt config is never reported | `install` fails. |
| 13 | `uninstall` and a statusLine changed since install | Remove it only while it is still sesshin's. |
| 14 | Old and new binary alternating on one session | Document: upgrade with no sessions running. |
| 15 | Attention has no core reader | Deferred, later dropped for good ([deferred/README.md](../deferred/README.md#dropped-armed-attention-and-ack)). |
| 21 | `internal` with details | A new kind, `self-test-failed`. |
| 35 | Symlink-resolved binary path | Keep it; note it in cli-spec install. |

### Auto-fixed

- 1: the `entrypoint` schema accepts `sdk-cli`.
- 3: `install.json` written after `settings.json`.
- 4: session-start's lock order.
- 6: the statusline's panic path.
- 7: `prompt_cache` types left as an open question.
- 9: `settings.json` shape and editing rules.
- 10: `dry_run` effects.
- 11: self-test isolation and pass criteria.
- 16: "unusable clock" defined.
- 17–19: dangling `incomplete-session`, `busy`, and implementation-spec references.
- 20: operation template conformance.
- 22: `install`'s error order.
- 23, 24: number formats; the model rule.
- 25, 27: "shown" hit ratio; "never opens".
- 26: empty stdin versus the statusline.
- 28: prune writing `last_id` 0.
- 29: a new `lifecycle.json` record.
- 30: an unreadable `hook_event_name`.
- 31: directory creation and modes.
- 32: backup collisions.
- 33: SessionEnd's budget, verified (a hook's `timeout` raises it, up to 60 s).
- 34: goroutines (H5).
- 36: config ranges.

## Foundation review

21 findings on the first code: jsonio, fsys, the guards, the CLI envelopes. All closed.

### Decisions

| # | Decision | Decided |
|---|---|---|
| 1 | Lone surrogates in the stored payload | Rewrite each to `�`. |
| 5 | Three lock waits break H4's bound | One lock deadline per hook: twice `hook_lock_wait_ms` (`session-end`: `min(hook_lock_wait_ms, 1000)`); every wait draws on it. |
| 6 | `changes` can't report removals | A `removed` action; duplicates keep the first. |
| 7 | `--input` from a pipe versus fsys's non-regular reads | Keep `--input`; read it with a blocking `os.Open` and `io.ReadAll`. |
| 8 | Logging before `<state>` exists | A hook may create `<state>` (`0700`) and `hooks.log`, nothing else. |
| 13 | `sesshin` core dumps | `RLIMIT_CORE` 0, as `sesshin-hook`. |

### Auto-fixed

- 2: `jsonio.Payload` refuses nesting past `PayloadDepth` (64).
- 3: the guard tests check an explicit hook-path package list.
- 4: the scrub set defined (C0, DEL, C1, U+2028, U+2029).
- 9: every CLI envelope printed through `jsonio.MarshalLine` and validated.
- 10: `uninstall`'s Order and Errors.
- 11: "Stop once `cost_sample` is read" dropped.
- 12: stale text.
- 14: init guard blind spots; `TestHookNoExit`.
- 15: `OpenAppend` bounded to three rounds; `fsys.MaxRead` (16 MiB).
- 16: the temp name removed before the synced link is flushed.
- 17: the log escapes U+2028, U+2029, and C1.
- 18: settings errors are `io` with their errno, else `internal`.
- 19: rules beyond the schema (`float64` range, repeated key).
- 20: typos and a stranded bullet.
- 21: a garbled comment.

## Hooks review

25 findings on the hook code and hooks-spec. All closed.

### Decisions

Each was decided as recommended.

| # | Decision | Decided |
|---|---|---|
| 1 | An unreadable `lifecycle.json` | Log it and write nothing, as for `sesshin.json`. |
| 2 | An unreadable `state.json` | Log it and issue no ID (`sesshin.json` with `id` `null`). Only a missing or unusable file is rebuilt. |
| 3 | `session-start` completing a `sesshin.json` with `id` `null` | Replace the placement there too, also when the ID can't be issued. |
| 4 | Wall clock stepped back | A stored `received_ns` more than a minute ahead of now counts as older and is replaced. |
| 5 | Trailing data after the payload | `Decode` reports it, so every verb logs `payload:` once. |
| 6 | Unscrubbed `tab_title` and `user_vars` | `ParseLS` scrubs them; `socket` stays verbatim. |
| 7 | Log wording | One grammar: `<op> <file>: <err>`, `<file> unusable: <reason>`, no package prefix. |
| 8 | The tmux/screen rule in two packages | One `placement.Multiplexed(getenv)`; terminal-sync takes kitty's parsed form. |
| 9 | Names | `hook.Env` is `hook.Process`; `Now` and `Lookup` in both `record` and `statusline`. |

### Auto-fixed

- 10: statusline logic moved into `statusline.Run`; one fallback line.
- 11: one `readFile` per package; `lifecycle.json` read once.
- 12: `Record` calls `withLocked`; `fsys.OpenRootCreate`.
- 13: `RecordEnv()` sets kitty's placement.
- 14: duplicate and sleeping tests removed; e2e runs in parallel (7.0 s to 1.5 s).
- 15: paths and file names in one place each.
- 16: `constError` doc texts; `kitty.Error`.
- 17: shared e2e helpers; one hook-binary sentence per `doc.go`.
- 18: `pending` logs what happened, after it happened.
- 19: README status.
- 20: performance gate and CI drift.
- 21: `ErrNothingToRecord`'s docs.
- 22: the fallback list.
- 23: Reads lists.
- 24: hook template drift.
- 25: stale package lists.

## Implementation spec review

15 findings on implementation-spec. All closed.

### Decisions

| # | Decision | Decided |
|---|---|---|
| 1 | Fatal runtime errors exit 2 (blocking; the trace reaches Claude) | `debug.SetTraceback("crash")`, so SIGABRT 134, non-blocking; `RLIMIT_CORE` 0. |
| 4 | Atomic rename over a symlinked `settings.json` | sesshin never writes `settings.json`. `install` and `uninstall` write `<state>/settings.proposed.json` and print the `diff` and `cat` commands to apply it. The proposal replaces a non-sesshin `statusLine` and the output flags it (`status-line-replaced`). `doctor` stays deferred; re-running `install` is the wiring check. |
| 6 | Hook payload decoding: `json.Unmarshal` can't record "whatever decoded" | A token-stream reader only. |
| 8 | `hooks.log` rotation race | Rotate under a non-blocking `flock` on the log's descriptor; skip if held. |
| 13 | Agreement tests versus `2.0` as an integer | Reject non-integer literals, as koan. |

### Auto-fixed

- 2: `CLAUDE_PID` accepted from the hook's parent or grandparent only.
- 3: no `init()` or working package-level initializer on the hook path.
- 5: `kitten`'s deadline gets `cmd.WaitDelay`.
- 7: the state lock uses the bounded wait, not a try-lock.
- 9: the statusline verb has its own outer recover.
- 10: `sesshin-hook` ignores SIGPIPE.
- 11: a nesting limit on the JSON reader.
- 12: JSON writing escapes no HTML; collections are never `null`; every envelope is validated in tests.
- 14: durations saturate.
- 15: cobra's cost is about 0.19ms.

## Migrations design

2026-10-07, koan #63. Flyway-style `sesshin migrate`, with migration 1 adding `extra` to every existing `sesshin.json`. Implementation is #64–#68.

### Decisions

| # | Question | Decision |
|---|---|---|
| 1 | Keeping new hooks from replacing old files before `migrate` runs | Per-file `schema`: a file in another format (older or newer) is left alone by every writer; only a corrupt file is replaced. No extra read on the hot path. Refusing `install` wouldn't help: replacing the binary at its path changes running sessions' hooks at once. |
| 2 | Migrations and `schema` | A step takes file kinds from schema N to N+1. `migrate` applies pending steps per file in memory and validates before one atomic write. `state.json` `migration` records the last step. Every format change now bumps `schema` and ships a step: pre-1.0 in-place changes end. |
| 3 | Locking, live sessions | Each session lock in turn (2 s wait), then the state lock, never both. Live sessions don't block it. A lock not taken fails `busy` without advancing the number. |
| 4 | An old file the steps can't convert | Report it (`unconverted`, `unusable-file`), leave it, advance anyway. |
| 5 | Missing state.json, fresh install, older binary | Fresh (no sessions): the latest step. A rebuild while sessions exist: 0. Schema-1 `state.json`: 0. Ahead of the binary: `unsupported-format`. |
| 6 | Output | Prune-style: `{dry_run, from, to, applied, changed, unconverted}`. New error kind `unsupported-format`; `busy` gains `lock: session`. |
| 7 | Reporting | `version` gains `migration`. Every operation that reads the state directory warns `migration-pending` or `migration-ahead`. Nothing runs `migrate` for you. |
| 8 | Fixtures | Hand-written `before/` and `after/` trees per step, with rerun and crash-injection tests. |

The deferred carry-forward upgrade path is superseded and removed from deferred/. Only its never-downgrade rule survives, in the main spec.

## Session-scoped extra

A proposal (2026-10-07) to stop `extra` going stale on `/clear` and `/new`: `extra` describes one session, travels in a token-named reservation instead of `SESSHIN_EXTRA`, and `update` comes back with a `self` selector.

### Decisions

| # | Question | Decision |
|---|---|---|
| 1 | Is `extra` window-level or session-level? | Session-level, never inherited; the job is the only window-level handle. sesshin's specs name no tool that uses `extra`; examples use neutral keys. |
| 2 | How does `spawn --extra` reach a session past the trust dialog? | Through the reservation. Every spawn writes one, `<key>_<token>.json` or `<token>.json`; the key stays in the name for `ls`, `rm api_*.json`, and a glob for "held". |
| 3 | Migrate old `<key>.json` reservations? | No. They are unusable, removed by `prune`; a session waiting at a trust dialog across the upgrade gets its job by `SESSHIN_JOB`, with `source` `hook`. |
| 4 | `update` on a session whose `id` is still `null` | Refused, `no-sesshin-file` with `file` `pending`, so the completing hook's adoption never meets a changed `extra`. No merge rule. |
| 5 | `self` | A selector that behaves as an exact ID in each scope (`resume self` is `conflict` `live`). A job named `self` stays legal, as `job:self`. |
| 6 | Smaller questions | A job-less spawn takes the state lock (and can fail `busy`); `resume` reserves only with a job; no `extra_updated_at`. |
| 7 | `update` on a `sesshin.json` in another format | Its own `file` value, `other-format`: no hook rewrites it, so the way out is `sesshin migrate` (older) or a newer `sesshin` (newer), never a retry or a `resume`. |
| 8 | `self`'s lookup | The nearest ancestor that is `CLAUDE_PID` or a `claude` by name, at any depth (`proc.FindCaller`); a hook's `proc.Find` still takes `CLAUDE_PID` only from its parent or grandparent. |

`/new` was verified to reach the hooks as `clear` on 2.1.293 (design-spec Settled).
