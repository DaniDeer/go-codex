# Vector Store Adapter (RAG retrieval) — `adapters/vectorstore`

> **Status:** Awaiting use case — placeholder / not yet designed in detail.
> [← Back to Roadmap](index.md)
>
> See also: [LLM Integration](llm-integration.md) · [Redis Cache Adapter](../features/redis.md) · [SQL Adapter](../features/sql.md)

---

## Motivation

Retrieval-Augmented Generation (RAG) = **retrieval** (find relevant context for a query) + **augmentation** (fold that context into a prompt) + **generation** (call the LLM). [`llm-integration.md`](llm-integration.md) designs the generation half (`api/llm` + `adapters/openai`). This page is the placeholder for the retrieval half — a **vector similarity search adapter** — plus the one piece of `adapters/openai` that `llm-integration.md` deliberately deferred: **embeddings**.

This is a reminder/placeholder page, not a finished design — captured now so the need and its dependencies aren't lost, to be fully specified once a concrete driving use case appears (same bar as [`stream-flatmap.md`](stream-flatmap.md)'s "awaiting use case" status).

---

## What already works today, zero new code

Worth stating explicitly so this isn't over-scoped later:

- **Postgres + `pgvector`** retrieval already works via the existing generic `adapters/sql.QueryEachAdapter[In, T]` — its query is a fully user-supplied closure (raw SQL + driver), so `SELECT ... ORDER BY embedding <-> $1 LIMIT $2` (pgvector's distance operator) needs no go-codex change at all. Same for any other Postgres-extension-based similarity search.
- **Representing an embedding vector** needs no new codec — `codex.SliceOf(codex.Float32())` (or `codex.Float64()`) already gives a validated `Codec[[]float32]` with `MinItems`/`MaxItems` available if a fixed dimensionality needs enforcing (see the array-length-constraints work already shipped in `codex`/`validate`).
- **Composing retrieval → augmentation → generation as a pipeline** needs no new `ports` mechanism — it is exactly the same "one `IOPort` feeds another" composition already used throughout go-codex (`examples/order`-style chaining, or `ports.PipePort`/`Chain`/`ChainStream` for multi-stage pipelines). A retrieval `IOPort[Query, []Chunk]` followed by an LLM `IOPort[RAGRequest, Answer]` (where `RAGRequest` simply has a `Context []Chunk` field alongside the user's question) composes with zero port-layer changes.

## What's genuinely missing

1. **`adapters/openai` embeddings support** — `llm-integration.md` explicitly deferred "non-chat-completions OpenAI endpoints (embeddings, moderation, image generation)" as out of scope. RAG needs the embeddings endpoint specifically (turning a chunk of text into a `[]float32` vector before it can be stored or queried). This is a small addition — same HTTP client/auth/error infrastructure as `adapters/openai.CallAdapter`, just a different endpoint path (`/embeddings`) and response shape (a vector, not a chat completion) — likely an `EmbedAdapter` implementing `ports.IOAdapter[Req, []float32]` (or `IOAdapter[Req, EmbeddingResult]` if token-usage metadata should travel with the vector). **Depends on `llm-integration.md` shipping first** (shares its HTTP client setup, error types, and observer conventions).
2. **A native vector-store adapter** — for vector databases that are NOT reachable through `database/sql` (Qdrant, Weaviate, Pinecone, Milvus) or that need their own query surface (Redis vector search via `FT.SEARCH`/RediSearch, distinct from the existing `adapters/redis.Commands` key-value surface). The natural shape, mirroring `adapters/redis`'s narrow `Commands` interface precedent: a small `VectorStore` interface (roughly `Upsert(ctx, collection string, items []VectorRecord) error` + `Query(ctx, collection string, vector []float32, topK int) ([]Match, error)`) that a concrete client wraps — keeping go-codex free of any specific vector-DB SDK dependency, exactly like `adapters/redis.NewCommands(goredisClient)` wraps go-redis.
3. **Chunking strategy** — splitting long documents into overlapping windows before embedding. Likely **out of scope for go-codex itself** (a domain/business decision, not infrastructure — plain Go string/rune slicing, no codec or adapter involved) but worth a documented recommendation/example rather than new API surface. Revisit if a concrete pattern proves broadly reusable.
4. **Hybrid search / reranking** — combining vector similarity with keyword (BM25) search, or a reranking pass over retrieved candidates. Not evaluated yet — likely a Phase 2+ concern once Phase 1 retrieval exists.

## Rough shape (to refine when this gets designed properly)

```go
// adapters/vectorstore — narrow interface, mirrors adapters/redis.Commands.
type VectorStore interface {
    Upsert(ctx context.Context, collection string, items []VectorRecord) error
    Query(ctx context.Context, collection string, vector []float32, topK int) ([]Match, error)
}

type VectorRecord struct {
    ID       string
    Vector   []float32
    Metadata map[string]any // or a typed payload via a codec, TBD
}

type Match struct {
    ID       string
    Score    float64
    Metadata map[string]any
}

// QueryAdapter/UpsertAdapter would implement ports.IOAdapter[Query, []Chunk] /
// ports.SinkAdapter[Chunk] the same way adapters/redis's Get/SetAdapter do —
// exact shape TBD when this is designed in full.
```

```go
// adapters/openai — embeddings addition (small, once llm-integration.md ships)
func EmbedAdapter(client *http.Client, opts EmbedAdapterOptions) ports.IOAdapter[string, []float32]
```

## Open questions (for the real design pass)

- One generic `VectorStore` interface with pluggable distance metrics, or per-vendor adapter packages (`adapters/qdrant`, `adapters/pinecone`) like `adapters/mqtt`/`adapters/mqtt5` are separate packages for different protocol versions?
- Does `VectorRecord.Metadata` stay `map[string]any` (handle-less, `IOParam`-style, matching `adapters/sql`'s row-shape flexibility) or get a proper codec-validated typed payload (matching `ports.Cache[T]`'s typed value)?
- Should `EmbedAdapter` batch multiple texts per request (most embedding APIs accept an array input) — almost certainly yes, but changes the adapter's shape (`IOAdapter[[]string, [][]float32]` vs one-at-a-time) — decide once real usage patterns are known.
- Fixed vector dimensionality enforcement — `validate.MinItems`/`MaxItems` on the embedding codec (e.g. exactly 1536 for `text-embedding-3-small`) is already available with zero new code; just needs to be documented as the recommended pattern when this ships.
