# go-codex Documentation Review History (DR1–)

Do not re-report any findings listed here. They have been implemented.

---

## Round DR1 (doc.go quality + concept cross-links)

- **D1 — `api/internal/doc.go` thin (3 lines)**: Expanded to 13 lines documenting `ParseTemplateVars`, `StripTemplateVars`, and `BuildFromTemplate` with their roles in template-transparent validation.
- **D2 — `render/jsonschema/doc.go` thin (8 lines)**: Expanded to 18 lines documenting the `Schema()` function, its use by `api/mcp`, and its relationship to `render/internal/schemarender`.
- **D3 — `render/internal/schemarender/doc.go` thin (6 lines)**: Expanded to 18 lines documenting `SchemaObject`, the single-change design rationale, and the `AdditionalPropertiesSchema` field precedence rule.
- **D4 — `docs/concepts/api-contracts.md` See also links stale**: Four `guides/` links replaced with correct `features/` links (REST API, HTTP Client, Events, MCP, API Builders).
- **D5 — `docs/concepts/codec.md` See also links stale**: `guides/error-handling.md` replaced with `features/error-handling.md`.

---

<!-- New rounds go here, above the previous round. -->
