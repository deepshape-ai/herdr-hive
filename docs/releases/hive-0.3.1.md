Hive v0.3.1 shortens shared workspace names in Herdr's sidebar.

- Default sessions now appear as `[Alice] ops` instead of `[Alice] default / ops`.
- Named sessions appear as `[Alice/research] ops`, keeping the source first and
  leaving more room for the workspace name.

Compatibility: unmodified Herdr 0.9.0 and Bee v0.2.0. Session IDs, routing and
workspace names on the publishing device are unchanged. Existing connections
briefly reconnect when Hive updates.
