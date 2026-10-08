# Use it from Claude Code and OpenCode

The [sesshin skill](../claude/skills/sesshin/SKILL.md) teaches the agent the commands: spawning sessions under a job, watching them with `list` and `show`, bringing an ended one back with `resume`, typing into a live one with `send`, bringing its window to the front with `focus`, tagging a session with `update`, and when not to retry. Agents never run `restart` or `jump`: they are pickers for you. Claude Code gets it from the `sesshin` plugin (the repo is a Claude Code plugin marketplace). Until it is published, add the marketplace from a clone:

```sh
claude plugin marketplace add ~/code/sesshin
claude plugin install sesshin@sesshin
```

`sesshin install` ([README](../README.md#install)) also proposes the permission rules, so they arrive in the same reviewed diff as the hooks: `sesshin` and `jq` run without a prompt, while `install` and `uninstall`, which propose changes to Claude Code's setup, and `prune`, which deletes sessions, still ask. `sesshin uninstall` takes them out again, except `jq`'s, which other tools share. To try an edited skill without updating the plugin, run `claude --plugin-dir .` in a clone; `claude plugin update sesshin@sesshin` picks up changes.

Then ask your agent things like "spawn a session on ~/code/api to run the tests, job api" or "which of my sessions are waiting on me?".

For OpenCode, `scripts/opencode.sh` adds the same rules to `~/.config/opencode/opencode.json` and links the skill into `~/.config/opencode/skills/sesshin` (`--uninstall` takes both out). It needs `jq`, backs the file up first, touches only sesshin's rules, and is safe to rerun. By hand, the rules go after any other rule that matches sesshin, since in OpenCode the last match wins:

```json
{
  "permission": {
    "bash": {
      "sesshin *": "allow",
      "jq *": "allow",
      "sesshin install*": "ask",
      "sesshin uninstall*": "ask",
      "sesshin prune*": "ask"
    }
  }
}
```

Link the skill into OpenCode's directory, not `~/.claude/skills`: OpenCode reads that as well, and Claude Code would load the skill a second time next to the plugin's.
