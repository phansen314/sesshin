# Upgrading

The best way, from a kitty tab with no Claude session in it:

1. Exit every Claude session.
2. Replace both binaries side by side.
3. Run `sesshin migrate`. It brings old files to the new formats, keeping every session's ID, job, and `extra`. `sesshin migrate --dry-run` shows what it would change first.
4. Run `sesshin install` again, and apply its proposal if it has changes.
5. Run `sesshin restart`, and pick the sessions to bring back (see [restart](pickers.md#restart-bring-sessions-back-after-a-reboot)).

With nothing running between the swap and `migrate`, no session misses an event or starts without an ID. You can upgrade without stopping sessions too: they call the binary at the path `install` recorded, so they pick up the new one at their next hook. But until `migrate` runs, the new hooks leave files in an older format alone, so a session may miss events, show no ID in its statusline, or start without one ([why](../design-spec.md#format-versions)); the commands that read sessions warn `migration-pending` meanwhile. `sesshin version | jq .result.migration` is the latest step a binary knows. `install` records `sesshin-hook`'s path with symlinks resolved, so a package manager that installs through a symlink into a versioned directory (Homebrew's Cellar) breaks the hooks on its next upgrade, until you run `sesshin install` again and apply its proposal.

What each release changed, and whether it needs `migrate`, is in the [CHANGELOG](../CHANGELOG.md).
