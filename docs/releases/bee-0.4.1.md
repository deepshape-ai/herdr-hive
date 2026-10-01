Bee v0.4.1 restores sharing automatically after Herdr updates and restarts.

- Shared sessions reconnect automatically when Herdr replaces their endpoints,
  so teammates can access them again without toggling Bee sharing.
- Sessions whose endpoints disappear are withdrawn from Hive until Herdr is
  ready to share them again.

Compatibility: Herdr 0.9.0 and newer, including 0.9.3; remote API commands
require Hive 0.4.0 and Bee 0.4.0 or newer on both devices.
Update the publishing Bee to receive this fix; Hive needs no update.
Updating Bee briefly reconnects sharing without stopping local agents.
