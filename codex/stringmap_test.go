package codex_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/validate"
)

func TestStringMap_RoundTrip(t *testing.T) {
	c := codex.StringMap(codex.Int())
	original := map[string]int{"a": 1, "b": 2}
	enc, err := c.Encode(original)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := c.Decode(enc)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	for k, v := range original {
		if got[k] != v {
			t.Errorf("key %q: want %d, got %d", k, v, got[k])
		}
	}
}

func TestStringMap_Empty(t *testing.T) {
	c := codex.StringMap(codex.String())
	enc, err := c.Encode(map[string]string{})
	if err != nil {
		t.Fatalf("Encode empty: %v", err)
	}
	got, err := c.Decode(enc)
	if err != nil {
		t.Fatalf("Decode empty: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want empty map, got %v", got)
	}
}

func TestStringMap_DecodeWrongType(t *testing.T) {
	c := codex.StringMap(codex.Int())
	if _, err := c.Decode("not-an-object"); err == nil {
		t.Fatal("expected error for non-object input")
	}
}

func TestStringMap_DecodeValueError(t *testing.T) {
	c := codex.StringMap(codex.Int())
	raw := map[string]any{"k": "not-a-number"}
	if _, err := c.Decode(raw); err == nil {
		t.Fatal("expected error for bad value type")
	}
}

func TestStringMap_Schema(t *testing.T) {
	c := codex.StringMap(codex.String())
	if c.Schema.Type != "object" {
		t.Errorf("want type=object, got %q", c.Schema.Type)
	}
	if c.Schema.AdditionalPropertiesSchema == nil {
		t.Fatal("want AdditionalPropertiesSchema set")
	}
	if c.Schema.AdditionalPropertiesSchema.Type != "string" {
		t.Errorf("want additionalProperties type=string, got %q", c.Schema.AdditionalPropertiesSchema.Type)
	}
}

func TestStringMap_SchemaDoesNotMutateInner(t *testing.T) {
	inner := codex.Int()
	_ = codex.StringMap(inner)
	if inner.Schema.AdditionalPropertiesSchema != nil {
		t.Error("StringMap should not mutate inner codec schema")
	}
}

// sensorIDPattern matches keys of the form <sensor>-<id>, e.g. "temp-01".
var sensorIDPattern = regexp.MustCompile(`^[a-z]+-\d+$`)

func TestMap_RoundTrip(t *testing.T) {
	keyCodec := codex.String().Refine(validate.Pattern(sensorIDPattern))
	c := codex.Map[string, int](keyCodec, codex.Int())
	original := map[string]int{"temp-01": 42, "press-02": 7}
	enc, err := c.Encode(original)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := c.Decode(enc)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	for k, v := range original {
		if got[k] != v {
			t.Errorf("key %q: want %d, got %d", k, v, got[k])
		}
	}
}

func TestMap_TypedKey_RoundTrip(t *testing.T) {
	type SensorID = string
	keyCodec := codex.String().Refine(validate.Pattern(sensorIDPattern)).WithTitle("SensorID")
	c := codex.Map[SensorID, float64](keyCodec, codex.Float64())
	original := map[SensorID]float64{"vibr-03": 1.5}
	enc, err := c.Encode(original)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := c.Decode(enc)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got["vibr-03"] != 1.5 {
		t.Errorf("want 1.5, got %v", got["vibr-03"])
	}
}

func TestMap_KeyValidationError_Encode(t *testing.T) {
	keyCodec := codex.String().Refine(validate.Pattern(sensorIDPattern))
	c := codex.Map[string, int](keyCodec, codex.Int())
	_, err := c.Encode(map[string]int{"INVALID_KEY": 1})
	if err == nil {
		t.Fatal("expected error for invalid key on Encode")
	}
	var keyErr codex.KeyError
	if !errors.As(err, &keyErr) {
		t.Fatalf("want KeyError, got %T: %v", err, err)
	}
	if keyErr.Key != "INVALID_KEY" {
		t.Errorf("want key=INVALID_KEY, got %q", keyErr.Key)
	}
}

func TestMap_KeyValidationError_Decode(t *testing.T) {
	keyCodec := codex.String().Refine(validate.Pattern(sensorIDPattern))
	c := codex.Map[string, int](keyCodec, codex.Int())
	raw := map[string]any{"INVALID_KEY": 1}
	_, err := c.Decode(raw)
	if err == nil {
		t.Fatal("expected error for invalid key on Decode")
	}
}

func TestMap_ValueError(t *testing.T) {
	keyCodec := codex.String().Refine(validate.Pattern(sensorIDPattern))
	c := codex.Map[string, int](keyCodec, codex.Int())
	raw := map[string]any{"temp-01": "not-a-number"}
	_, err := c.Decode(raw)
	if err == nil {
		t.Fatal("expected error for bad value type")
	}
	var keyErr codex.KeyError
	if !errors.As(err, &keyErr) {
		t.Fatalf("want KeyError wrapping value error, got %T: %v", err, err)
	}
}

func TestMap_DecodeWrongType(t *testing.T) {
	c := codex.Map[string, int](codex.String(), codex.Int())
	if _, err := c.Decode("not-an-object"); err == nil {
		t.Fatal("expected error for non-object input")
	}
}

func TestMap_Schema(t *testing.T) {
	keyCodec := codex.String().Refine(validate.Pattern(sensorIDPattern)).WithTitle("SensorID")
	c := codex.Map[string, float64](keyCodec, codex.Float64())
	s := c.Schema
	if s.Type != "object" {
		t.Errorf("want type=object, got %q", s.Type)
	}
	if s.PropertyNames == nil {
		t.Fatal("want PropertyNames set")
	}
	if s.PropertyNames.Title != "SensorID" {
		t.Errorf("want PropertyNames.Title=SensorID, got %q", s.PropertyNames.Title)
	}
	if s.AdditionalPropertiesSchema == nil {
		t.Fatal("want AdditionalPropertiesSchema set")
	}
	if s.AdditionalPropertiesSchema.Type != "number" {
		t.Errorf("want additionalProperties type=number, got %q", s.AdditionalPropertiesSchema.Type)
	}
}

// ── EntrySlice tests ─────────────────────────────────────────────────────────────

const moduleKeyPrefix = "properties.desired.modules."

type moduleConfig struct {
	Image  string
	Status string
}

type container struct {
	Name   string
	Image  string
	Status string
}

var moduleConfigCodec = codex.Struct[moduleConfig](
	codex.RequiredField("image", codex.String().Refine(validate.NonEmptyString),
		func(m moduleConfig) string { return m.Image },
		func(m *moduleConfig, v string) { m.Image = v },
	),
	codex.RequiredField("status",
		codex.String().Refine(validate.OneOf("running", "stopped")),
		func(m moduleConfig) string { return m.Status },
		func(m *moduleConfig, v string) { m.Status = v },
	),
)

var containerNameConstraint = codex.Constraint[string]{
	Name:  "container-name",
	Check: func(v string) bool { return len(v) > 0 && !strings.ContainsAny(v, " /_") },
	Message: func(v string) string {
		return fmt.Sprintf("container name %q must be non-empty and contain no spaces, slashes or underscores", v)
	},
}

var moduleFullKeyConstraint = codex.Constraint[string]{
	Name:  "module-key-path",
	Check: func(v string) bool { return strings.HasPrefix(v, moduleKeyPrefix) && len(v) > len(moduleKeyPrefix) },
	Message: func(v string) string {
		return fmt.Sprintf("key %q must start with %q followed by a module name", v, moduleKeyPrefix)
	},
}

// containerKeyCodec: wire = full dotted key; domain = container name (prefix stripped).
var containerKeyCodec = codex.MapCodecValidated(
	codex.String().Refine(moduleFullKeyConstraint),
	codex.String().Refine(containerNameConstraint),
	func(fullKey string) (string, error) {
		name := strings.TrimPrefix(fullKey, moduleKeyPrefix)
		if name == fullKey {
			return "", fmt.Errorf("key %q missing prefix", fullKey)
		}
		return name, nil
	},
	func(name string) (string, error) { return moduleKeyPrefix + name, nil },
)

var containersCodec = codex.EntrySlice(
	containerKeyCodec,
	moduleConfigCodec,
	func(name string, m moduleConfig) container {
		return container{Name: name, Image: m.Image, Status: m.Status}
	},
	func(c container) (string, moduleConfig) {
		return c.Name, moduleConfig{Image: c.Image, Status: c.Status}
	},
)

func TestEntrySlice_RoundTrip(t *testing.T) {
	input := []container{
		{Name: "cv-writer", Image: "registry/cv-writer:1.0", Status: "running"},
		{Name: "analytics", Image: "registry/analytics:2.0", Status: "stopped"},
	}
	enc, err := containersCodec.Encode(input)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	m, ok := enc.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", enc)
	}
	if _, ok := m[moduleKeyPrefix+"cv-writer"]; !ok {
		t.Errorf("expected key %q in encoded map", moduleKeyPrefix+"cv-writer")
	}

	got, err := containersCodec.Decode(enc)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 containers, got %d", len(got))
	}
	sort.Slice(got, func(i, j int) bool { return got[i].Name < got[j].Name })
	if got[0].Name != "analytics" || got[0].Image != "registry/analytics:2.0" {
		t.Errorf("unexpected container[0]: %+v", got[0])
	}
	if got[1].Name != "cv-writer" || got[1].Status != "running" {
		t.Errorf("unexpected container[1]: %+v", got[1])
	}
}

func TestEntrySlice_Empty(t *testing.T) {
	enc, err := containersCodec.Encode([]container{})
	if err != nil {
		t.Fatalf("Encode empty: %v", err)
	}
	got, err := containersCodec.Decode(enc)
	if err != nil {
		t.Fatalf("Decode empty: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty slice, got %v", got)
	}
}

func TestEntrySlice_DecodeWrongType(t *testing.T) {
	_, err := containersCodec.Decode("not-an-object")
	if err == nil {
		t.Fatal("expected error for non-object input")
	}
	var tm codex.TypeMismatchError
	if !errors.As(err, &tm) {
		t.Errorf("expected TypeMismatchError, got %T: %v", err, err)
	}
}

func TestEntrySlice_KeyError_Decode(t *testing.T) {
	// Key missing the required prefix → keyCodec.Decode fails.
	raw := map[string]any{
		"invalid-no-prefix": map[string]any{"image": "img:1", "status": "running"},
	}
	_, err := containersCodec.Decode(raw)
	if err == nil {
		t.Fatal("expected KeyError for invalid key")
	}
	var ke codex.KeyError
	if !errors.As(err, &ke) {
		t.Fatalf("expected KeyError, got %T: %v", err, err)
	}
	if ke.Key != "invalid-no-prefix" {
		t.Errorf("expected Key=%q, got %q", "invalid-no-prefix", ke.Key)
	}
}

func TestEntrySlice_KeyError_Encode(t *testing.T) {
	// Container name contains underscore — fails containerNameConstraint.
	bad := []container{{Name: "bad_name", Image: "img:1", Status: "running"}}
	_, err := containersCodec.Encode(bad)
	if err == nil {
		t.Fatal("expected KeyError for invalid container name on Encode")
	}
	var ke codex.KeyError
	if !errors.As(err, &ke) {
		t.Fatalf("expected KeyError, got %T: %v", err, err)
	}
}

func TestEntrySlice_ValueError_Decode(t *testing.T) {
	// Value has an invalid status — valueCodec.Decode fails.
	raw := map[string]any{
		moduleKeyPrefix + "cv-writer": map[string]any{"image": "img:1", "status": "INVALID"},
	}
	_, err := containersCodec.Decode(raw)
	if err == nil {
		t.Fatal("expected KeyError wrapping value error")
	}
	var ke codex.KeyError
	if !errors.As(err, &ke) {
		t.Fatalf("expected KeyError, got %T: %v", err, err)
	}
	if ke.Key != moduleKeyPrefix+"cv-writer" {
		t.Errorf("expected key=%q, got %q", moduleKeyPrefix+"cv-writer", ke.Key)
	}
}

func TestEntrySlice_Schema(t *testing.T) {
	s := containersCodec.Schema
	if s.Type != "object" {
		t.Errorf("expected schema type=object, got %q", s.Type)
	}
	if s.PropertyNames == nil {
		t.Fatal("expected PropertyNames set")
	}
	if s.AdditionalPropertiesSchema == nil {
		t.Fatal("expected AdditionalPropertiesSchema set")
	}
}

func TestEntrySlice_WithYAML_RoundTrip(t *testing.T) {
	f := format.YAML(containersCodec)
	input := []container{
		{Name: "cv-writer", Image: "registry/cv-writer:1.0", Status: "running"},
	}
	data, err := f.Marshal(input)
	if err != nil {
		t.Fatalf("YAML Marshal: %v", err)
	}
	got, err := f.Unmarshal(data)
	if err != nil {
		t.Fatalf("YAML Unmarshal: %v", err)
	}
	if len(got) != 1 || got[0].Name != "cv-writer" {
		t.Errorf("YAML round-trip: got %+v", got)
	}
}

func TestEntrySlice_WithTOML_RoundTrip(t *testing.T) {
	f := format.TOML(containersCodec)
	input := []container{
		{Name: "cv-writer", Image: "registry/cv-writer:1.0", Status: "running"},
	}
	data, err := f.Marshal(input)
	if err != nil {
		t.Fatalf("TOML Marshal: %v", err)
	}
	got, err := f.Unmarshal(data)
	if err != nil {
		t.Fatalf("TOML Unmarshal: %v", err)
	}
	if len(got) != 1 || got[0].Name != "cv-writer" {
		t.Errorf("TOML round-trip: got %+v", got)
	}
}

func ExampleEntrySlice() {
	type ModuleConfig struct {
		Image  string
		Status string
	}
	type Container struct {
		Name   string
		Image  string
		Status string
	}

	const prefix = "modules."
	keyCodec := codex.MapCodecSafe(
		codex.String(),
		func(fullKey string) string { return strings.TrimPrefix(fullKey, prefix) },
		func(name string) (string, error) { return prefix + name, nil },
	)
	valueCodec := codex.Struct[ModuleConfig](
		codex.RequiredField("image", codex.String(),
			func(m ModuleConfig) string { return m.Image },
			func(m *ModuleConfig, v string) { m.Image = v },
		),
		codex.RequiredField("status", codex.String(),
			func(m ModuleConfig) string { return m.Status },
			func(m *ModuleConfig, v string) { m.Status = v },
		),
	)
	c := codex.EntrySlice(
		keyCodec,
		valueCodec,
		func(name string, m ModuleConfig) Container {
			return Container{Name: name, Image: m.Image, Status: m.Status}
		},
		func(c Container) (string, ModuleConfig) {
			return c.Name, ModuleConfig{Image: c.Image, Status: c.Status}
		},
	)

	raw := map[string]any{
		"modules.writer": map[string]any{"image": "registry/writer:1.0", "status": "running"},
	}
	containers, _ := c.Decode(raw)
	data, _ := json.Marshal(containers)
	fmt.Println(string(data))
	// Output: [{"Name":"writer","Image":"registry/writer:1.0","Status":"running"}]
}
