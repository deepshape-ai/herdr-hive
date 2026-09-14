Bee v0.3.2 brings an outline-only hexagonal layout to the Hive panel.

- Cells use the terminal background, with no filled tiles. Each shows the Bee
  name, connection dot and shared sessions; your device is marked YOU.
- Cells fill each row from the left and wrap as whole units when space runs out.
  There is no stagger or centering of incomplete rows.
- A shared cell width and height keep all edges aligned. Long names and multiple
  sessions wrap without hiding content, and narrow panes scroll vertically.

Compatibility: Herdr 0.9.0 and existing Hive installations. Sharing, permissions
and remote terminal control are unchanged. Reopen existing Bee panels to load
this layout. Updating Bee briefly reconnects sharing without stopping agents.
