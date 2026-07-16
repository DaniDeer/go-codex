// Package redis provides a typed cache boundary for go-codex pipelines,
// backed by any server speaking the Redis protocol.
//
// # Typed cache boundary
//
// Values enter and leave the cache through the port's [codex.Codec] — every
// read decodes AND validates, every write validates AND encodes. The cache
// key is declared once as a template on the port ([ports.CachePattern]) and
// expanded per item; TTL and wire format (JSON/YAML/TOML) are part of the
// same declaration.
//
//	var UserCache = codex.Must(ports.NewIOPort[UserQuery, User]("user-cache",
//	    queryCodec, userCodec, ports.PortOptions{
//	        Patterns: []ports.Pattern{
//	            ports.CachePattern{Key: "user:{id}", TTL: 15 * time.Minute},
//	        },
//	    }))
//
// # Port mapping
//
//   - [GetAdapter] — [ports.IOAdapter]: read-through lookup (Req → key vars →
//     cached Resp; miss skips the item or errors per options).
//   - [SetAdapter] — [ports.IOAdapter]: write-through transform (writes each
//     item, passes it through unchanged).
//   - [DrainSetAdapter] — [ports.SinkAdapter]: terminal write-through.
//   - [Seed] — warm-restart read of a var-free key (e.g. re-feeding a
//     [ports.LatestPort] after a process restart).
//
// # Narrow client interface
//
// All constructors accept [Commands] — a three-method subset of the Redis
// command surface — never a concrete client. [NewCommands] adapts a go-redis
// [redis.UniversalClient]; unit tests use a hand-written fake. This is the
// only package file that imports go-redis.
//
// # Observer integration
//
// Every lookup and write fires [stats.CacheObserver] events (hit, miss,
// write) when the configured Observer implements it — always type-asserted,
// existing Observer implementations need not change. Decode validation
// failures additionally report per-field via stats.ReportErrors with
// location "payload". A nil Observer resolves from ctx
// ([stats.ObserverFromContext]).
//
// # Errors
//
// All failures are wrapped in [CacheError] (implements slog.LogValuer and
// Unwrap). A missing key surfaces as [ErrCacheMiss] — reachable through
// errors.Is on the wrapped chain.
package redis
