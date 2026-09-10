# Ghost

Ghost is a CLI/TUI harness for supervising coding-style agents. Phase 0 is the first runnable shell: it contains mock agents, navigation, local steering events, configuration, structured diagnostics, and the Bloodwire theme.

Status: Phase 0 scaffolding. Provider integration is not implemented.

Screenshot: a terminal screenshot will be added after the first visual review.

## Build and run

Requirements: Go 1.26 or newer.

```text
make build
make run
```

Ghost runs without a configuration file. To select a theme later, copy `ghost.toml.example` to `ghost.toml`.

## Controls

- Up or `k`: previous agent
- Down or `j`: next agent
- Enter: open the selected agent
- Esc: return to the dashboard
- Tab: change focus
- `?`: show key help
- `q`: quit when no text input is focused
- Ctrl+C: quit at any time

Global and per-agent steering inputs create local mock events only. They do not contact an agent or model.

## Development commands

```text
make test
make vet
make fmt
```

The current MVP deliberately excludes provider runtimes, agent loops, QAC, memory, MCP, tools, persistence, authentication, quotas, and autonomous execution.
