# Closed review decisions

Every closed review decision, so a future reviewer doesn't raise them again. Each section is one review: the decisions the author made, then the mechanical fixes, one line each. The full reviews, their evidence, and the earlier data review and design-spec review (round 2) are in git history before commit f51483e.

The operations review is not here: its findings were never applied, and it waits in [deferred/](../deferred/README.md).

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
| 15 | Attention has no core reader | Deferred, later dropped for good ([deferred/README.md](../deferred/README.md#dropped-attention-and-ack)). |
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
| 13 | Agreement tests versus `2.0` as an integer | Reject non-integer literals, as ftask. |

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
