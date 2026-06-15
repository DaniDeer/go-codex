# Guide: Metrics Observer

For the full API reference, interface definitions, and Prometheus wiring, see the feature page.

**Feature:** [Metrics Observer](../features/observer.md)

## Observer location values by adapter

| Location | Adapter |
|---|---|
| `"body"` | nethttp/chi — request or response body |
| `"query"` | nethttp/chi — query param validation |
| `"cookie"` | nethttp/chi — cookie param validation |
| `"header"` | nethttp/chi — request header validation |
| `"path"` | nethttp/chi — path param validation |
| `"response_header"` | nethttp/chi — response header validation |
| `"payload"` | mqtt — message payload |
| `"topic_var"` | mqtt — per-variable codec failure |
| `"input"` | mcpgo — tool argument decode/validation |
| `"prompt.args"` | mcpgo — prompt argument codec failure |
| `"file"` | format.File — per-field codec failure during file read/write |

## Examples

- [examples/stats-observer](https://github.com/DaniDeer/go-codex/tree/main/examples/stats-observer) — codec-level `ValidationObserver` without any adapter (config file validation)
- [examples/adapters-nethttp](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-nethttp) — `CountingObserver` wired to HTTP adapter
- [examples/adapters-mqtt](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-mqtt) — `CountingObserver` for subscribe + publish
