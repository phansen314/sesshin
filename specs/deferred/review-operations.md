# operations.md review

A review of operations.md for correctness, internal consistency, and consistency with design-spec.md, hooks-spec.md, cli-spec.md, and README.md, as of 2026-10-02. Findings are ordered most severe first. Each gives the section, the problem, a failure scenario where one helps, and a suggested fix.

Settled decisions (no daemon, derived attention, the accepted false 🔐, `send` refusing mid-turn without `force`, `SESSHIN_TOKEN`, the per-session lock, etc.) are not argued here. They come up only where the text contradicts them.

---

## High

### 1. A crash during `install` can lose the user's own `statusLine` for good

**Section:** `install` Effects 2–4; design-spec `install.json` (`replaced_status_line`).

**Problem:** Step 3 overwrites `statusLine` in `settings.json`, and step 4 only then records the replaced value in `install.json`. A re-install carries `replaced_status_line` forward "when the `statusLine` was already sesshin's".

**Failure scenario:** On the first `install --replace-status-line`, sesshin crashes (or is killed) between steps 3 and 4. `settings.json` now runs sesshin's statusline, and no `install.json` exists. The user re-runs `install`. The `statusLine` is already sesshin's, so `replaced_status_line` is carried forward from a missing file, as `null`. `uninstall` later removes the statusline and restores nothing. Only the timestamped backup still holds the original, and nothing points to it.

**Fix:** Write `install.json`, including `replaced_status_line` and the backup path, *before* replacing `settings.json`. Also record `backup_path` in `install.json`, so that `uninstall` (or a person) can find the original even when the record is incomplete.

### 2. `send`'s "one bracketed paste" can be broken out of by the text itself

**Section:** `send` Input schema (`text`), Additional validation, Effects.

**Problem:** `text` is "delivered byte for byte", and "several lines arrive as one prompt". That holds only if the text cannot end the paste early. A `text` containing the bracketed-paste end sequence `ESC [ 2 0 1 ~` ends the paste there. Everything after it arrives as keystrokes, and a newline among them submits. Other C0 controls (Ctrl-C, Esc, Tab) arrive as keys too. Without `force`, the mid-turn guard exists to keep text out of dialogs, and this path walks around it.

**Failure scenario:** An agent pipes a file, or tool output, into `sesshin send 12 --text-file -`. The content happens to include `\e[201~` followed by a line `1`. The paste ends, `1` and Enter are typed as keys, and a menu or dialog that opens on the first part of the submitted text receives them.

**Fix:** Under Additional validation, reject (as `invalid-input`, `/text`) or strip ESC and C0 controls other than `\t` and `\n`, and state the rule. At minimum, forbid the paste-end sequence. Say which you chose in the `text` description. Also say whether kitty's `send-text --bracketed-paste` already sanitizes this. Even if it does, the spec should own the guarantee.

### 3. How `spawn` and `resume` build the shell command is unspecified, and a prompt can be read as a flag

**Section:** `spawn` Effects 2 (`claude <args…> [prompt]` through `spawn_shell`); `resume` Effects 2; design-spec Configuration (`spawn_shell`).

**Problem:** The command runs through `$SHELL -l -i`, which means a `-c` string. The spec doesn't say how `args` and `prompt` are quoted into that string. If they are concatenated, a prompt such as `fix $(whoami)'s bug` is shell-expanded. Separately, a prompt that starts with `-` (`-v is broken, fix it`) is parsed by `claude` as an option, and an `args` list ending in an option that takes a value (`--model`) swallows the prompt.

**Fix:** Specify that sesshin passes the argument vector out of band. One way is `$SHELL -l -i -c 'exec "$@"' sesshin claude <args…> -- <prompt>`, so that no argument is interpreted by the shell. Specify that the prompt is preceded by `--`, after checking that `claude` accepts `--` before its positional prompt. If it doesn't, reject a prompt that starts with `-` as `invalid-input`.

### 4. A duplicated sesshin ID has three contradictory outcomes

**Section:** Selecting a session (bullets "One or `ambiguous`" and "A duplicated sesshin ID"); `show` Warnings; `conflict` (`duplicate-id`); Precedence.

**Problem:**
- "Several matches in scope are `ambiguous`." A selector `12` that names two sessions matches several.
- "A duplicated sesshin ID refuses a write with `conflict` (`duplicate-id`)." In precedence, `conflict` is step 6 and `ambiguous` is step 4, so `ambiguous` would win first.
- "A read reports every copy, with a `duplicate-id` warning." But `show` returns exactly one `session`, and its Warnings table says only "the selected sesshin ID names several sessions" without saying which one it shows.

**Fix:** Pick one rule. A suggestion:
- Selecting by a duplicated sesshin ID is always `ambiguous`, for reads and writes alike, listing the copies, with the message pointing to `doctor`. A UUID selects one of them unambiguously.
- `list` keeps its `duplicate-id` warning.
- Drop `conflict` (`duplicate-id`) and the `show` warning row.

### 5. The selector forms overlap, and nothing says which one wins

**Section:** Selecting a session (table).

**Problem:**
- `12345678` is all digits (a sesshin ID) and also 8 hex characters (a UUID prefix).
- `deadbeef` or `cafe1234` is a valid job name and also a UUID prefix.
- It isn't stated whether a bare selector takes the *union* of every form it fits (and so can be `ambiguous` across forms) or tries the forms in a priority order.
- `name:` isn't defined: is it the `/rename` title and the statusline session name, or the derived `name`, which includes the job and `#id`?
- Case: are full UUIDs and prefixes matched case-insensitively? Directories are lowercased.
- Is a job matched as stored or as readers report it? The two differ for a live session whose job an earlier session holds.

**Fix:** Add one rule. For example: "All digits is a sesshin ID, always. Otherwise a bare selector matches the union of UUID prefix (when it is 8 or more hex characters), job, `/rename` title, and session name, and more than one distinct session is `ambiguous`." Define `name:` as title or session name, lowercase UUID input, and say that jobs match as readers report them.

### 6. The precedence model can't be followed by `spawn` or `resume`

**Section:** Error kinds (`invalid-input` "raised before any lock is sought or any file is read"); Precedence ("Steps 4–7 are checked under the operation's lock"); `spawn` Additional validation and Errors; `resume` Kind and Errors.

**Problem:**
- `spawn`: "no `cwd` after merging" is `invalid-input`, but it can only be known after the template file is read, and template `not-found` and `corrupt` are steps 4–5. A template can also supply the `job`, so the template has to be read *before* the state lock. That puts template errors ahead of `busy`, against the stated order.
- `resume` takes the lock only "if it resumes under a job", and the stored job is known only after the selector has been resolved. So selection happens before the lock, not under it. Its Errors table also lists `not-found`/`ambiguous` before `busy`, out of the stated precedence order.

**Fix:** Let operations declare a pre-lock phase. For example: "1. `invalid-input` (input alone). 2. `environment`, config `corrupt`. 3. Resolution without a lock: the selector (`not-found`, `ambiguous`), the template (`not-found`, `corrupt`), and post-merge validation (`invalid-input`). 4. `busy`. 5. Re-checks under the lock: `conflict`, and `not-found` if the session vanished. 6. `terminal`." Then reorder the `spawn` and `resume` tables to match. Loosen the `invalid-input` sentence to "before any lock is sought; post-template checks follow the template's own errors".

### 7. `terminal` (`unavailable`) for `send` and `focus` contradicts the design

**Section:** Error kinds (`terminal`, `reason` `unavailable`: "no backend recognizes this environment: not in kitty…"); `send` and `focus` Errors; the Operation kinds paragraph ("through the session's terminal backend").

**Problem:** The design spec says `send` and `focus` work "from a CLI outside kitty (ssh, cron)": they use the stored socket and its siblings, not the caller's environment. Yet `send`'s and `focus`'s Errors say "The backend is unavailable here". That sentence is true only for `spawn` and `resume`, which open windows in the *caller's* terminal. cli-spec's `info` example ("`terminal == "kitty"`: can spawn, send, and focus from here") repeats the conflation.

**Fix:** Define `unavailable` as "the caller's environment has no backend (`spawn`, `resume`)". For `send` and `focus`, the only failure to reach the stored socket and its siblings is `unreachable`. Fix the "session's terminal backend" sentence (`spawn` and `resume` use the caller's) and the cli-spec `info` example comment ("can spawn and resume from here").

---

## Medium

### 8. `prune` with `retain_days = 0` contradicts the design for headless sessions, and its output has one cutoff for two windows

**Section:** `prune` Preconditions, Output (`cutoff`); design-spec Retention.

**Problem:** operations.md says that with the config's `retain_days` at 0, "nothing is prunable". The design spec says a headless session is prunable after `retain_headless_hours`, with its own window, and that `retain_headless_hours = 0` means "kept as long as any other". So with `retain_days = 0` and `retain_headless_hours = 24`, the design prunes headless sessions daily, and operations.md prunes nothing. The design's own reason for the headless window is to stop hundreds of headless sessions piling up, and operations.md's reading lets them pile up. `prune-output.cutoff` is a single timestamp, though there are two cutoffs.

**Fix:** Decide the case and state it in both specs. The suggestion is that the headless window applies whenever it is non-zero, whatever `retain_days` is. Then replace `cutoff` with `cutoffs: {ended, headless}`, each nullable.

### 9. `prune` hides an untrusted clock, never says it updates `last_prune_at`, and has no partial-failure rule

**Section:** `prune` Effects, Errors, Warnings; design-spec `state.json` (`last_prune_at`).

**Problem:**
- An untrusted clock returns success with `cutoff` `null`, the same output as "retention is off". A caller can't tell a refusal from a policy.
- The design spec says `last_prune_at` is written by each prune that finishes, `prune` included. `prune`'s Effects never mention it, and don't say whether `dry_run` or a run that skipped locked sessions counts as finished.
- The state lock is taken in slices. If slice 3 can't get the lock within `cli_lock_wait_ms`, is the result `busy`? If so, it contradicts "an error means the operation's effects did not take place", because slices 1–2 have already pruned. If not, what is returned?
- Sessions skipped because their lock was held aren't reported.

**Fix:**
- Add a warning, `clock-untrusted` (`details`: `newest_started_at`, `now`).
- Add to Effects: "sets `last_prune_at` when it finishes, unless `dry_run`."
- Specify that after the first slice, a lock timeout ends the run with success, a `prune-incomplete` warning, and what was pruned so far, and that Retry safety covers it.
- Add `skipped: [session-ref]`, or a count, to the output.

### 10. An ended session with an unusable `lifecycle.json` can never be removed

**Section:** Findings (`unusable-file`: "suggests `prune`"); `prune` Warnings ("couldn't be judged; it is kept"); Reading the sessions.

**Problem:** The finding suggests `prune`, but `prune` keeps any session it can't judge. Reads skip the session, and no hook will rewrite the file once the session has ended. The directory stays forever, and `doctor` reports it on every run.

**Fix:** Add an on-request finding kind, such as `unusable-session`, for a session directory whose `lifecycle.json` is unusable or in an older format and older than `unknown_pid_ttl_secs`, and whose pid (if one can be read) is not running. `repair` removes it by renaming it aside, under its session lock. Point `unusable-file`'s suggestion at `sesshin repair --kinds unusable-session` when the file is `lifecycle.json`.

### 11. `doctor` promises findings that have no kinds

**Section:** `doctor` Preconditions ("A missing state directory … are findings"); Finding kinds.

**Problem:** The Finding kinds table has no kind for a missing state directory. It also has none for an unusable `install.json` (which `doctor` relies on for `location-mismatch`, `hooks-binary-mismatch`, and `uninstall`), or for a missing one, which means sesshin was never installed.

**Fix:** Add `state-dir-missing` (informational, or manual with suggestion `sesshin install`), `install-record-missing` (manual, `sesshin install`), and `install-record-unusable` (manual, `sesshin install`). Alternatively, drop "a missing state directory" from the sentence and fold it into `hooks-not-wired`.

### 12. `uninstall` can clobber a statusline set after install, and leaves `doctor` complaining

**Section:** `uninstall` Effects.

**Problem:**
- `statusLine` is restored from `install.json` unconditionally. If the user changed `statusLine` after installing sesshin, `uninstall` overwrites their newer choice with the old one, or removes it.
- `install.json` is left in place ("the state directory is left alone"). After `uninstall`, `doctor` still reads the record and reports `hooks-not-wired` on every run.
- The kitty block is removed even if the user now relies on remote control for other tools. That is acceptable, but should be reported.

**Fix:**
- Restore or remove `statusLine` only when it is still sesshin's. Otherwise leave it, and report it as `unchanged`.
- Have `uninstall` remove `install.json`, or mark it `uninstalled_at`, and have `doctor` treat an uninstalled record as "not installed", with no `hooks-not-wired`.

### 13. `spawn`/`resume`: the post-launch placement write and a launch timeout are unspecified

**Section:** `spawn` Effects 3 and Retry safety; `resume` Effects 3.

**Problem:**
- Recording the launched window takes the state lock again. If that wait exceeds `cli_lock_wait_ms`, the spec doesn't say what happens. Failing with `busy` would be wrong, because the session has launched. If the placement is silently left `null`, the reservation goes stale after `stranded_secs` (120). That is exactly what the placement exists to prevent while the session waits at the trust dialog.
- `launch-failed` is treated as "nothing was launched", but a `kitten @ launch` that *timed out* may have opened the tab. The reservation is then removed, the session's `SESSHIN_TOKEN` matches nothing, and by Adopt rule 3 it still takes the job if it's free. A retry, which the spec calls "safe", opens a second one.

**Fix:**
- After a successful launch, wait for the state lock up to `hook_lock_wait_ms`, not `cli_lock_wait_ms`. On timeout, succeed with a new warning (`placement-not-recorded`), and say in Retry safety that the reservation may go stale early.
- Split `launch-failed` (the backend refused) from a new `launch-unknown` (a timeout or unclear reply). For `launch-unknown`, keep the reservation, and mark the outcome not safe to retry.

### 14. Which operations talk to the terminal to judge a reservation stale is unspecified, and `spawn` would do it under the state lock

**Section:** `spawn` Effects 1 ("Under the state lock: … remove any stale reservations"); `prune`; `repair`; `doctor` (`stale-reservation`); design-spec Reservations ("Only the operations that talk to the terminal check the window").

**Problem:**
- "Its launched window gone" requires `kitten @ ls`, possibly over several sibling sockets with connect timeouts. If `spawn` does that while holding the state lock, it can hold off every new session's `SessionStart`, which waits at most 2000 ms. That goes against the "hooks hold it for milliseconds" premise.
- It also isn't said whether `prune`, `repair`, and `doctor` query kitty. If they don't, a launched reservation whose window was closed stays fresh for a day, and `doctor` never reports it.

**Fix:** Say which operations query the backend for reservation windows. The suggestion is `spawn`, `resume`, `prune`, `repair`, and `doctor`, but not `list` or hooks. Require that the query be made *before* the state lock is taken: collect the windows that exist, then judge under the lock, treating a reservation created since then as fresh.

### 15. `conflict` (`rule`: `busy`) collides with the error kind `busy`

**Section:** Error kinds (`conflict` rules, `busy` kind); `send` Errors.

**Problem:** `busy` the kind means "a lock was held, retry now and it's safe". `busy` the conflict rule means "the session's turn hasn't ended, wait for it". A caller's `jq '.. | .busy?'`, or a person skimming the stderr line `sesshin: conflict: …`, can confuse them. A generic retry loop keyed on the string "busy" would hammer a mid-turn session.

**Fix:** Rename the rule `mid-turn`, or `turn-active`.

### 16. `send` now depends on `wait`, which is only planned

**Section:** `send` Preconditions ("To prompt a busy session, `wait` for `waiting` first"); Planned operations; cli-spec `send` example `sesshin wait api --until waiting && …`.

**Problem:** Since `send` refuses mid-turn, `wait` is how agents are told to proceed, but it isn't specified, and cli-spec already uses it in an example. Until it exists, an agent has no documented way to wait.

**Fix:** Either specify `wait` now (it is small: a selector, `until` states, `timeout_secs`, and a poll on `event_seq` and `status`), or give the interim recipe in `send`: "poll `sesshin show <session> | jq .result.session.status` until `waiting` or `idle`". Mark the cli-spec example as planned.

### 17. The session view is missing what the pickers and debugging need

**Section:** Session view; cli-spec Pickers ("built on `list`"); design-spec Ack ("`at` is kept for display, 'acked 4m ago'") and Restart ("`(transcript gone)`").

**Problem:**
- The `ack` time isn't in the session view, so `watch` and `jump`, which are built on `list`, can't show "acked 4m ago".
- Whether the transcript exists is only on `show` (`transcript_exists`), so the `restart` picker, which is built on `list`, can't annotate "(transcript gone)" without a `show` per row.
- `pid`, `last_start_at`, `entrypoint`, and `nested` are omitted. These are what you need to see why `send` failed `unreachable`, why a session is headless, or which of two sessions sharing a process was superseded.
- `show-output.transcript_exists` isn't in `required`, though it is always computed.

**Fix:** Add `attention.acked_at` (nullable). Add `transcript_exists` to the session view, or document that the restart picker stats it itself. Add `pid`, `last_start_at`, `entrypoint`, and `nested`, as stored. Make `transcript_exists` required in `show-output`, or drop it in favor of the view field.

### 18. `sesshin resume api` will almost always be `ambiguous`

**Section:** `resume` (`session` "among ended sessions"); Selecting a session ("A job names at most one live session"); cli-spec `resume` example `sesshin resume api`.

**Problem:** Every `/clear` in a job's window ends one session and starts another that inherits `SESSHIN_JOB`. After a day's work, the job `api` names many ended sessions, so the headline example fails with `ambiguous`.

**Fix:** For ended-only scope, either define a bare job selector as "the most recently seen ended session with that job" (and say so in the `resume` input description), or change the cli-spec example to use an ID, and note that ended jobs are usually ambiguous. The first matches how people think of "resume api".

### 19. `resume`: missing or null `cwd`, `start_timeout_secs` 0, and the output

**Section:** `resume` Preconditions, Effects 2 and 4, Output.

**Problem:**
- `lifecycle.json`'s `cwd` can be `null`, or a directory deleted since. Neither case is specified.
- With `start_timeout_secs` 0, `spawn` returns `session` `null` with no warning. `resume`'s output says "still ended (with a `not-started` warning) if not", so a 0 timeout would always warn.
- The output has no `job`, unlike `spawn`'s, so a caller of `resume --job` can't see which job was claimed while the session hasn't started.

**Fix:** Make a missing or null `cwd` `not-found` (`paths`), or fall back to `HOME` with a warning; pick one. State that a 0 timeout returns the session as read, with no warning. Add `job: string|null` to `resume-output`.

### 20. `send` and `focus` on a session whose pid is unknown, and the pid match rule

**Section:** `send` Effects ("the window whose foreground process is the session's `pid`"); design-spec Liveness (Unknown pid: "`send`, `focus` … treat it as running").

**Problem:**
- With a `null` pid in both `lifecycle.json` and `statusline.json`, `send` can never verify a window, so it always fails with `unreachable`. That is safe, but the design text implies `send` works on such a session.
- It isn't stated that "the session's pid" means the one liveness used, which is the statusline's when `lifecycle.json`'s is `null`.
- A window's foreground process group can include Claude's tool children, so the match should be "among the foreground processes", not "is".

**Fix:** Say: "the pid liveness resolved (`lifecycle.json`'s, else `statusline.json`'s); the window whose foreground processes include it. With no pid, `send` fails `terminal` (`unreachable`) and `focus` falls back to the stored window." Amend the design-spec Unknown pid bullet to match.

### 21. `spawn` must not leak the caller's Claude environment into the new window

**Section:** `spawn` Effects 2; `resume` Effects 2; design-spec Liveness ("Started by another session").

**Problem:** Agents will run `sesshin spawn` from inside a Claude session, whose environment has `CLAUDECODE=1`, `CLAUDE_PID`, and possibly the caller's own `SESSHIN_JOB` and `SESSHIN_TOKEN`. If the backend copies the caller's environment (kitty's `--copy-env`, or a future backend's default), the spawned Claude reads as nested. It then gets no job and no placement, and `spawn` waits out its whole timeout and reports `not-started` for a session that started.

**Fix:** Add to Effects 2: "The new window's environment is the terminal's own, plus `SESSHIN_JOB` and `SESSHIN_TOKEN`. sesshin never copies the caller's environment, and removes `CLAUDECODE`, `CLAUDE_PID`, `CLAUDE_CODE_ENTRYPOINT`, and any inherited `SESSHIN_*` if a backend would." State the same rule for `resume`.

### 22. `install`'s self-test failure reports details that `internal` doesn't have

**Section:** Error kinds (`internal`: `details` "none (`{}`)"); `install` Errors (`internal`: "`details` says which hook and what it wrote").

**Problem:** The two sections disagree about `internal`'s details. A self-test failure is also often environmental (an unwritable temp directory, or a `noexec` mount), not a bug.

**Fix:** Add an error kind, `self-test-failed` (`details`: `hook`, `detail`), used only by `install`, and keep `internal` as `{}`.

---

## Low

23. **"Derived, not stored" lists the burn rate,** but the burn rate is stored in `statusline.json` and "reported as stored" (design-spec, and the session view's own description). Remove "burn rate" from the Conventions bullet.
24. **`busy` (`lock`: `session`) is never raised.** Operation kinds says "a held session lock fails at once", but the only operations that take one (`focus`, `send`) go ahead without it, and `prune` and `repair` skip. Either drop `session` from `busy`'s `lock` values, or reword: "an operation that needs a session lock tries it without waiting; each says what it does when the lock is held." Update design-spec Concurrency to match.
25. **"Job names are the design spec's name rule, with one addition: not all digits."** The design spec's rule and its `defs.job` pattern already include "not all digits". Reword as "Job names follow the design spec's rule (not all digits, so `12` always means a sesshin ID)."
26. **`not-started` details** are "`job`, or `uuid`", but a `spawn` with no job knows neither. Add `placement` as the third option.
27. **The scope exception is one-sided.** The shared rule turns a live-only operation's ended-session ID into `conflict` (`not-live`). `resume`, an ended-only operation given a live session's ID or UUID, uses `conflict` (`live`), but the shared rule doesn't say so. Add the mirror sentence.
28. **`hooks-binary-mismatch` ("or gone") overlaps `hooks-not-wired`** ("points at a binary that doesn't exist"). Keep "gone" in one of them only.
29. **`install --kitty-remote-control` is under-specified.** The spec doesn't say which `kitty.conf` (`$KITTY_CONFIG_DIRECTORY`, `$XDG_CONFIG_HOME/kitty`, `~/.config/kitty`), what the block contains (`allow_remote_control` and `listen_on unix:/tmp/kitty`), or that kitty must be restarted. "Unless remote control is already on" can only be judged from the environment, by the design's "probed, never parsed" rule. The appended path and its backup aren't recorded in `install.json` for `uninstall`. The `terminal-unavailable` suggestion could then be the command `sesshin install --kitty-remote-control`, rather than prose. That also fits the finding schema, whose `suggestion` is "a command".
30. **"Leftovers are judged by age"** (Operation kinds, diagnostic) isn't true of `prune-leftover`, which has no age threshold, unlike `temp-leftover` and `incomplete-session`. Add "more than 60 seconds old".
31. **Template conformance.** `version` and `info` give "an empty object" instead of a JSON Schema with `$id` `<op>-input`. `repair` (`repaired`, `remaining`) and `install` (`changes`) return collections without an **Order** part. The `info-output` sub-objects lack `additionalProperties: false`, unlike every other schema.
32. **`send.text` `maxLength` 1048576 counts code points,** not bytes, though the limit reads as 1 MiB. State the byte limit under Additional validation.
33. **Placement: "a caller may show it and pass it back".** No operation takes a placement as input. Drop "pass it back", or name where it would go.
34. **`ambiguous` caps `candidates` at 20 with no truncation flag.** Add `candidates_truncated`, as `invalid-input` has `problems_truncated`.
35. **`focus` acks a session that isn't armed.** It writes `{event_seq}` regardless of attention. Focusing a `working` session that then hangs, with no further events, leaves it acked when it reaches `stuck_secs`, so 🥱 never shows. If that is intended ("an ack is for this silence"), say so in `focus`. Otherwise write the ack only when the session is armed, and define `focus-output.acked` accordingly.
36. **`install` order.** Effects run the self-test first, but the Errors table checks `settings.json` (`corrupt`, `conflict`) before `internal`. Run the cheap `settings.json` checks first, so that the documented order is the real one. Step 3's "Set `statusLine` to `sesshin hook statusline`" should say the absolute path, as the hook entries do. Also say how a herd installation's entries are treated: they are replaced if they match `sesshin hook <verb>`, and otherwise left, which would double every event.
37. **Template values are unvalidated.** A template's `job` that breaks the name rule, a `cwd` with `~/` (cli-spec expands `~` only for `--cwd` and `--input`), or a relative `cwd`: is the template `corrupt`, or is it `invalid-input`? Suggest `corrupt` for type and shape errors in the file, and `~/` expansion for a template's `cwd`.
38. **`doctor --kinds` and `healthy`.** It isn't said whether `healthy` covers only the requested kinds, which is the natural reading. Say so.
39. **The `list-output.reservations` description** says "spawned sessions not yet started", but `resume` makes reservations too. Say "launched by `spawn` or `resume`, not yet started".
40. **`send`'s Errors table** doesn't say that `terminal` (`command-failed`) may have had effects. Only Retry safety does, but the envelope section says that an error means no effects "except as its Errors table says". Add the caveat to the table row.
