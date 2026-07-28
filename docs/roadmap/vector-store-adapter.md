# Vector Store Adapter (RAG retrieval) — `adapters/vectorstore`

> **Status:** Awaiting use case — placeholder / not yet designed in detail.
> [← Back to Roadmap](index.md)
>
> See also: [LLM Integration](../features/llm-integration.md) · [OpenAI Multimodal Content](openai-multimodal-content.md) · [Redis Cache Adapter](../features/redis.md) · [SQL Adapter](../features/sql.md)

---

## Motivation

Retrieval-Augmented Generation (RAG) = **retrieval** (find relevant context for a query) + **augmentation** (fold that context into a prompt) + **generation** (call the LLM). [LLM Integration](../features/llm-integration.md) implements the generation half (`api/llm` + `adapters/openai`). This page is the placeholder for the retrieval half — a **vector similarity search adapter** — plus the one piece of `adapters/openai` that the LLM Integration feature deliberately deferred: **embeddings**.

This is a reminder/placeholder page, not a finished design — captured now so the need and its dependencies aren't lost, to be fully specified once a concrete driving use case appears (same bar as [`stream-flatmap.md`](stream-flatmap.md)'s "awaiting use case" status).

---

## What already works today, zero new code

Worth stating explicitly so this isn't over-scoped later:

- **Postgres + `pgvector`** retrieval already works via the existing generic `adapters/sql.QueryEachAdapter[In, T]` — its query is a fully user-supplied closure (raw SQL + driver), so `SELECT ... ORDER BY embedding <-> $1 LIMIT $2` (pgvector's distance operator) needs no go-codex change at all. Same for any other Postgres-extension-based similarity search.
- **Representing an embedding vector** needs no new codec — `codex.SliceOf(codex.Float32())` (or `codex.Float64()`) already gives a validated `Codec[[]float32]` with `MinItems`/`MaxItems` available if a fixed dimensionality needs enforcing (see the array-length-constraints work already shipped in `codex`/`validate`).
- **Composing retrieval → augmentation → generation as a pipeline** needs no new `ports` mechanism — see [Orchestration — how retrieval and generation compose](#orchestration--how-retrieval-and-generation-compose) below for the concrete pattern.

## Orchestration — how retrieval and generation compose

This is the direct answer to "do we need some kind of logic between the model and the store" — **yes, a small amount of ordinary Go orchestration code is needed, but no new go-codex primitive.** This is intentional, not a gap: go-codex's `ports` package commits to typed BOUNDARIES (a retrieval call, an LLM call); it deliberately does not try to own the business logic that sits between them — the same philosophy already visible in `examples/order` and `examples/sensor-service`.

### The composition pattern: sequential `IOPort.Call`

A retrieval boundary is naturally an `IOPort[Query, []Chunk]` (bound to a future `adapters/vectorstore` adapter, or to `adapters/sql.QueryEachAdapter` for pgvector today). The generation boundary is the already-shipped `IOPort[RAGRequest, Answer]` bound via `ports.LLMPattern` + `adapters/openai.CallAdapter`. A RAG turn is exactly two sequential calls in a plain Go function, using [`IOPort.Call`](../features/ports.md) — the plain-Go request/response consumption style already shipped as a first-class citizen (not a fallback) alongside stream-based consumption:

```go
// domain/rag.go — ordinary Go orchestration, zero new ports/api primitive
type RAGRequest struct {
    Question string
    Context  []Chunk // retrieved chunks, folded into the LLM's user-turn content
}

func AnswerQuestion(ctx context.Context, question string) (Answer, error) {
    chunks, err := Retrieve.Call(ctx, Query{Text: question, TopK: 5})
    if err != nil {
        return Answer{}, err
    }
    return Generate.Call(ctx, RAGRequest{Question: question, Context: chunks})
}
```

- `Retrieve` and `Generate` are both declared, plugged into a Pattern, and bound to a concrete adapter exactly like every other `ports` boundary — see [Ports, Plugins, and Adapters](../concepts/ports-and-adapters.md). Only `AnswerQuestion` itself is new code, and it is plain Go: no codec, no adapter, no new interface.
- If the "augmentation" step needs more than folding `[]Chunk` into a field (e.g. truncating context to fit a token budget, re-ranking, deduplicating overlapping chunks), that logic goes inside `AnswerQuestion` too — still plain Go, still no new primitive.

### Why `Chain`/`ChainStream` do NOT apply here (and that's correct)

`ports.Chain`/`ports.ChainStream` wire a `chainSource`/`chainSink` pair — satisfied by `PipePort`/`SourcePort`/`SinkPort`, all of which expose an ambient `Stream(ctx)`/`Feed(ctx, stream)`. `IOPort` exposes `Connect`/`Call` instead (request/response semantics: one request in, one response out) and deliberately does NOT implement `chainSource`/`chainSink` — so `Chain`/`ChainStream` cannot wire two `IOPort`s together, and should not: a RAG turn is a request/response operation, not an unbounded stream, so `IOPort.Call` composition (above) is the right-shaped tool.

For a **high-throughput batch** RAG pipeline (embed+retrieve+generate for a large stream of queries, rather than one interactive turn at a time), `IOPort.Connect(ctx, src gstream.Stream[Req]) gstream.Stream[Resp]` already exists for exactly that — a small `gstream.Map` step between the retrieval port's output stream and the generation port's input stream plays the same "augmentation" role as `AnswerQuestion` above, still with no new primitive:

```go
retrieved := Retrieve.Connect(ctx, queries)
requests := gstream.Map(ctx, retrieved, func(chunks []Chunk) (RAGRequest, error) {
    return RAGRequest{Context: chunks /* ... */}, nil
}, gstream.MapOptions{})
answers := Generate.Connect(ctx, requests)
```

### Exposing a RAG turn to an external caller (agent-facing)

If the RAG turn itself should be callable by an external client — a REST endpoint, or a tool a *different* orchestrating LLM invokes — wrap the same orchestration in a `ports.ToolPort[Query, Answer]` via `SetFunc`:

```go
var RAGTool = codex.Must(ports.NewToolPort[Query, Answer]("answer_question", queryCodec, answerCodec, ports.PortOptions{}))
RAGTool.SetFunc(func(ctx context.Context, q Query) (Answer, error) {
    return AnswerQuestion(ctx, q.Text) // the SAME orchestration function above
})
```

`ToolPort` accepts `RESTPattern`/`ReqReplyPattern`/`MCPPattern`/`FilePattern` (not `LLMPattern` — that stays `IOPort`-only, since the LLM call is an INTERNAL outbound call the tool's implementation makes, not the tool's own external contract). Plugging in `MCPPattern` here mirrors `render/openaitools.FromLLMSpec`'s already-documented "agent-calls-agent" use case in [LLM Integration](../features/llm-integration.md) (see its `render/openaitools` section): a RAG-answering tool exposed this way lets a DIFFERENT orchestrating LLM invoke it as a tool-call, while its own implementation internally calls a vector store and an LLM via two `IOPort.Call`s.

### Is go-codex ready to build RAG agents today?

- **Orchestration layer — yes, ready today.** No new `ports`/`api` primitive is needed to glue retrieval and generation together; `IOPort.Call`/`Connect` composition in an ordinary Go function (above) is the complete, intended answer.
- **Retrieval + embeddings layer — partially ready.** Postgres+pgvector retrieval already works with zero new code via `adapters/sql.QueryEachAdapter` (see above). The two genuinely missing pieces are the `adapters/openai` embeddings endpoint and a dedicated non-SQL vector-store adapter — both listed below, both still "awaiting use case." A minimal RAG agent over pgvector could be built TODAY using only shipped packages (`adapters/sql` for retrieval + `adapters/openai`'s Chat Completions for generation), provided embeddings are computed some other way in the meantime (e.g. a raw `net/http` call to the embeddings endpoint, or an offline batch-embedding process) until item 1 below ships.

## What's genuinely missing

1. **`adapters/openai` embeddings support** — The LLM Integration feature explicitly deferred "non-chat-completions OpenAI endpoints (embeddings, moderation, image generation)" as out of scope. RAG needs the embeddings endpoint specifically (turning a chunk of text into a `[]float32` vector before it can be stored or queried). This is a small addition — same HTTP client/auth/error infrastructure as `adapters/openai.CallAdapter`, just a different endpoint path (`/embeddings`) and response shape (a vector, not a chat completion) — likely an `EmbedAdapter` implementing `ports.IOAdapter[Req, []float32]` (or `IOAdapter[Req, EmbeddingResult]` if token-usage metadata should travel with the vector). **Depends on the LLM Integration feature (already shipped)** (shares its HTTP client setup, error types, and observer conventions).
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
// adapters/openai — embeddings addition (small; adapters/openai already shipped)
func EmbedAdapter(client *http.Client, opts EmbedAdapterOptions) ports.IOAdapter[string, []float32]
```

## Open questions (for the real design pass)

- One generic `VectorStore` interface with pluggable distance metrics, or per-vendor adapter packages (`adapters/qdrant`, `adapters/pinecone`) like `adapters/mqtt`/`adapters/mqtt5` are separate packages for different protocol versions?
- Does `VectorRecord.Metadata` stay `map[string]any` (handle-less, `IOParam`-style, matching `adapters/sql`'s row-shape flexibility) or get a proper codec-validated typed payload (matching `ports.Cache[T]`'s typed value)?
- Should `EmbedAdapter` batch multiple texts per request (most embedding APIs accept an array input) — almost certainly yes, but changes the adapter's shape (`IOAdapter[[]string, [][]float32]` vs one-at-a-time) — decide once real usage patterns are known.
- Fixed vector dimensionality enforcement — `validate.MinItems`/`MaxItems` on the embedding codec (e.g. exactly 1536 for `text-embedding-3-small`) is already available with zero new code; just needs to be documented as the recommended pattern when this ships.
