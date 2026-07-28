# OpenAI Multimodal Content (images / audio / video) — `adapters/openai`

> **Status:** Awaiting use case — placeholder / not yet designed in detail.
> [← Back to Roadmap](index.md)
>
> See also: [LLM Integration](../features/llm-integration.md) · [Vector Store Adapter (RAG retrieval)](vector-store-adapter.md)

---

## Motivation

[LLM Integration](../features/llm-integration.md) (`api/llm` + `adapters/openai`) is text/JSON-only end-to-end today: `api/llm`'s `build()` hardcodes `format.JSON` for both request encoding and response decoding, and `adapters/openai`'s wire shape (`chatMessage{Role, Content string}`) always sends a plain string. There is no way to attach an image, audio clip, or video frame to a `Call` — even though OpenAI's own Chat Completions API (and the equivalent on Azure OpenAI / other OpenAI-compatible providers) already supports a multimodal `content` array (`[{"type":"text",...}, {"type":"image_url",...}, {"type":"input_audio",...}]`) for vision/audio-capable models (`gpt-4o`, `gpt-4o-mini`, etc.).

This page is the placeholder for that gap, captured now so the need and its rough shape aren't lost — to be fully specified once a concrete driving use case appears (same bar as [`stream-flatmap.md`](stream-flatmap.md)'s "awaiting use case" status, and [`vector-store-adapter.md`](vector-store-adapter.md)'s placeholder precedent).

---

## What's genuinely missing

1. **A way to declare binary/media input alongside the typed request codec.** Today `Req` is always JSON-encoded wholesale into a single string content part. Real multimodal use cases (e.g. "classify this uploaded photo", "transcribe and summarize this audio clip") need the request to carry raw bytes (a `[]byte` field, or a codec like `codex.Base64Bytes()`) that get sent as a *separate* content part (`image_url`/`input_audio`) rather than JSON-embedded as a base64 string inside the text part — the latter technically "works" today (bytes can already round-trip through a byte-slice codec) but defeats the purpose: the model would just see a giant base64 string in the text, not a first-class image/audio input it can actually "look at" or "listen to".
2. **`chatMessage.Content` needs to become a union, not a plain string.** OpenAI's wire format allows `content` to be either a plain string OR an array of typed parts (`{"type":"text","text":...}`, `{"type":"image_url","image_url":{"url":...}}`, `{"type":"input_audio","input_audio":{"data":...,"format":...}}`). `adapters/openai/client.go`'s `chatMessage` struct and `complete[Req,Resp]`'s message-building logic need to support both shapes — likely keeping the current plain-string path as the default (backward compatible) and adding an opt-in multimodal path.
3. **A new `llm.CallOpt`** to attach media parts — rough sketch: `llm.WithImage(field extractor func(Req) ([]byte, string /* mime type */))` or similar, mirroring how `llm.UserMessage` already lets a caller override text-content rendering. Needs to decide whether media come from a dedicated `Req` field (typed, codec-validated) or a raw side-channel (less consistent with go-codex's "one struct, one call" philosophy — should be avoided if possible).
4. **Response side**: some models can also *generate* images (e.g. via a separate image-generation endpoint, not Chat Completions) — explicitly out of scope for this page; Chat Completions multimodal is input-only (the model describes/analyzes media, it does not return media through this endpoint).
5. **Size/encoding concerns** — base64-encoding a large image/audio/video payload inline vs. referencing a URL (`image_url.url` accepts either a data URI or a hosted URL) has real payload-size and latency implications; needs a documented recommendation once designed (e.g. prefer hosted URLs for large files, inline base64 only for small images).

## Rough shape (to refine when this gets designed properly)

```go
// adapters/openai/client.go — sketch, NOT final
type contentPart struct {
    Type     string        `json:"type"` // "text" | "image_url" | "input_audio"
    Text     string        `json:"text,omitempty"`
    ImageURL *imageURLPart `json:"image_url,omitempty"`
    // ... input_audio, etc.
}

type chatMessage struct {
    Role    string `json:"role"`
    Content any    `json:"content"` // string (today) OR []contentPart (multimodal)
}
```

```go
// api/llm — sketch, NOT final
func WithImage[Req any](extract func(Req) (data []byte, mimeType string)) CallOpt
```

## Open questions (for the real design pass)

- Does this belong in `api/llm` (protocol-agnostic, since the *concept* of "attach an image" isn't OpenAI-specific) or purely in `adapters/openai` (since the wire shape — `image_url` vs. some other provider's equivalent — is genuinely provider-specific)? Precedent: `api/llm` stays protocol-agnostic today (`CallHandle` has no HTTP/OpenAI knowledge at all) — likely the abstraction (`WithImage`/media parts) should live in `api/llm` with `adapters/openai` translating to its specific wire shape, but this needs confirming once a second provider's multimodal shape is examined (Anthropic/Gemini both have their own different multimodal content shapes).
- Should media input be a dedicated typed `Req` field (codec-validated, e.g. `codex.Bytes()` with a `MaxLength` constraint) or an option-level extractor function (as sketched above)? The former is more consistent with "one struct, one call"; the latter avoids polluting `Req` with transport-shape concerns (mime type, part ordering) that arguably don't belong in the domain type.
- Multiple images/media parts per call — one field, or a slice? Needs a concrete use case to decide the ergonomics.
- Should this also cover request-side YAML/TOML format selection (`llm.CallOpt`/`ports.LLMPattern` currently have no `Format`/`CustomFormat` option at all, unlike `FilePattern`/`CachePattern`/`SocketPattern`) — a smaller, unrelated gap noted in [LLM Integration's current limitations](../features/llm-integration.md#current-limitations--format-and-content-type) — or should that ship independently, sooner, since it doesn't require the wire-shape redesign this page is about? Likely independent and smaller — revisit separately if a concrete non-JSON use case appears.
