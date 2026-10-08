# Use it from Claude Code and OpenCode

The [sesshin skill](../claude/skills/sesshin/SKILL.md) teaches the agent the commands: spawning sessions under a job, watching them with `list` and `show`, bringing an ended one back with `resume`, typing into a live one with `send`, bringing its window to the front with `focus`, tagging a session with `update`, and when not to retry. Agents never run `restart` or `jump`: they are pickers for you. Claude Code gets it from the `sesshin` plugin (the repo is a Claude Code plugin marketplace). Add the marketplace from GitHub and install the plugin:

```sh
claude plugin marketplace add phansen314/sesshin
claude plugin install sesshin@sesshin
```

`sesshin install` ([README](../README.md#install)) also proposes the permission rules, so they arrive in the same reviewed diff as the hooks: `sesshin` runs without a prompt, while `install` and `uninstall`, which propose changes to Claude Code's setup, `prune`, which deletes sessions, and `resume`, which brings an ended session back, still ask. `sesshin uninstall` takes them out again.

**What the `allow` rule lets an agent do.** `spawn` and `send` run without asking, so any session can start another session or type into one. That includes a `spawn` with claude arguments that skip permissions (`sesshin spawn -- --dangerously-skip-permissions`), and a `send` to a session that already skips them: a session you restricted, or one a prompt injection steered, can hand its work to one you didn't. If that matters to you, add `"Bash(sesshin spawn:*)"` and `"Bash(sesshin send:*)"` to `permissions.ask` yourself; `install` never removes a rule you added.

**`jq` is yours to allow.** The skill's examples pipe `sesshin` into `jq`, which asks each time unless you allow it. `install` doesn't propose `"Bash(jq:*)"`, since it also lets an agent read any file with `jq` without asking; add it to `permissions.allow` if you accept that. `--fields` cuts most of what the examples need `jq` for.

To try an edited skill from a clone, add the clone as the marketplace instead (`claude plugin marketplace add /path/to/clone`) or run `claude --plugin-dir .` in it; `claude plugin update sesshin@sesshin` picks up changes.

Then ask your agent things like "spawn a session on ~/code/api to run the tests, job api" or "which of my sessions are waiting on me?".

For OpenCode, `scripts/opencode.sh` adds the same rules to `~/.config/opencode/opencode.json` and links the skill into `~/.config/opencode/skills/sesshin` (`--uninstall` takes both out). It needs `jq`, backs the file up first, touches only sesshin's rules, and is safe to rerun. By hand, the rules go after any other rule that matches sesshin, since in OpenCode the last match wins:

```json
{
  "permission": {
    "bash": {
      "sesshin *": "allow",
      "sesshin install*": "ask",
      "sesshin uninstall*": "ask",
      "sesshin prune*": "ask",
      "sesshin resume*": "ask"
    }
  }
}
```

Link the skill into OpenCode's directory, not `~/.claude/skills`: OpenCode reads that as well, and Claude Code would load the skill a second time next to the plugin's.
