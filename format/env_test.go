package format_test

import (
	"errors"
	"testing"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/validate"
)

// ── Test structs ──────────────────────────────────────────────────────────────

type flat struct {
	Host    string
	Port    int
	Debug   bool
	Timeout float64
}

var flatCodec = codex.Struct[flat](
	codex.Field[flat, string]{
		Name: "host", Codec: codex.String().Refine(validate.NonEmptyString),
		Get: func(c flat) string { return c.Host }, Set: func(c *flat, v string) { c.Host = v },
		Required: true,
	},
	codex.Field[flat, int]{
		Name: "port", Codec: codex.Int().Refine(validate.RangeInt(1, 65535)),
		Get: func(c flat) int { return c.Port }, Set: func(c *flat, v int) { c.Port = v },
		Required: true,
	},
	codex.Field[flat, bool]{
		Name: "debug", Codec: codex.Bool(),
		Get: func(c flat) bool { return c.Debug }, Set: func(c *flat, v bool) { c.Debug = v },
		Required: false,
	},
	codex.Field[flat, float64]{
		Name: "timeout", Codec: codex.Float64().Refine(validate.PositiveFloat),
		Get: func(c flat) float64 { return c.Timeout }, Set: func(c *flat, v float64) { c.Timeout = v },
		Required: true,
	},
)

type nested struct {
	Server serverCfg
	Port   int
}

type serverCfg struct {
	Host string
	TLS  bool
}

var nestedCodec = codex.Struct[nested](
	codex.Field[nested, serverCfg]{
		Name: "server",
		Codec: codex.Struct[serverCfg](
			codex.Field[serverCfg, string]{
				Name: "host", Codec: codex.String().Refine(validate.NonEmptyString),
				Get: func(c serverCfg) string { return c.Host }, Set: func(c *serverCfg, v string) { c.Host = v },
				Required: true,
			},
			codex.Field[serverCfg, bool]{
				Name: "tls", Codec: codex.Bool(),
				Get: func(c serverCfg) bool { return c.TLS }, Set: func(c *serverCfg, v bool) { c.TLS = v },
				Required: false,
			},
		),
		Get: func(c nested) serverCfg { return c.Server }, Set: func(c *nested, v serverCfg) { c.Server = v },
		Required: true,
	},
	codex.Field[nested, int]{
		Name: "port", Codec: codex.Int(),
		Get: func(c nested) int { return c.Port }, Set: func(c *nested, v int) { c.Port = v },
		Required: true,
	},
)

type withSlice struct {
	Tags  []string
	Ports []int
}

var sliceCodec = codex.Struct[withSlice](
	codex.Field[withSlice, []string]{
		Name: "tags", Codec: codex.SliceOf(codex.String()),
		Get: func(c withSlice) []string { return c.Tags }, Set: func(c *withSlice, v []string) { c.Tags = v },
		Required: false,
	},
	codex.Field[withSlice, []int]{
		Name: "ports", Codec: codex.SliceOf(codex.Int()),
		Get: func(c withSlice) []int { return c.Ports }, Set: func(c *withSlice, v []int) { c.Ports = v },
		Required: false,
	},
)

type withNullable struct {
	Note *string
}

var nullableCodec = codex.Struct[withNullable](
	codex.Field[withNullable, *string]{
		Name: "note", Codec: codex.Nullable(codex.String()),
		Get: func(c withNullable) *string { return c.Note }, Set: func(c *withNullable, v *string) { c.Note = v },
		Required: false,
	},
)

// ── Tests ─────────────────────────────────────────────────────────────────────

func TestFromEnv_FlatValid(t *testing.T) {
	t.Setenv("APP_HOST", "localhost")
	t.Setenv("APP_PORT", "8080")
	t.Setenv("APP_DEBUG", "true")
	t.Setenv("APP_TIMEOUT", "30.5")

	cfg, err := format.FromEnv(flatCodec, "APP_")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Host != "localhost" {
		t.Errorf("host: got %q, want %q", cfg.Host, "localhost")
	}
	if cfg.Port != 8080 {
		t.Errorf("port: got %d, want 8080", cfg.Port)
	}
	if !cfg.Debug {
		t.Error("debug: got false, want true")
	}
	if cfg.Timeout != 30.5 {
		t.Errorf("timeout: got %f, want 30.5", cfg.Timeout)
	}
}

func TestFromEnv_MissingRequired(t *testing.T) {
	// No env vars set; host, port, timeout are required.
	_, err := format.FromEnv(flatCodec, "APP_")
	if err == nil {
		t.Fatal("expected error for missing required fields, got nil")
	}
	var ve codex.ValidationErrors
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationErrors, got %T: %v", err, err)
	}
	if len(ve) == 0 {
		t.Error("expected at least one ValidationError")
	}
}

func TestFromEnv_ParseError_Int(t *testing.T) {
	t.Setenv("APP_HOST", "localhost")
	t.Setenv("APP_PORT", "notanumber")
	t.Setenv("APP_TIMEOUT", "30.5")

	_, err := format.FromEnv(flatCodec, "APP_")
	if err == nil {
		t.Fatal("expected parse error for bad int, got nil")
	}
	var ve codex.ValidationErrors
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationErrors, got %T: %v", err, err)
	}
	found := false
	for _, e := range ve {
		if e.Field == "port" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected error on field 'port', errors: %v", ve)
	}
}

func TestFromEnv_ParseError_Bool(t *testing.T) {
	t.Setenv("APP_HOST", "localhost")
	t.Setenv("APP_PORT", "8080")
	t.Setenv("APP_TIMEOUT", "30.0")
	t.Setenv("APP_DEBUG", "notabool")

	_, err := format.FromEnv(flatCodec, "APP_")
	if err == nil {
		t.Fatal("expected parse error for bad bool, got nil")
	}
	var ve codex.ValidationErrors
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationErrors, got %T: %v", err, err)
	}
	found := false
	for _, e := range ve {
		if e.Field == "debug" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected error on field 'debug', errors: %v", ve)
	}
}

func TestFromEnv_ConstraintViolation(t *testing.T) {
	t.Setenv("APP_HOST", "")      // violates NonEmptyString
	t.Setenv("APP_PORT", "99999") // violates RangeInt(1,65535)
	t.Setenv("APP_TIMEOUT", "30.0")

	_, err := format.FromEnv(flatCodec, "APP_")
	if err == nil {
		t.Fatal("expected constraint error, got nil")
	}
}

func TestFromEnv_NestedStruct(t *testing.T) {
	t.Setenv("APP_SERVER_HOST", "api.example.com")
	t.Setenv("APP_SERVER_TLS", "true")
	t.Setenv("APP_PORT", "443")

	cfg, err := format.FromEnv(nestedCodec, "APP_")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Server.Host != "api.example.com" {
		t.Errorf("server.host: got %q, want %q", cfg.Server.Host, "api.example.com")
	}
	if !cfg.Server.TLS {
		t.Error("server.tls: got false, want true")
	}
	if cfg.Port != 443 {
		t.Errorf("port: got %d, want 443", cfg.Port)
	}
}

func TestFromEnv_SliceOfStrings(t *testing.T) {
	t.Setenv("APP_TAGS", "web,api,v2")

	cfg, err := format.FromEnv(sliceCodec, "APP_")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Tags) != 3 {
		t.Fatalf("tags: got %d elements, want 3", len(cfg.Tags))
	}
	if cfg.Tags[0] != "web" || cfg.Tags[1] != "api" || cfg.Tags[2] != "v2" {
		t.Errorf("tags: got %v", cfg.Tags)
	}
}

func TestFromEnv_SliceOfInts(t *testing.T) {
	t.Setenv("APP_PORTS", "8080, 9090, 9091")

	cfg, err := format.FromEnv(sliceCodec, "APP_")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Ports) != 3 {
		t.Fatalf("ports: got %d elements, want 3", len(cfg.Ports))
	}
	if cfg.Ports[0] != 8080 || cfg.Ports[1] != 9090 || cfg.Ports[2] != 9091 {
		t.Errorf("ports: got %v", cfg.Ports)
	}
}

func TestFromEnv_SliceParseError(t *testing.T) {
	t.Setenv("APP_PORTS", "8080,notanumber")

	_, err := format.FromEnv(sliceCodec, "APP_")
	if err == nil {
		t.Fatal("expected parse error for bad slice element, got nil")
	}
}

func TestFromEnv_NullableAbsent(t *testing.T) {
	cfg, err := format.FromEnv(nullableCodec, "APP_")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Note != nil {
		t.Errorf("note: expected nil, got %v", cfg.Note)
	}
}

func TestFromEnv_NullablePresent(t *testing.T) {
	t.Setenv("APP_NOTE", "hello")

	cfg, err := format.FromEnv(nullableCodec, "APP_")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Note == nil {
		t.Fatal("note: expected non-nil")
	}
	if *cfg.Note != "hello" {
		t.Errorf("note: got %q, want %q", *cfg.Note, "hello")
	}
}

func TestFromEnv_EmptyPrefix(t *testing.T) {
	t.Setenv("HOST", "localhost")
	t.Setenv("PORT", "3000")
	t.Setenv("TIMEOUT", "5.0")

	cfg, err := format.FromEnv(flatCodec, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Host != "localhost" {
		t.Errorf("host: got %q, want localhost", cfg.Host)
	}
	if cfg.Port != 3000 {
		t.Errorf("port: got %d, want 3000", cfg.Port)
	}
}

// ── JSON-in-env tests ─────────────────────────────────────────────────────────

// withStringMap is a test struct that contains a StringMap field.
type withStringMap struct {
	Labels map[string]string
}

var stringMapCodec = codex.Struct[withStringMap](
	codex.Field[withStringMap, map[string]string]{
		Name:     "labels",
		Codec:    codex.StringMap(codex.String()),
		Get:      func(c withStringMap) map[string]string { return c.Labels },
		Set:      func(c *withStringMap, v map[string]string) { c.Labels = v },
		Required: false,
	},
)

func TestFromEnv_NestedStructAsJSON(t *testing.T) {
	t.Setenv("APP_SERVER", `{"host":"api.example.com","tls":true}`)
	t.Setenv("APP_PORT", "443")

	cfg, err := format.FromEnv(nestedCodec, "APP_")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Server.Host != "api.example.com" {
		t.Errorf("server.host: got %q, want api.example.com", cfg.Server.Host)
	}
	if !cfg.Server.TLS {
		t.Error("server.tls: got false, want true")
	}
	if cfg.Port != 443 {
		t.Errorf("port: got %d, want 443", cfg.Port)
	}
}

func TestFromEnv_JSONWinsOverPrefixExpansion(t *testing.T) {
	// Both JSON and prefix-style vars are set. JSON wins.
	t.Setenv("APP_SERVER", `{"host":"from-json","tls":false}`)
	t.Setenv("APP_SERVER_HOST", "from-prefix") // should be ignored
	t.Setenv("APP_PORT", "80")

	cfg, err := format.FromEnv(nestedCodec, "APP_")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Server.Host != "from-json" {
		t.Errorf("server.host: expected JSON to win, got %q", cfg.Server.Host)
	}
}

func TestFromEnv_SliceAsJSONArray(t *testing.T) {
	t.Setenv("APP_TAGS", `["web","api","v2"]`)
	t.Setenv("APP_PORTS", `[8080,9090]`)

	cfg, err := format.FromEnv(sliceCodec, "APP_")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Tags) != 3 || cfg.Tags[0] != "web" || cfg.Tags[2] != "v2" {
		t.Errorf("tags: got %v", cfg.Tags)
	}
	if len(cfg.Ports) != 2 || cfg.Ports[0] != 8080 || cfg.Ports[1] != 9090 {
		t.Errorf("ports: got %v", cfg.Ports)
	}
}

func TestFromEnv_StringMapAsJSON(t *testing.T) {
	t.Setenv("APP_LABELS", `{"env":"prod","team":"platform"}`)

	cfg, err := format.FromEnv(stringMapCodec, "APP_")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Labels["env"] != "prod" || cfg.Labels["team"] != "platform" {
		t.Errorf("labels: got %v", cfg.Labels)
	}
}

func TestFromEnv_MalformedJSONObject_ReturnsError(t *testing.T) {
	t.Setenv("APP_SERVER", `{"host":"unclosed`)
	t.Setenv("APP_PORT", "443")

	_, err := format.FromEnv(nestedCodec, "APP_")
	if err == nil {
		t.Fatal("expected parse error for malformed JSON, got nil")
	}
	var ve codex.ValidationErrors
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationErrors, got %T: %v", err, err)
	}
	found := false
	for _, e := range ve {
		if e.Field == "server" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected error on field 'server', got: %v", ve)
	}
}

func TestFromEnv_MalformedJSONArray_ReturnsError(t *testing.T) {
	t.Setenv("APP_TAGS", `["unclosed`)

	_, err := format.FromEnv(sliceCodec, "APP_")
	if err == nil {
		t.Fatal("expected parse error for malformed JSON array, got nil")
	}
}
