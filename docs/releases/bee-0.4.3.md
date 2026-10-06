Bee v0.4.3 reuses publisher-owned terminal size controllers across rapid viewer reconnects.

- Unused controllers remain available for two seconds before restoring local sizing, avoiding repeated detach/attach cycles during rapid reconnects while keeping the process-wide 32-controller limit.
- Fresh viewers use current native resources, and stale snapshots or removed panes no longer interfere with another viewer's healthy controllers.
- Classified failures reach Hive through an optional SSH request without changing native Herdr stream bytes.

Compatibility: verified with Herdr 0.9.0 through 0.9.3; existing Hive versions remain interoperable.
Update Bee with `bee update` on publishers and Hive to 0.4.5 for classified recovery; local agents continue running.
Existing direct terminal controllers are never displaced, and initial attach/final detach may still trigger native redraws.
