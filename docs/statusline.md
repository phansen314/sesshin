# What the statusline shows

Inside a Claude Code session, sesshin's statusline shows that session and no other. It is two lines:

```text
#12 | ⬢ api refactor | 🧠 35% 351k/1M | ♨️ until 3:04PM | 📁 sesshin | 🌿 main | 🤖 Opus 5.5 | 💰 $1.20 | 🔥 $3.40/h | ⌛ 12m API
⏱️ 5h 22% resets 3:00PM | 7d 41% resets 10/6 9:00AM
```

A segment with nothing to report is left out rather than shown as zero: "0%" and "not reported" are different things. The exact rules for each format are in [hooks-spec.md, Rendering](../hooks-spec.md#rendering).

## The first line: this session

| Segment | What it means | Missing when |
|---|---|---|
| `#12` | The session's **sesshin ID**. Use it with any command: `sesshin show 12`, `sesshin focus 12`. | The session has no ID yet (in its first moments, or until `sesshin migrate` runs after an upgrade). |
| `⬢ api refactor` | The session's **name**: its `/rename` title, else the name it was started with (`spawn --job`, `claude --name`). | It has neither. |
| `🧠 35% 351k/1M` | **Context used**: the share of the context window, then the tokens in it out of the window's size. | Never: before the first response it reads `🧠 0%`. |
| `♨️ until 3:04PM` | **Prompt cache is warm** until that time. Answer before then and the next turn reuses the cache. | See below. |
| `🧊 ~45k` | **Prompt cache is cold**: the next turn will re-cache about that many tokens, which costs more. `🧊 cold` when Claude Code doesn't say how many. | See below. |
| `📁 sesshin` | The **directory** the session is in (its last part). | |
| `🌿 main` | The **git branch** of that directory. | Not in a repository. |
| `🤖 Opus 5.5` | The **model**. | |
| `💰 $1.20` | What the session has **cost** so far. | |
| `🔥 $3.40/h` | The **burn rate**: how fast it is spending now, per hour, over the last few minutes. | Until there's a minute of spending to measure, and while the session is idle. |
| `⌛ 12m API` | **Time spent waiting on the API**, in total. | No API call yet. |

### The prompt cache

Claude Code caches the conversation so that each turn doesn't pay to resend it. The cache expires a while after the last turn: 5 minutes or an hour, depending on your account. `♨️ until 3:04PM` tells you when, and `🧊` tells you it has already expired.

It shows a clock time and not a countdown because Claude Code doesn't redraw the statusline of an idle session. A countdown would freeze, but the expiry time stays true. Claude Code does redraw it once more when the cache expires, so `♨️` turns into `🧊` on its own.

Neither appears when the cache state is unknown: before the first response, or when caching is off or not reported.

## The second line: your account

| Segment | What it means |
|---|---|
| `⏱️ 5h 22% resets 3:00PM` | How much of your **5-hour rate limit** is used, and when it resets. |
| `7d 41% resets 10/6 9:00AM` | The same for the **weekly limit**, with the reset's date. |

These are per account, not per session: every session shows the same numbers. The line is missing until Claude Code reports them, which is after the first response.

## When it updates

Claude Code redraws the statusline when something changes: a new message, a finished `/compact`, a permission-mode change, the cache expiring. It doesn't redraw during a long tool call or on a timer. So an idle session's line is as of its last change, which for every segment but the burn rate is still true.

## For scripts

The line's layout may change in any release. Scripts should read the data instead: `sesshin show <id>` reports everything the line shows and more. The raw data behind the line is in the session's `statusline.json`, which keeps Claude Code's whole payload as it was sent. [`sesshin list`](../operations.md#list) reports the same data for every session.
