Bee v0.4.2 keeps remote CLI commands independent of local pane and filesystem context.

- `bee on` now rejects `pane focus --current`, including selectors following value options, instead of applying it to the remote owner's current pane.
- Remote command validation handles command-specific and equals-valued options without confusing literal prompts, labels or environment values with context selectors.

Compatibility: Herdr 0.9.0 through 0.9.3, and future versions retaining the supported native command and API contracts; remote API commands require Hive 0.4.0 and Bee 0.4.0 or newer on both devices.
Update the Bee running `bee on` to receive this fix; updating publishers is recommended but not required for Hive 0.4.4's aggregate compatibility fix.
Updating Bee briefly reconnects sharing without stopping local agents.
