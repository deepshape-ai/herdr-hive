Hive v0.3.2 fixes terminal resizing caused by background Hive connections.

- Discovering shared sessions no longer makes every publisher adopt the
  viewer's terminal size.
- Window resizes reach only the shared session currently being viewed.
  Switching sessions or returning to Local releases the previous session's
  surface interest; reopening uses the viewer's latest size.

Compatibility: unmodified Herdr 0.9.0 and existing Bee installations. No new
settings or connection steps are needed. Multiple people actively viewing the
same terminal still follow Herdr's shared sizing rules. Existing connections
briefly reconnect when Hive updates.
