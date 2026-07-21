# SSE & WebSocket Merge Hardening — post-ship follow-up

> **Status:** Design draft.
> [← Back to Roadmap](index.md)
>
> Replaces the now-shipped design-only doc
> `sse-websocket-merge-field-gaps.md`.

SSE and WebSocket connection-level merge are now implemented:

- SSE: `rest.NewRequiredSSEEventParam` / `NewOptionalSSEEventParam`,
  `SSERouteHandle.MergeEvent`, adapter auto-merge in
  `adapters/nethttp`/`adapters/chi`.
- WebSocket: `SocketPattern.InOpts`/`OutOpts`,
  `NewRequiredSocketInParam` / `NewOptionalSocketInParam`,
  `NewRequiredSocketOutParam` / `NewOptionalSocketOutParam`,
  adapter auto-merge in `adapters/websocket`.

This roadmap tracks only remaining hardening discovered during
post-implementation review.

## Findings from review

| ID | Boundary | Severity | Finding |
|---|---|---|---|
| H1 | SSE docs/examples | small | "One struct, one call" for SSE event-side merge is now real but still under-demonstrated compared to REST/events/reqreply (especially nested/binary composition examples). |
| H2 | WebSocket docs/examples | small | WebSocket merge (`InOpts`/`OutOpts`) is implemented but needs a dedicated nested/non-JSON demo showing connection vars merged into structured payloads. |
| H3 | Adapter-level coverage | small | Merge behavior is tested for standard JSON flows; add explicit hardening tests for non-default formats (e.g. Gob/custom typed format) and nested field access under connection-var merge. |

## Scope (hardening only)

1. Add one SSE example snippet (or runnable example) with nested struct merge
   (`Meta` sub-struct) and non-default format composition.
2. Add one WebSocket example snippet (or runnable example) with
   `InOpts`/`OutOpts`, path/query/header vars, and targeted+broadcast outbound
   merge.
3. Add targeted tests proving merge remains format-agnostic and nested-field
   compatible for SSE and WebSocket.

## Out of scope

- New merge APIs (feature already shipped).
- Behavior changes to precedence (keep: connection vars override payload).
- New observer interfaces or error types.
