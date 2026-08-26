// Package typed-params demonstrates codex.StringCodec/codex.TextCodec/
// codex.IntString (and friends) — path/topic/query/header/cookie/key
// variables merged directly into a NON-string Go type instead of a bare
// string the handler has to re-parse by hand.
//
// codex.DecodeVars/codex.EncodeVars box every var value as a plain Go
// string on the wire — that's the only requirement. Any Codec[V] that
// decodes-from and encodes-to a string composes with rest.NewPathParam/
// events.NewTopicParam/etc. (all generic over V, not hardcoded to
// Codec[string]).
//
// Three scenes:
//   - codex.TextCodec[uuid.UUID](): zero-boilerplate for any type whose
//     pointer implements encoding.TextMarshaler/TextUnmarshaler (uuid.UUID
//     already does) — a REST path param merges straight into a uuid.UUID
//     field, no manual uuid.Parse in the handler.
//   - codex.IntString(): a ready-made stdlib convenience codec — an
//     events topic segment merges straight into an int field.
//   - codex.StringCodec(parse, format, schema): the fully explicit escape
//     hatch for a custom type with no pre-existing text form.
//
// # Running
//
// go run ./examples/typed-params
package main

import (
	"fmt"

	"github.com/google/uuid"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
)

// ── Scene 1: codex.TextCodec[uuid.UUID]() for a REST path param ──────────

// GetUserReq.ID is a REAL uuid.UUID — not a string the handler has to
// parse itself.
type GetUserReq struct {
	ID uuid.UUID
}

var getUserReqCodec = codex.Struct[GetUserReq](
	codex.RequiredField("id", codex.TextCodec[uuid.UUID](),
		func(r GetUserReq) uuid.UUID { return r.ID },
		func(r *GetUserReq, v uuid.UUID) { r.ID = v }),
)

type UserResp struct{ Name string }

var userRespCodec = codex.Struct[UserResp](
	codex.RequiredField("name", codex.String(),
		func(u UserResp) string { return u.Name },
		func(u *UserResp, v string) { u.Name = v }),
)

// ── Scene 2: codex.IntString() for an events topic param ─────────────────

// SensorReading.Seq is a REAL int — merged straight from the topic
// segment, no manual strconv.Atoi in the subscriber.
type SensorReading struct {
	Seq   int
	Value float64
}

var sensorReadingCodec = codex.Struct[SensorReading](
	codex.RequiredField("value", codex.Float64(),
		func(r SensorReading) float64 { return r.Value },
		func(r *SensorReading, v float64) { r.Value = v }),
)

var sensorChannel = events.NewChannel[SensorReading]("sensors/{seq}/readings", sensorReadingCodec,
	events.NewTopicParam("seq", codex.IntString(),
		func(r SensorReading) int { return r.Seq },
		func(r *SensorReading, v int) { r.Seq = v },
	).WithDescription("Reading sequence number — merged from the topic, never the payload"),
)

// ── Scene 3: codex.StringCodec for a custom type with no pre-existing
// text form (the fully explicit escape hatch) ─────────────────────────────

// Slug is a validated "url-safe identifier" type with its own String
// method for display, but no MarshalText/UnmarshalText — StringCodec lets
// us supply parse/format directly instead of implementing those
// interfaces just for this one use.
type Slug string

func (s Slug) String() string { return string(s) }

func parseSlug(s string) (Slug, error) {
	if s == "" {
		return "", fmt.Errorf("slug must not be empty")
	}
	for _, r := range s {
		if !(r == '-' || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')) {
			return "", fmt.Errorf("slug %q: only lowercase letters, digits, and '-' allowed", s)
		}
	}
	return Slug(s), nil
}

var slugCodec = codex.StringCodec(parseSlug,
	func(s Slug) (string, error) { return string(s), nil },
	codex.String().Schema)

type ArticleReq struct{ Slug Slug }

var articleReqCodec = codex.Struct[ArticleReq](
	codex.RequiredField("slug", slugCodec,
		func(r ArticleReq) Slug { return r.Slug },
		func(r *ArticleReq, v Slug) { r.Slug = v }),
)

func main() {
	// ── Scene 1: uuid.UUID path param ──────────────────────────────────
	fmt.Println("─── Scene 1: codex.TextCodec[uuid.UUID]() — REST path param")

	b := rest.NewBuilder(rest.Info{Title: "Typed Params Demo", Version: "1.0.0"})
	getUser, err := rest.NewRoute[GetUserReq, UserResp]("GET", "/users/{id}",
		getUserReqCodec, userRespCodec,
		rest.NewPathParam("id", codex.TextCodec[uuid.UUID](),
			func(r GetUserReq) uuid.UUID { return r.ID },
			func(r *GetUserReq, v uuid.UUID) { r.ID = v }),
	).Register(b)
	if err != nil {
		panic(err)
	}

	id := uuid.MustParse("f47ac10b-58cc-4372-a567-0e02b2c3d479")
	path, err := getUser.BuildPath(map[string]string{"id": id.String()})
	if err != nil {
		panic(err)
	}
	fmt.Printf("  BuildPath: %s\n", path)

	req, err := getUser.DecodeMerged(nil, map[string]string{"id": id.String()}, nil, nil, nil)
	if err != nil {
		panic(err)
	}
	fmt.Printf("  DecodeMerged: req.ID = %s (type %T)\n", req.ID, req.ID)

	if err := getUser.ValidatePathParams(map[string]string{"id": "not-a-uuid"}); err != nil {
		fmt.Println("  invalid UUID correctly rejected:", err)
	}

	// ── Scene 2: int topic param ───────────────────────────────────────
	fmt.Println("\n─── Scene 2: codex.IntString() — events topic param")

	eb := events.NewBuilder(events.Info{Title: "Sensor Events", Version: "1.0.0"})
	sensorHandle, err := sensorChannel.Register(eb)
	if err != nil {
		panic(err)
	}

	reading, err := sensorHandle.DecodeMerged([]byte(`{"value":21.5}`), map[string]string{"seq": "7"})
	if err != nil {
		panic(err)
	}
	fmt.Printf("  DecodeMerged: reading.Seq = %d (type %T), Value = %v\n", reading.Seq, reading.Seq, reading.Value)

	vars, err := codex.EncodeVars(SensorReading{Seq: 7}, sensorHandle.MergeFields()...)
	if err != nil {
		panic(err)
	}
	fmt.Printf("  EncodeVars: %v\n", vars)

	if err := sensorHandle.ValidateTopicVars(map[string]string{"seq": "not-a-number"}); err != nil {
		fmt.Println("  invalid seq correctly rejected:", err)
	}

	// ── Scene 3: custom type via codex.StringCodec ─────────────────────
	fmt.Println("\n─── Scene 3: codex.StringCodec — custom type escape hatch")

	ab := rest.NewBuilder(rest.Info{Title: "Articles Demo", Version: "1.0.0"})
	getArticle, err := rest.NewRoute[ArticleReq, UserResp]("GET", "/articles/{slug}",
		articleReqCodec, userRespCodec,
		rest.NewPathParam("slug", slugCodec,
			func(r ArticleReq) Slug { return r.Slug },
			func(r *ArticleReq, v Slug) { r.Slug = v }),
	).Register(ab)
	if err != nil {
		panic(err)
	}

	artReq, err := getArticle.DecodeMerged(nil, map[string]string{"slug": "hello-world"}, nil, nil, nil)
	if err != nil {
		panic(err)
	}
	fmt.Printf("  DecodeMerged: req.Slug = %s (type %T)\n", artReq.Slug, artReq.Slug)

	if err := getArticle.ValidatePathParams(map[string]string{"slug": "Not Valid!"}); err != nil {
		fmt.Println("  invalid slug correctly rejected:", err)
	}
}
