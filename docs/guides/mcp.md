# Guide: MCP Server

For the full API reference and all code examples, see the feature page.

**Feature:** [MCP Server — Tools, Resources, Prompts](../features/mcp.md)

## examples/adapters-mcp

Demonstrates all three MCP primitives:

- **Tools** — `CalcTool` with codec-driven `inputSchema`; input validation errors surface to the LLM as `IsError: true` results; output codec failures are protocol-level errors
- **Resources** — `ItemResource` with URI template `items://{id}`; `BuildURI` validates the `id` parameter before assembling the URI
- **Prompts** — `SummaryPrompt` with required `content` arg and optional `style`; `ValidateArgs` returns `MissingPromptArgError` for absent required args
- `b.MCPSpec()` — static JSON manifest listing all primitives with their codec-derived JSON Schemas
- All three transport options: stdio, streamable HTTP (recommended), SSE over HTTP (legacy)

→ [examples/adapters-mcp](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-mcp)
