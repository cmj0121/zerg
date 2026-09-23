# zerg-lsp — Claude Code

This directory is the `zerg-lsp` plugin that `.claude-plugin/marketplace.json` at the root of the
checkout lists. The plugin is only a config: it tells Claude Code to start `zerg lsp` for a `.zg`
file. It has no server of its own and no code.

```text
/plugin marketplace add ./
/plugin install zerg-lsp@zerg
```

Restart the session afterwards so the server loads. See
[`docs/tooling/lsp.md`](../../docs/tooling/lsp.md#claude-code) for what the agent gets from it
and why it runs the `zerg` on `PATH`.
