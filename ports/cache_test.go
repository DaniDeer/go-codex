package ports_test

import (
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/ports"
	"github.com/DaniDeer/go-codex/validate"
)

// CF3: CachePattern + CustomFormat(Gob) round-trips through the cache handle.
func TestCachePattern_CustomFormat_Gob(t *testing.T) {
	p, err := ports.NewIOPort[int, cfgItem]("cache-gob", intCodec, cfgCodec, ports.PortOptions{
		Patterns: []ports.Pattern{
			ports.CachePattern{Key: "item:{id}", CustomFormat: format.Gob(cfgCodec)},
		},
	})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	c, ok := ports.CacheHandle[cfgItem](p)
	if !ok {
		t.Fatal("want CacheHandle to be present")
	}
	data, err := c.Format.Marshal(cfgItem{V: 9})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := c.Format.Unmarshal(data)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.V != 9 {
		t.Errorf("want V=9, got %d", got.V)
	}
}

// ── CachePattern key codecs (CacheKeyParam) ────────────────────────────────────

// CK1: CacheKeyParam.WithCodec returns an updated value; value semantics.
func TestCacheKeyParam_WithCodec(t *testing.T) {
	base := ports.CacheKeyParam{Name: "id"}
	withCodec := base.WithCodec(codex.String())
	if base.Codec != nil {
		t.Error("want original CacheKeyParam unmodified")
	}
	if withCodec.Codec == nil {
		t.Error("want returned CacheKeyParam to have Codec set")
	}
	if withCodec.Name != "id" {
		t.Errorf("want Name preserved, got %q", withCodec.Name)
	}
}

// CK2: no CacheKeyParam declared — BuildKey is byte-identical to pre-feature
// behavior (regression guard).
func TestCache_BuildKey_NoParams_Unchanged(t *testing.T) {
	c := ports.NewCache("user:{id}", format.JSON(cfgCodec))
	got, err := c.BuildKey(map[string]string{"id": "anything-goes"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "user:anything-goes" {
		t.Errorf("want %q, got %q", "user:anything-goes", got)
	}
}

// CK3: a declared codec accepts a valid value and substitutes it.
func TestCache_BuildKey_ValidatesDeclaredCodec_HappyPath(t *testing.T) {
	c := ports.NewCache("user:{id}", format.JSON(cfgCodec),
		ports.CacheKeyParam{Name: "id"}.WithCodec(codex.String().Refine(validate.UUID)))
	got, err := c.BuildKey(map[string]string{"id": "f47ac10b-58cc-4372-a567-0e02b2c3d479"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "user:f47ac10b-58cc-4372-a567-0e02b2c3d479"
	if got != want {
		t.Errorf("want %q, got %q", want, got)
	}
}

// CK4: a declared codec rejects an invalid value with CacheKeyParamError.
func TestCache_BuildKey_CodecRejectsValue(t *testing.T) {
	c := ports.NewCache("user:{id}", format.JSON(cfgCodec),
		ports.CacheKeyParam{Name: "id"}.WithCodec(codex.String().Refine(validate.UUID)))
	_, err := c.BuildKey(map[string]string{"id": "not-a-uuid"})
	if err == nil {
		t.Fatal("want error for invalid UUID")
	}
	var paramErr ports.CacheKeyParamError
	if !errors.As(err, &paramErr) {
		t.Fatalf("want CacheKeyParamError, got %T: %v", err, err)
	}
	if paramErr.Key != "user:{id}" || paramErr.Var != "id" || paramErr.Value != "not-a-uuid" {
		t.Errorf("unexpected fields: %+v", paramErr)
	}
	if paramErr.Err == nil {
		t.Error("want wrapped cause")
	}
	v := paramErr.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("want slog.KindGroup, got %v", v.Kind())
	}
	attrs := v.Group()
	keys := make(map[string]bool, len(attrs))
	for _, a := range attrs {
		keys[a.Key] = true
	}
	for _, want := range []string{"key", "var", "value", "cause"} {
		if !keys[want] {
			t.Errorf("want LogValue key %q present, got %v", want, keys)
		}
	}
}

// CK5: a declared CacheKeyParam for a var absent from vars still returns
// CacheKeyError (not CacheKeyParamError) — can't codec-validate an absent value.
func TestCache_BuildKey_MissingVar_StillCacheKeyError(t *testing.T) {
	c := ports.NewCache("user:{id}", format.JSON(cfgCodec),
		ports.CacheKeyParam{Name: "id"}.WithCodec(codex.String().Refine(validate.UUID)))
	_, err := c.BuildKey(map[string]string{})
	var keyErr ports.CacheKeyError
	if !errors.As(err, &keyErr) {
		t.Fatalf("want CacheKeyError, got %T: %v", err, err)
	}
	var paramErr ports.CacheKeyParamError
	if errors.As(err, &paramErr) {
		t.Error("want CacheKeyError, not CacheKeyParamError, for a missing var")
	}
}

// CK6: ValidateKeyVars mirrors BuildKey's validation without building the key.
func TestCache_ValidateKeyVars_HappyAndError(t *testing.T) {
	c := ports.NewCache("user:{id}", format.JSON(cfgCodec),
		ports.CacheKeyParam{Name: "id"}.WithCodec(codex.String().Refine(validate.UUID)))

	if err := c.ValidateKeyVars(map[string]string{"id": "f47ac10b-58cc-4372-a567-0e02b2c3d479"}); err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	err := c.ValidateKeyVars(map[string]string{"id": "not-a-uuid"})
	var paramErr ports.CacheKeyParamError
	if !errors.As(err, &paramErr) {
		t.Fatalf("want CacheKeyParamError, got %T: %v", err, err)
	}

	err = c.ValidateKeyVars(map[string]string{})
	var keyErr ports.CacheKeyError
	if !errors.As(err, &keyErr) {
		t.Fatalf("want CacheKeyError for missing var, got %T: %v", err, err)
	}
}

// CK7: KeySchemas omits params without a codec; empty map when none declared.
func TestCache_KeySchemas_OmitsParamsWithoutCodec(t *testing.T) {
	c := ports.NewCache("thing:{id}:{tag}", format.JSON(cfgCodec),
		ports.CacheKeyParam{Name: "id"}.WithCodec(codex.String().Refine(validate.UUID)),
		ports.CacheKeyParam{Name: "tag"}, // no codec
	)
	schemas := c.KeySchemas()
	if _, ok := schemas["id"]; !ok {
		t.Error("want schema for \"id\" (has codec)")
	}
	if _, ok := schemas["tag"]; ok {
		t.Error("want no schema for \"tag\" (no codec)")
	}

	noParams := ports.NewCache("static", format.JSON(cfgCodec))
	if len(noParams.KeySchemas()) != 0 {
		t.Error("want empty map when no params declared")
	}
}

// CK8: CachePattern.Opts wired through an IOPort — end-to-end validation.
func TestCachePattern_Opts_WiredThroughIOPort(t *testing.T) {
	p, err := ports.NewIOPort[int, cfgItem]("cache-keyparam-io", intCodec, cfgCodec, ports.PortOptions{
		Patterns: []ports.Pattern{
			ports.CachePattern{
				Key: "item:{id}",
				Opts: []ports.CacheOpt{
					ports.CacheKeyParam{Name: "id"}.WithCodec(codex.String().Refine(validate.UUID)),
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	c, ok := ports.CacheHandle[cfgItem](p)
	if !ok {
		t.Fatal("want CacheHandle to be present")
	}
	if _, err := c.BuildKey(map[string]string{"id": "not-a-uuid"}); err == nil {
		t.Error("want invalid UUID rejected end-to-end")
	}
	if _, err := c.BuildKey(map[string]string{"id": "f47ac10b-58cc-4372-a567-0e02b2c3d479"}); err != nil {
		t.Errorf("want valid UUID accepted end-to-end, got %v", err)
	}
}

// CK9: CachePattern.Opts wired through a SinkPort.
func TestCachePattern_Opts_WiredThroughSinkPort(t *testing.T) {
	p, err := ports.NewSinkPort[cfgItem]("cache-keyparam-sink", cfgCodec, ports.PortOptions{
		Patterns: []ports.Pattern{
			ports.CachePattern{
				Key: "item:{id}",
				Opts: []ports.CacheOpt{
					ports.CacheKeyParam{Name: "id"}.WithCodec(codex.String().Refine(validate.UUID)),
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	c, ok := ports.CacheHandle[cfgItem](p)
	if !ok {
		t.Fatal("want CacheHandle to be present")
	}
	if _, err := c.BuildKey(map[string]string{"id": "not-a-uuid"}); err == nil {
		t.Error("want invalid UUID rejected end-to-end")
	}
}

// CK10: CachePattern.Opts wired through a LatestPort — a var-free key with
// Opts declared is a no-op, not an error.
func TestCachePattern_Opts_WiredThroughLatestPort(t *testing.T) {
	p, err := ports.NewLatestPort[cfgItem]("cache-keyparam-latest", cfgCodec, ports.PortOptions{
		Patterns: []ports.Pattern{
			ports.CachePattern{
				Key: "current", // var-free
				Opts: []ports.CacheOpt{
					ports.CacheKeyParam{Name: "id"}.WithCodec(codex.String().Refine(validate.UUID)),
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	c, ok := ports.CacheHandle[cfgItem](p)
	if !ok {
		t.Fatal("want CacheHandle to be present")
	}
	got, err := c.BuildKey(nil)
	if err != nil {
		t.Fatalf("want var-free key with unused CacheKeyParam to be a no-op, got %v", err)
	}
	if got != "current" {
		t.Errorf("want %q, got %q", "current", got)
	}
}

// mergedCacheItem is used to verify NewCacheKeyParam (merge-capable, unlike
// plain CacheKeyParam above) is correctly wired through CachePattern.Opts —
// regression guard for a bug where the Pattern-build path only copied
// cb.params, silently dropping cb.mergeFields for every Pattern-built Cache.
type mergedCacheItem struct {
	ID    string
	Value int
}

var mergedCacheItemCodec = codex.Struct[mergedCacheItem](
	codex.RequiredField("value", codex.Int(),
		func(c mergedCacheItem) int { return c.Value },
		func(c *mergedCacheItem, v int) { c.Value = v }),
)

// CachePattern.Opts' NewCacheKeyParam merge fields are wired through an
// IOPort's built Cache[Resp] handle.
func TestCachePattern_NewCacheKeyParam_WiredThroughIOPort(t *testing.T) {
	p, err := ports.NewIOPort[int, mergedCacheItem]("cache-mergefield-io", intCodec, mergedCacheItemCodec, ports.PortOptions{
		Patterns: []ports.Pattern{
			ports.CachePattern{
				Key: "item:{id}",
				Opts: []ports.CacheOpt{
					ports.NewCacheKeyParam("id", codex.String(),
						func(c mergedCacheItem) string { return c.ID },
						func(c *mergedCacheItem, v string) { c.ID = v }),
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	c, ok := ports.CacheHandle[mergedCacheItem](p)
	if !ok {
		t.Fatal("want CacheHandle to be present")
	}
	if len(c.MergeFields()) != 1 {
		t.Fatalf("want 1 merge field wired through CachePattern.Opts, got %d", len(c.MergeFields()))
	}
	var got mergedCacheItem
	if err := codex.DecodeVars(&got, map[string]string{"id": "abc"}, c.MergeFields()...); err != nil {
		t.Fatalf("DecodeVars: %v", err)
	}
	if got.ID != "abc" {
		t.Errorf("want merged ID, got %q", got.ID)
	}
}

// Same regression guard, wired through a SinkPort (the other Pattern-build
// path with the same historical bug).
func TestCachePattern_NewCacheKeyParam_WiredThroughSinkPort(t *testing.T) {
	p, err := ports.NewSinkPort[mergedCacheItem]("cache-mergefield-sink", mergedCacheItemCodec, ports.PortOptions{
		Patterns: []ports.Pattern{
			ports.CachePattern{
				Key: "item:{id}",
				Opts: []ports.CacheOpt{
					ports.NewCacheKeyParam("id", codex.String(),
						func(c mergedCacheItem) string { return c.ID },
						func(c *mergedCacheItem, v string) { c.ID = v }),
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	c, ok := ports.CacheHandle[mergedCacheItem](p)
	if !ok {
		t.Fatal("want CacheHandle to be present")
	}
	if len(c.MergeFields()) != 1 {
		t.Fatalf("want 1 merge field wired through CachePattern.Opts, got %d", len(c.MergeFields()))
	}
}

// CK11: NewCache builds a standalone Cache[T] usable with a cache adapter
// with no port/pipeline involved — same codec validation either way.
func TestNewCache_Standalone(t *testing.T) {
	c := ports.NewCache("user:{id}", format.JSON(cfgCodec),
		ports.CacheKeyParam{Name: "id"}.WithCodec(codex.String().Refine(validate.UUID)))
	c.TTL = 15 * time.Minute

	if c.TTL != 15*time.Minute {
		t.Errorf("want TTL settable via field assignment, got %v", c.TTL)
	}
	if _, err := c.BuildKey(map[string]string{"id": "not-a-uuid"}); err == nil {
		t.Error("want standalone Cache to enforce the same key codec validation")
	}
	data, err := c.Format.Marshal(cfgItem{V: 1})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := c.Format.Unmarshal(data)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.V != 1 {
		t.Errorf("want V=1, got %d", got.V)
	}
}

// ExampleNewCache shows the standalone cache descriptor — build a [ports.Cache]
// directly (no port/pipeline involved) and pass it straight to a cache
// adapter constructor (e.g. redis.GetAdapter/SetAdapter/Seed). A declared
// [ports.CacheKeyParam] codec-validates key variables before every key is
// built, exactly as it would if declared via [ports.CachePattern.Opts] on a
// port.
func ExampleNewCache() {
	userCache := ports.NewCache("user:{id}", format.JSON(cfgCodec),
		ports.CacheKeyParam{Name: "id"}.WithCodec(codex.String().Refine(validate.UUID)))

	key, err := userCache.BuildKey(map[string]string{"id": "f47ac10b-58cc-4372-a567-0e02b2c3d479"})
	fmt.Println("key:", key, "err:", err)

	_, err = userCache.BuildKey(map[string]string{"id": "not-a-uuid"})
	var paramErr ports.CacheKeyParamError
	fmt.Println("rejected invalid id:", errors.As(err, &paramErr))
	// Output:
	// key: user:f47ac10b-58cc-4372-a567-0e02b2c3d479 err: <nil>
	// rejected invalid id: true
}

// ── Phase 2: ports.NewCacheKeyParam / Cache.MergeFields ────────────────────

// C1: ports.NewCacheKeyParam registers both spec CacheKeyParam and merge field.
func TestNewCacheKeyParam_RegistersSpecAndMergeField(t *testing.T) {
	type user struct{ ID string }
	userCodec := codex.Struct[user](
		codex.RequiredField("id", codex.String(),
			func(u user) string { return u.ID },
			func(u *user, v string) { u.ID = v }),
	)
	c := ports.NewCache("user:{id}", format.JSON(userCodec),
		ports.NewCacheKeyParam("id", codex.String().Refine(validate.UUID),
			func(u user) string { return u.ID },
			func(u *user, v string) { u.ID = v }),
	)
	if len(c.MergeFields()) != 1 {
		t.Fatalf("MergeFields: want 1, got %d", len(c.MergeFields()))
	}
	if _, ok := c.KeySchemas()["id"]; !ok {
		t.Fatalf("KeySchemas: want \"id\" present, got %+v", c.KeySchemas())
	}
}

// C2: Cache.MergeFields() feeds directly into codex.EncodeVars/DecodeVars —
// round trip. MergeFields() is already strongly typed as []codex.FieldCodec[T]
// (T = user here), no type assertion needed at the call site.
func TestCacheMergeFields_EncodeDecodeRoundTrip(t *testing.T) {
	type user struct{ ID string }
	userCodec := codex.Struct[user](
		codex.RequiredField("id", codex.String(),
			func(u user) string { return u.ID },
			func(u *user, v string) { u.ID = v }),
	)
	c := ports.NewCache("user:{id}", format.JSON(userCodec),
		ports.NewCacheKeyParam("id", codex.String().Refine(validate.NonEmptyString),
			func(u user) string { return u.ID },
			func(u *user, v string) { u.ID = v }),
	)
	vars, err := codex.EncodeVars(user{ID: "u1"}, c.MergeFields()...)
	if err != nil {
		t.Fatalf("EncodeVars: %v", err)
	}
	var decoded user
	if err := codex.DecodeVars(&decoded, vars, c.MergeFields()...); err != nil {
		t.Fatalf("DecodeVars: %v", err)
	}
	if decoded.ID != "u1" {
		t.Errorf("round trip: want ID=u1, got %q", decoded.ID)
	}
}
