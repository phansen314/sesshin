# Triage: review-operations.md

The 40 findings in [review-operations.md](review-operations.md), split into what an agent applies without asking and what needs the author. Started 2026-10-02. For the High findings, [proposed-fixes-high.md](proposed-fixes-high.md) holds the exact replacement text.

## Auto-fix (applied without asking)

These are corrections with one obvious answer: ordering bugs, contradictions between specs, missing fields, and template conformance.

| # | Finding | How it's applied |
|---|---|---|
| 1 | `install` crash loses `statusLine` | proposed-fixes-high §1 (write `install.json` first) |
| 2 | `send` text can break the bracketed paste | proposed-fixes-high §2 (refuse ESC and C0/C1 except `\t` `\n`) |
| 3 | shell command for `spawn`/`resume` | proposed-fixes-high §3 (argv out of band, POSIX and fish, `--` before the prompt) |
| 6 | precedence vs `spawn`/`resume` | proposed-fixes-high §6 |
| 7 | `terminal` `unavailable` for `send`/`focus` | proposed-fixes-high §7 |
| 9 | `prune` clock, `last_prune_at`, partial failure | review's fix |
| 11 | `doctor` findings with no kinds | review's fix (three new kinds) |
| 15 | `conflict` rule `busy` vs kind `busy` | rename the rule `mid-turn` |
| 17 | session view gaps | review's fix |
| 19 | `resume` null `cwd`, 0 timeout, output | null or gone `cwd` is `not-found` (`paths`); 0 timeout returns as read, no warning; add `job` |
| 20 | `send`/`focus` with unknown pid | review's fix |
| 21 | `spawn` leaking the caller's Claude env | review's fix |
| 22 | `install` self-test details | new kind `self-test-failed` |
| 23–34, 36–40 | Low | review's fixes |

## Needs you

Each is a behavior choice. Asked in batches of a few, each with a recommendation.

| # | Decision | Recommendation | Status |
|---|---|---|---|
| 4+5 | How selectors resolve (duplicated IDs, overlapping forms) | Shape decides the form, all digits is always a sesshin ID, a duplicated ID is always `ambiguous` | **Decided 2026-10-02: narrowed.** A selector is an exact sesshin ID (digits) or a full UUID (pending confirmation). No job, name, UUID-prefix, or `id:`/`job:`/`name:`/`uuid:` forms; selecting by job or name is the pickers' job. A duplicated sesshin ID is `ambiguous`, listing copies by UUID. Drop `conflict` `duplicate-id` and the `show` warning. Rewrite cli-spec examples (`sesshin send api`, `focus api`, `resume api`, `show job:12-factor`, `wait api`). |
| 18 | `sesshin resume api` when `api` names many ended sessions | Most recently seen ended session with that job | **Moot** (4+5: no job selectors). |
| 16 | `wait` is referenced but only planned | Specify it now (small) | batch 1 |
| 13 | `spawn` launch that times out | Treat as failed, but keep the reservation and let it go stale | batch 1 |
| 8 | Headless retention when `retain_days` is 0 | Headless window applies whenever non-zero | batch 2 |
| 35 | `focus` acks a session that isn't armed | Ack only when armed | batch 2 |
| 12 | `uninstall` and a `statusLine` changed since install | Restore only if still sesshin's; remove `install.json` | batch 2 |
| 14 | Which operations ask kitty whether a reservation's window is gone | `spawn`, `resume`, `prune`, `repair`, `doctor`, queried before the state lock | batch 2 |
| 10 | Ended session with an unusable `lifecycle.json` lives forever | New `repair` kind `unusable-session` | batch 2 |

## Applied

(filled in as fixes land)
