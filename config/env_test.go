package config_test

import (
	"errors"
	"fmt"
	"log/slog"
	"testing"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/config"
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
	codex.RequiredField("host",
		codex.String().Refine(validate.NonEmptyString),
		func(c flat) string { return c.Host },
		func(c *flat, v string) { c.Host = v },
	),
	codex.RequiredField("port",
		codex.Int().Refine(validate.RangeInt(1, 65535)),
		func(c flat) int { return c.Port },
		func(c *flat, v int) { c.Port = v },
	),
	codex.OptionalField("debug",
		codex.Bool(),
		func(c flat) bool { return c.Debug },
		func(c *flat, v bool) { c.Debug = v },
	),
	codex.RequiredField("timeout",
		codex.Float64().Refine(validate.PositiveFloat),
		func(c flat) float64 { return c.Timeout },
		func(c *flat, v float64) { c.Timeout = v },
	),
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
	codex.RequiredField("server",
		codex.Struct[serverCfg](
			codex.RequiredField("host",
				codex.String().Refine(validate.NonEmptyString),
				func(c serverCfg) string { return c.Host },
				func(c *serverCfg, v string) { c.Host = v },
			),
			codex.OptionalField("tls",
				codex.Bool(),
				func(c serverCfg) bool { return c.TLS },
				func(c *serverCfg, v bool) { c.TLS = v },
			),
		),
		func(c nested) serverCfg { return c.Server },
		func(c *nested, v serverCfg) { c.Server = v },
	),
	codex.RequiredField("port",
		codex.Int(),
		func(c nested) int { return c.Port },
		func(c *nested, v int) { c.Port = v },
	),
)

type withSlice struct {
	Tags  []string
	Ports []int
}

var sliceCodec = codex.Struct[withSlice](
	codex.OptionalField("tags",
		codex.SliceOf(codex.String()),
		func(c withSlice) []string { return c.Tags },
		func(c *withSlice, v []string) { c.Tags = v },
	),
	codex.OptionalField("ports",
		codex.SliceOf(codex.Int()),
		func(c withSlice) []int { return c.Ports },
		func(c *withSlice, v []int) { c.Ports = v },
	),
)

type withNullable struct {
	Note *string
}

var nullableCodec = codex.Struct[withNullable](
	codex.OptionalField("note",
		codex.Nullable(codex.String()),
		func(c withNullable) *string { return c.Note },
		func(c *withNullable, v *string) { c.Note = v },
	),
)

// ── Tests ─────────────────────────────────────────────────────────────────────

func TestFromEnv_FlatValid(t *testing.T) {
	t.Setenv("APP_HOST", "localhost")
	t.Setenv("APP_PORT", "8080")
	t.Setenv("APP_DEBUG", "true")
	t.Setenv("APP_TIMEOUT", "30.5")

	cfg, err := config.FromEnv(flatCodec, "APP_")
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
	_, err := config.FromEnv(flatCodec, "APP_")
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

	_, err := config.FromEnv(flatCodec, "APP_")
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

	_, err := config.FromEnv(flatCodec, "APP_")
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

	_, err := config.FromEnv(flatCodec, "APP_")
	if err == nil {
		t.Fatal("expected constraint error, got nil")
	}
}

func TestFromEnv_NestedStruct(t *testing.T) {
	t.Setenv("APP_SERVER_HOST", "api.example.com")
	t.Setenv("APP_SERVER_TLS", "true")
	t.Setenv("APP_PORT", "443")

	cfg, err := config.FromEnv(nestedCodec, "APP_")
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

	cfg, err := config.FromEnv(sliceCodec, "APP_")
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

	cfg, err := config.FromEnv(sliceCodec, "APP_")
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

	_, err := config.FromEnv(sliceCodec, "APP_")
	if err == nil {
		t.Fatal("expected parse error for bad slice element, got nil")
	}
}

func TestFromEnv_NullableAbsent(t *testing.T) {
	cfg, err := config.FromEnv(nullableCodec, "APP_")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Note != nil {
		t.Errorf("note: expected nil, got %v", cfg.Note)
	}
}

func TestFromEnv_NullablePresent(t *testing.T) {
	t.Setenv("APP_NOTE", "hello")

	cfg, err := config.FromEnv(nullableCodec, "APP_")
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

	cfg, err := config.FromEnv(flatCodec, "")
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

	cfg, err := config.FromEnv(nestedCodec, "APP_")
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

	cfg, err := config.FromEnv(nestedCodec, "APP_")
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

	cfg, err := config.FromEnv(sliceCodec, "APP_")
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

	cfg, err := config.FromEnv(stringMapCodec, "APP_")
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

	_, err := config.FromEnv(nestedCodec, "APP_")
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

	_, err := config.FromEnv(sliceCodec, "APP_")
	if err == nil {
		t.Fatal("expected parse error for malformed JSON array, got nil")
	}
}

// ── config.FromEnvVar ────────────────────────────────────────────────────────────────

func TestFromEnvVar_HappyPath_Int(t *testing.T) {
	t.Setenv("TEST_PORT", "8080")

	port, err := config.FromEnvVar("TEST_PORT",
		codex.Int().Refine(validate.RangeInt(1, 65535)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if port != 8080 {
		t.Errorf("want 8080, got %d", port)
	}
}

func TestFromEnvVar_HappyPath_String(t *testing.T) {
	t.Setenv("TEST_LEVEL", "debug")

	level, err := config.FromEnvVar("TEST_LEVEL",
		codex.String().Refine(validate.OneOf("debug", "info", "warn", "error")))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if level != "debug" {
		t.Errorf("want debug, got %q", level)
	}
}

func TestFromEnvVar_NotSet_ReturnsZero(t *testing.T) {
	// ensure variable is absent
	t.Setenv("TEST_ABSENT_VAR_XYZ", "")
	// Use a key that is definitely not set — t.Setenv always sets, so use a
	// unique key and then unset it via t.Cleanup (simpler: just don't set it).
	port, err := config.FromEnvVar("TEST_TRULY_ABSENT_VAR_12345",
		codex.Int().Refine(validate.RangeInt(1, 65535)))
	if err != nil {
		t.Fatalf("expected nil error for unset var, got: %v", err)
	}
	if port != 0 {
		t.Errorf("expected zero value, got %d", port)
	}
}

func TestFromEnvVar_InvalidValue_ReturnsEnvVarError(t *testing.T) {
	t.Setenv("TEST_PORT_BAD", "99999") // out of range

	_, err := config.FromEnvVar("TEST_PORT_BAD",
		codex.Int().Refine(validate.RangeInt(1, 65535)))
	if err == nil {
		t.Fatal("expected error for out-of-range port, got nil")
	}
	var envErr config.EnvVarError
	if !errors.As(err, &envErr) {
		t.Fatalf("expected config.EnvVarError, got %T: %v", err, err)
	}
	if envErr.Key != "TEST_PORT_BAD" {
		t.Errorf("expected Key=TEST_PORT_BAD, got %q", envErr.Key)
	}
	if envErr.Err == nil {
		t.Error("expected non-nil Err inside config.EnvVarError")
	}
}

func TestFromEnvVar_InvalidType_ReturnsEnvVarError(t *testing.T) {
	t.Setenv("TEST_PORT_STR", "not-a-number")

	_, err := config.FromEnvVar("TEST_PORT_STR", codex.Int())
	if err == nil {
		t.Fatal("expected error for non-integer value, got nil")
	}
	var envErr config.EnvVarError
	if !errors.As(err, &envErr) {
		t.Fatalf("expected config.EnvVarError, got %T: %v", err, err)
	}
}

func TestFromEnvVar_Unwrap_ExposesInnerError(t *testing.T) {
	t.Setenv("TEST_PORT_INNER", "bad")

	_, err := config.FromEnvVar("TEST_PORT_INNER", codex.Int())

	var envErr config.EnvVarError
	if !errors.As(err, &envErr) {
		t.Fatalf("expected config.EnvVarError, got %T", err)
	}
	if envErr.Unwrap() == nil {
		t.Error("Unwrap() must return non-nil inner error")
	}
}

func TestEnvVarError_LogValue(t *testing.T) {
	envErr := config.EnvVarError{
		Key: "APP_PORT",
		Err: fmt.Errorf("bad value"),
	}
	lv := envErr.LogValue()
	if lv.Kind() != slog.KindGroup {
		t.Fatalf("LogValue: want KindGroup, got %v", lv.Kind())
	}
	attrs := lv.Group()
	keys := make(map[string]bool, len(attrs))
	for _, a := range attrs {
		keys[a.Key] = true
	}
	for _, want := range []string{"key", "err"} {
		if !keys[want] {
			t.Errorf("LogValue missing attribute %q", want)
		}
	}
}
