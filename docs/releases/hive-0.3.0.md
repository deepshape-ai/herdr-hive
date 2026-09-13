Hive v0.3.0 adds one native Herdr gateway for multiple Bee publications.

- Add an SSH alias with `User hive`, then run `herdr machine add hive --label Hive`.
  Workspaces appear as `[Bee name] session / workspace` without modifying Herdr.
- Focus, terminal input and supported workspace/tab/pane operations route to the
  owning session. Namespace separation prevents collisions between local IDs.
- Offline sources disappear and return after reconnect; your own publications
  remain hidden. Existing `User s-…` direct connections remain available.
- Generation-1 frames, current surfaces, image assets and outstanding operations
  have explicit bounds. A failed source is isolated from the rest of the viewer.

Compatibility: unmodified Herdr 0.9.0 and Bee v0.2.0. The aggregate endpoint permits
2 simultaneous viewers and 8 sessions per viewer; see the Hive README for limits
and operations requiring direct sharing-ID access. Enrollment tokens are unchanged.

Validation: race-enabled Go tests, vet, installer checks and native three-publisher
integration covering names, focus, input isolation, self-hiding, reconnect and
existing transparent connections.
