Bee v0.4.0 lets agents manage agents and panes on other Bees through Hive.

- Use `bee targets` to find shared sessions and `bee on B/research -- herdr ...`
  to create, prompt, inspect and wait for agents, or manage panes, tabs and
  workspaces with the native Herdr CLI.
- Join Hive with one invitation from the Bee panel or `bee join`, including
  automatic setup of the Hive connection in Herdr.
- Shared terminals keep a stable size while remote tabs are open, preventing
  repeated resizing when viewers use different window sizes.

Compatibility: unmodified Herdr 0.9.0; remote commands require Hive 0.4.0 and
Bee 0.4.0 on both devices, with the target session shared by its owner.
Existing terminal sharing remains compatible with older installations.
Updating Bee briefly reconnects sharing without stopping local agents.
