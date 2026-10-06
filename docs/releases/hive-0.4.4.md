Hive v0.4.4 keeps aggregate workspaces available with newer Herdr clients and owners.

- Modern optional rendering offers no longer make healthy shared workspaces disappear during continuous output; Hive selects its supported generation-1 fallback codecs.
- Ordinary surface patches stay incremental, while switching sources restores the complete scene and graphics.
- Silent source initialization is bounded, and late operation replies cannot disconnect a healthy source or reach a newer request using the same client ID.
- `hive inspect` now reports aggregate viewer and additional upstream stream usage separately.

Compatibility: Herdr 0.9.0 through 0.9.3, and future versions retaining the generation-1 required codecs; existing Bee 0.4.0+ publishers remain supported.
Update Hive first with `hive update --state-dir PATH` for the aggregate compatibility fix.
Bee 0.4.2 separately fixes remote CLI context validation; simultaneous upgrades are not required.
Clients briefly reconnect while device identities, names and sharing settings are preserved.
