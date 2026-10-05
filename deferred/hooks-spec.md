# Deferred: hooks-spec.md

What the format-version upgrade path adds to [hooks-spec.md](../hooks-spec.md). The design side is in [design-spec.md](design-spec.md#format-versions-the-upgrade-path). Bring it back before sesshin has users (see [README](README.md#bringing-a-command-back)).

The text is as it stood when the scope was cut, minus what has since come back to the main spec or been dropped. Check it against the main spec before merging.

---

## Recording an event

In the main spec's [Recording an event](../hooks-spec.md#recording-an-event), step 2 reads `lifecycle.json`. With the upgrade path it splits:

- **Unusable, or an older format:** start from what can be [carried forward](design-spec.md#format-versions-the-upgrade-path).
- **A newer format:** log, unlock, and stop. A binary never downgrades a file.

And step 5, completing `sesshin.json`:

- **Missing, `id` `null`, unusable, or an older format:** [create it](../hooks-spec.md#creating-sesshinjson), starting from what can be carried forward (`id`, `job`, `source`, `placement`, each when it parses). An `id` that can't be read is issued afresh, never rebuilt.
- **A newer format:** log, and leave it alone.

## Creating sesshin.json

In the main spec's [Creating `sesshin.json`](../hooks-spec.md#creating-sesshinjson), issuing the ID gains one case: if `state.json` is in a newer format, leave `id` `null` and go on. Only `session-start` logs that, so a busy session's every tool call doesn't add a line.
