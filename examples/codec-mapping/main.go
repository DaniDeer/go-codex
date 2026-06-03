// Package main demonstrates three patterns for reusing and transforming codecs
// without repeating constraint definitions.
//
// Pattern 1 — Shared field codec variables:
//
//	Define portCodec and hostCodec once. Embed them in multiple struct codecs
//	(AppConfig, ProxyConfig). Constraints live in one place; all structs benefit.
//
// Pattern 2 — Sub-codec direct reuse:
//
//	dbCodec is defined independently with its own constraints. It is embedded
//	in AppConfig and also passed directly to newDBModule — no re-definition,
//	no constraint duplication.
//
// Pattern 3a — MapCodecSafe (infallible decode):
//
//	type Port uint16 wraps portCodec. The port range constraint defined on
//	portCodec runs automatically in the mapped codec's Decode path.
//	Encode is trusted (Refine constraints are decode-only by design).
//
// Pattern 3b — MapCodecValidated (fallible mapping + post-decode validation):
//
//	DBConfig is mapped to a DSN string. The to function can fail (malformed
//	host), and cb.Validate enforces DBConfig constraints after the mapping.
//
// Run with: go run ./examples/codec-mapping
package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
)

// ── Shared field codecs (Pattern 1) ───────────────────────────────────────────
//
// Define once, reuse across AppConfig, ProxyConfig, or any other struct codec.
// Adding or tightening a constraint here propagates to every struct that uses it.

var hostCodec = codex.String().
	Refine(validate.NonEmptyString).
	WithDescription("Hostname or IP address.")

var portCodec = codex.Uint().
	Refine(validate.RangeUint(1, 65535)).
	WithDescription("TCP port (1–65535).")

// ── DB types and codec (Pattern 2) ────────────────────────────────────────────
//
// dbCodec is an independent, self-contained codec. It can be embedded in a
// parent struct codec or passed directly to any module that only needs DB config.

type DBConfig struct {
	Host string
	Port uint
	Name string
}

var dbCodec = codex.Struct[DBConfig](
	codex.RequiredField("host", hostCodec, func(c DBConfig) string { return c.Host }, func(c *DBConfig, v string) { c.Host = v }),   // ← shared field codec
	codex.RequiredField("port", portCodec, func(c DBConfig) uint { return c.Port }, func(c *DBConfig, v uint) { c.Port = v }),        // ← shared field codec
	codex.RequiredField("name", codex.String().Refine(validate.NonEmptyString).WithDescription("Database name."), func(c DBConfig) string { return c.Name }, func(c *DBConfig, v string) { c.Name = v }),
)

// ── AppConfig embeds dbCodec (Pattern 2) ──────────────────────────────────────

type AppConfig struct {
	Host string
	Port uint
	DB   DBConfig
}

var appConfigCodec = codex.Struct[AppConfig](
	codex.RequiredField("host", hostCodec, func(c AppConfig) string { return c.Host }, func(c *AppConfig, v string) { c.Host = v }),                            // ← same shared field codec
	codex.RequiredField("port", portCodec, func(c AppConfig) uint { return c.Port }, func(c *AppConfig, v uint) { c.Port = v }),                               // ← same shared field codec
	codex.RequiredField("db", dbCodec.WithDescription("Database connection."), func(c AppConfig) DBConfig { return c.DB }, func(c *AppConfig, v DBConfig) { c.DB = v }), // ← sub-codec reused
)

// ── ProxyConfig also reuses shared field codecs (Pattern 1) ───────────────────

type ProxyConfig struct {
	UpstreamHost string
	UpstreamPort uint
}

var proxyConfigCodec = codex.Struct[ProxyConfig](
	codex.RequiredField("upstream_host", hostCodec, func(c ProxyConfig) string { return c.UpstreamHost }, func(c *ProxyConfig, v string) { c.UpstreamHost = v }), // ← same hostCodec — constraint defined once
	codex.RequiredField("upstream_port", portCodec, func(c ProxyConfig) uint { return c.UpstreamPort }, func(c *ProxyConfig, v uint) { c.UpstreamPort = v }), // ← same portCodec — constraint defined once
)

// ── MapCodecSafe: Port newtype (Pattern 3a) ───────────────────────────────────
//
// Port is a named type over uint16. portCodec's RangeUint(1,65535) constraint
// runs automatically in portMappedCodec.Decode — no re-definition needed.

type Port uint16

var portMappedCodec = codex.MapCodecSafe(
	portCodec,
	func(n uint) Port { return Port(n) },
	func(p Port) (uint, error) { return uint(p), nil },
)

// ── MapCodecValidated: DBConfig → DSN (Pattern 3b) ───────────────────────────
//
// Maps a validated DBConfig to a DSN string. The to function can fail (it
// rejects hosts containing whitespace). After mapping, cb.Validate enforces
// dbCodec's constraints on the source value.
//
// dsnCodec carries dbCodec's schema — all field constraints are reflected.

var dsnCodec = codex.MapCodecValidated(
	dbCodec,
	codex.String(),
	func(cfg DBConfig) (string, error) {
		if strings.ContainsAny(cfg.Host, " \t") {
			return "", errors.New("host must not contain whitespace")
		}
		return fmt.Sprintf("postgres://%s:%d/%s", cfg.Host, cfg.Port, cfg.Name), nil
	},
	func(dsn string) (DBConfig, error) {
		// Minimal round-trip for the example — not production-grade parsing.
		dsn = strings.TrimPrefix(dsn, "postgres://")
		hostPort, name, ok := strings.Cut(dsn, "/")
		if !ok {
			return DBConfig{}, errors.New("invalid DSN: missing database name")
		}
		host, portStr, ok := strings.Cut(hostPort, ":")
		if !ok {
			return DBConfig{}, errors.New("invalid DSN: missing port")
		}
		port := uint(0)
		if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil {
			return DBConfig{}, fmt.Errorf("invalid DSN port: %w", err)
		}
		return DBConfig{Host: host, Port: port, Name: name}, nil
	},
)

// ── TimeWindow: cross-field constraint via RefineFunc (Pattern 4) ─────────────
//
// RefineFunc wraps a func(T) error as a post-decode constraint. Use it on struct
// codecs to validate relationships between fields without defining a named
// Constraint[T]. On failure it returns ConstraintError{Name:"refine"}.

type TimeWindow struct {
	Start int // Unix timestamp (seconds)
	End   int
}

var timeWindowCodec = codex.Struct[TimeWindow](
	codex.RequiredField("start", codex.Int().WithDescription("Start timestamp (Unix seconds)."), func(w TimeWindow) int { return w.Start }, func(w *TimeWindow, v int) { w.Start = v }),
	codex.RequiredField("end", codex.Int().WithDescription("End timestamp (Unix seconds)."), func(w TimeWindow) int { return w.End }, func(w *TimeWindow, v int) { w.End = v }),
).RefineFunc(func(w TimeWindow) error {
	if w.End <= w.Start {
		return errors.New("end must be after start")
	}
	return nil
})

// ── DB module (Pattern 2) ─────────────────────────────────────────────────────
//
// newDBModule accepts any Codec[DBConfig] — it can receive dbCodec directly from
// AppConfig without knowing about AppConfig. Constraints are not re-declared.

type DBModule struct {
	codec  codex.Codec[DBConfig]
	config DBConfig
}

func newDBModule(c codex.Codec[DBConfig], raw map[string]any) (*DBModule, error) {
	cfg, err := c.Decode(raw)
	if err != nil {
		return nil, err
	}
	return &DBModule{codec: c, config: cfg}, nil
}

func main() {
	// ── 1. Shared field codecs ────────────────────────────────────────────────
	fmt.Println("=== 1. Shared field codecs — constraint defined once ===")

	// portCodec's port range constraint enforced in AppConfig.
	_, err := appConfigCodec.Decode(map[string]any{
		"host": "0.0.0.0",
		"port": 99999, // violates portCodec constraint — defined once, enforced everywhere
		"db":   map[string]any{"host": "db.internal", "port": 5432, "name": "myapp"},
	})
	fmt.Println("AppConfig port=99999:", err)

	// Same portCodec constraint enforced in ProxyConfig — no extra definition.
	_, err = proxyConfigCodec.Decode(map[string]any{
		"upstream_host": "proxy.internal",
		"upstream_port": 0, // violates portCodec RangeUint
	})
	fmt.Println("ProxyConfig port=0:  ", err)

	// ── 2. Sub-codec direct reuse ─────────────────────────────────────────────
	fmt.Println("\n=== 2. Sub-codec direct reuse — dbCodec passed to DBModule ===")

	// dbCodec is used inside appConfigCodec and also passed directly to DBModule.
	// The module gets the same constraints without re-defining them.
	mod, err := newDBModule(dbCodec, map[string]any{
		"host": "db.internal",
		"port": 5432,
		"name": "myapp",
	})
	if err != nil {
		fmt.Println("module error:", err)
	} else {
		fmt.Printf("DBModule config: host=%s port=%d name=%s\n",
			mod.config.Host, mod.config.Port, mod.config.Name)
	}

	// dbCodec constraints fire when the module receives invalid data.
	_, err = newDBModule(dbCodec, map[string]any{
		"host": "db.internal",
		"port": 99999, // violates portCodec constraint — same constraint object
		"name": "myapp",
	})
	fmt.Println("DBModule port=99999:", err)

	// ── 3a. MapCodecSafe: Port newtype ────────────────────────────────────────
	fmt.Println("\n=== 3a. MapCodecSafe — portCodec constraints run in mapped codec ===")

	p, err := portMappedCodec.Decode(8080)
	if err != nil {
		fmt.Println("decode error:", err)
	} else {
		fmt.Printf("decoded Port: %d (type %T)\n", p, p)
	}

	// portCodec's constraint fires — no re-definition in portMappedCodec.
	_, err = portMappedCodec.Decode(99999)
	fmt.Println("Port 99999:", err)

	// Encode direction calls from, which always succeeds for Port.
	// Refine constraints are decode-only by design — encode is trusted.
	encoded, err := portMappedCodec.Encode(Port(0))
	fmt.Printf("Encode Port(0): value=%v err=%v (encode is trusted; no constraint check)\n", encoded, err)

	// ── 3b. MapCodecValidated: DBConfig → DSN ────────────────────────────────
	fmt.Println("\n=== 3b. MapCodecValidated — DBConfig → DSN, fallible mapping ===")

	dsn, err := dsnCodec.Decode(map[string]any{
		"host": "db.internal",
		"port": 5432,
		"name": "myapp",
	})
	if err != nil {
		fmt.Println("decode error:", err)
	} else {
		fmt.Println("DSN:", dsn)
	}

	// to function rejects whitespace in host.
	_, err = dsnCodec.Decode(map[string]any{
		"host": "db internal", // space in host — to function rejects this
		"port": 5432,
		"name": "myapp",
	})
	fmt.Println("whitespace host:", err)

	// dbCodec constraints fire before to is called.
	_, err = dsnCodec.Decode(map[string]any{
		"host": "db.internal",
		"port": 99999, // violates portCodec constraint — caught before to runs
		"name": "myapp",
	})
	fmt.Println("port out of range:", err)

	// ── 4. RefineFunc: cross-field constraint ─────────────────────────────────
	fmt.Println("\n=== 4. RefineFunc — cross-field constraint (end must be after start) ===")

	window, err := timeWindowCodec.Decode(map[string]any{"start": 1000, "end": 2000})
	if err != nil {
		fmt.Println("decode error:", err)
	} else {
		fmt.Printf("time window: start=%d end=%d\n", window.Start, window.End)
	}

	// end <= start: RefineFunc catches the cross-field violation.
	_, err = timeWindowCodec.Decode(map[string]any{"start": 2000, "end": 1000})
	fmt.Println("end before start:", err)

	// end == start: also invalid.
	_, err = timeWindowCodec.Decode(map[string]any{"start": 1500, "end": 1500})
	fmt.Println("end equals start:", err)
}
