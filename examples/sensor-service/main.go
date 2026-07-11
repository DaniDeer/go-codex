// Package sensor-service demonstrates the three-layer codec pipeline pattern
// across three transport adapters — HTTP, MQTT, and SQL — wired together with
// a single shared observer for metrics and logging.
//
// # Stream bridge helpers showcased
//
// This example uses the stream bridge helpers added in the stream bridge release:
//
//   - [adaptermqtt.SubscribeStream] — replaces rawCh + Subscribe + FromCodec
//     boilerplate; returns (Stream[MQTTPayload], pahomqtt.MessageHandler).
//   - [adaptermqtt.DrainPublish] — replaces the manual Drain + Publish loop for
//     alert publishing; takes Stream[SensorAlert] directly.
//   - [nethttp.HandlerLatest] — reactive cache endpoint; GET /readings/latest
//     returns the most recently saved reading without querying the DB.
//   - [sqladapter.QueryStream] — polls the DB at a fixed interval and emits
//     each row as a typed Stream[db.Reading]; showcases the SQL source bridge.
//
// # Three-layer model
//
// Layer 1 (Contracts): boundary codecs, each describing one wire format.
//
//	createReadingCodec  — HTTP POST /readings request body
//	readingCodec        — HTTP GET response + SQL post-read validation
//	insertParamsCodec   — SQL pre-insert validation (db.InsertReadingParams)
//	mqttPayloadCodec    — MQTT subscribe payload (sensor publishes)
//	alertCodec          — MQTT publish payload (alert events)
//
// Layer 2 (Domain): pure Go functions, zero IO.
//
//	buildInsertParams(CreateReadingReq) db.InsertReadingParams
//	buildInsertParamsFromMQTT(MQTTPayload) db.InsertReadingParams
//	shouldAlert(db.Reading) bool
//	buildAlert(db.Reading) SensorAlert
//
// Layer 3 (Infrastructure): HTTP adapter, mock MQTT client, SQL store.
//
// # Field factory functions
//
// [db.Reading] and [db.InsertReadingParams] share the same columns. Field
// factory functions — [sensorIDField], [valueField], [unitField] — capture the
// shared [validate] rules once and are reused across both codecs. This is the
// "field factory functions: reusing field groups across structs" pattern
// documented in docs/concepts/codec.md.
//
// # Observer
//
// A single [stats.NewFanout] value is passed to every adapter call site:
//
//	obs := stats.NewFanout(counting, stats.NewLoggingObserver(logger))
//
// [CountingObserver] implements both [stats.Observer] and [stats.SQLObserver].
// It records HTTP request counts by status, validation error counts by
// location, SQL validation calls, and migration events — all in memory.
// In production replace the counters with Prometheus / OpenTelemetry instruments;
// the interface contract is identical.
//
// # Running
//
// go run ./examples/sensor-service
//
//go:generate sqlc generate
package main

import (
	"bytes"
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"
	_ "modernc.org/sqlite"

	adaptermqtt "github.com/DaniDeer/go-codex/adapters/mqtt"
	nethttp "github.com/DaniDeer/go-codex/adapters/nethttp"
	sqladapter "github.com/DaniDeer/go-codex/adapters/sql"
	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/examples/sensor-service/db"
	"github.com/DaniDeer/go-codex/forge"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/stats"
	gstream "github.com/DaniDeer/go-codex/stream"
	"github.com/DaniDeer/go-codex/validate"
)

// latestRoute — GET /readings/latest, served by nethttp.HandlerLatest (reactive cache).
// Returns the most recently saved sensor reading without querying the DB.
var latestRoute = rest.NewRoute("GET", "/readings/latest",
	codex.Struct[struct{}](), readingCodec,
)

// ── Embedded assets ───────────────────────────────────────────────────────────

//go:embed migrations/*.sql
var migrationsFS embed.FS

// ── Observer ──────────────────────────────────────────────────────────────────

// CountingObserver is an in-memory [stats.Observer] + [stats.SQLObserver] +
// [stats.StreamObserver]. In production replace the maps and counters with
// Prometheus CounterVecs or OpenTelemetry instruments — the interface methods
// are identical.
type CountingObserver struct {
	mu             sync.Mutex
	requests       map[int]int    // HTTP status code → request count
	subscribes     int            // MQTT RecordSubscribe calls (alert publishes via adaptermqtt.Publish)
	publishes      int            // successful MQTT publish calls
	streamItems    int            // stream.Apply RecordStreamItem calls (one per MQTT sensor reading)
	valErrors      map[string]int // location → validation error count
	sqlValidations int
	migrations     int
}

func newCountingObserver() *CountingObserver {
	return &CountingObserver{
		requests:  make(map[int]int),
		valErrors: make(map[string]int),
	}
}

func (o *CountingObserver) RecordRequest(_ string, _ string, status int, _ time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.requests[status]++
}

func (o *CountingObserver) RecordSubscribe(_ string, success bool, _ time.Duration) {
	if success {
		o.mu.Lock()
		o.subscribes++
		o.mu.Unlock()
	}
}

func (o *CountingObserver) RecordPublish(_ string, success bool, _ time.Duration) {
	if success {
		o.mu.Lock()
		o.publishes++
		o.mu.Unlock()
	}
}

func (o *CountingObserver) RecordValidationError(location, _, _ string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.valErrors[location]++
}

// RecordValidation implements [stats.SQLObserver].
func (o *CountingObserver) RecordValidation(_, _ string, _ time.Duration, _ error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.sqlValidations++
}

// RecordMigration implements [stats.SQLObserver].
func (o *CountingObserver) RecordMigration(_, _ string, _ int64, _ time.Duration, _ error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.migrations++
}

// RecordStreamItem implements [stats.StreamObserver].
func (o *CountingObserver) RecordStreamItem(_ string, success bool, _ time.Duration) {
	if success {
		o.mu.Lock()
		o.streamItems++
		o.mu.Unlock()
	}
}

func (o *CountingObserver) Print() {
	o.mu.Lock()
	defer o.mu.Unlock()
	fmt.Println("\n── Observer summary ─────────────────────────────────────")
	fmt.Printf("  HTTP requests by status : %v\n", o.requests)
	fmt.Printf("  MQTT publishes  (ok)    : %d\n", o.publishes)
	fmt.Printf("  Stream items processed  : %d\n", o.streamItems)
	fmt.Printf("  SQL validations called  : %d\n", o.sqlValidations)
	fmt.Printf("  Migrations applied      : %d\n", o.migrations)
	if len(o.valErrors) > 0 {
		fmt.Printf("  Validation errors       : %v\n", o.valErrors)
	} else {
		fmt.Println("  Validation errors       : none")
	}
	fmt.Println("─────────────────────────────────────────────────────────")
}

// ── Domain types ──────────────────────────────────────────────────────────────

// CreateReadingReq is the HTTP POST /readings request body.
// The server assigns ID and RecordedAt.
type CreateReadingReq struct {
	SensorID string
	Value    float64
	Unit     string
}

// MQTTPayload is what an external sensor publishes over MQTT.
type MQTTPayload struct {
	SensorID string
	Value    float64
	Unit     string
}

// SensorAlert is published over MQTT when a reading exceeds the threshold.
type SensorAlert struct {
	SensorID  string
	Value     float64
	Threshold float64
	At        string // RFC3339
}

const alertThreshold = 50.0

// ── Layer 1: Field factory functions ─────────────────────────────────────────
//
// Each factory captures one field's Refine rules and returns a [codex.Field]
// bound to the concrete struct type T. Reusing factories across db.Reading and
// db.InsertReadingParams ensures both types share identical validation rules
// with no duplication.

func sensorIDField[T any](
	get func(T) string,
	set func(*T, string),
) codex.Field[T, string] {
	return codex.RequiredField("sensor_id",
		codex.String().Refine(validate.UUID),
		get, set)
}

func valueField[T any](
	get func(T) float64,
	set func(*T, float64),
) codex.Field[T, float64] {
	return codex.RequiredField("value",
		codex.Float64().Refine(validate.RangeFloat(-9999, 9999)),
		get, set)
}

func unitField[T any](
	get func(T) string,
	set func(*T, string),
) codex.Field[T, string] {
	return codex.RequiredField("unit",
		codex.String().Refine(validate.OneOf("C", "F", "pct", "Pa", "ms")),
		get, set)
}

func recordedAtField[T any](
	get func(T) string,
	set func(*T, string),
) codex.Field[T, string] {
	return codex.RequiredField("recorded_at",
		codex.String().Refine(validate.DateTime),
		get, set)
}

// ── Layer 1: Codecs ───────────────────────────────────────────────────────────

// createReadingCodec — HTTP POST /readings request body.
var createReadingCodec = codex.Struct(
	sensorIDField(
		func(r CreateReadingReq) string { return r.SensorID },
		func(r *CreateReadingReq, v string) { r.SensorID = v }),
	valueField(
		func(r CreateReadingReq) float64 { return r.Value },
		func(r *CreateReadingReq, v float64) { r.Value = v }),
	unitField(
		func(r CreateReadingReq) string { return r.Unit },
		func(r *CreateReadingReq, v string) { r.Unit = v }),
)

// readingCodec — HTTP GET response + SQL post-read validation.
// Uses the same field factories as insertParamsCodec for identical Refine rules.
var readingCodec = codex.Struct(
	codex.RequiredField("id",
		codex.String().Refine(validate.UUID),
		func(r db.Reading) string { return r.ID },
		func(r *db.Reading, v string) { r.ID = v }),
	sensorIDField(
		func(r db.Reading) string { return r.SensorID },
		func(r *db.Reading, v string) { r.SensorID = v }),
	valueField(
		func(r db.Reading) float64 { return r.Value },
		func(r *db.Reading, v float64) { r.Value = v }),
	unitField(
		func(r db.Reading) string { return r.Unit },
		func(r *db.Reading, v string) { r.Unit = v }),
	recordedAtField(
		func(r db.Reading) string { return r.RecordedAt },
		func(r *db.Reading, v string) { r.RecordedAt = v }),
)

// insertParamsCodec — SQL pre-insert validation.
// Shares sensorIDField/valueField/unitField with readingCodec — same Refine
// rules, different struct type T, zero duplication.
var insertParamsCodec = codex.Struct(
	codex.RequiredField("id",
		codex.String().Refine(validate.UUID),
		func(p db.InsertReadingParams) string { return p.ID },
		func(p *db.InsertReadingParams, v string) { p.ID = v }),
	sensorIDField(
		func(p db.InsertReadingParams) string { return p.SensorID },
		func(p *db.InsertReadingParams, v string) { p.SensorID = v }),
	valueField(
		func(p db.InsertReadingParams) float64 { return p.Value },
		func(p *db.InsertReadingParams, v float64) { p.Value = v }),
	unitField(
		func(p db.InsertReadingParams) string { return p.Unit },
		func(p *db.InsertReadingParams, v string) { p.Unit = v }),
	recordedAtField(
		func(p db.InsertReadingParams) string { return p.RecordedAt },
		func(p *db.InsertReadingParams, v string) { p.RecordedAt = v }),
)

// mqttPayloadCodec — what external sensors publish over MQTT.
var mqttPayloadCodec = codex.Struct(
	sensorIDField(
		func(p MQTTPayload) string { return p.SensorID },
		func(p *MQTTPayload, v string) { p.SensorID = v }),
	valueField(
		func(p MQTTPayload) float64 { return p.Value },
		func(p *MQTTPayload, v float64) { p.Value = v }),
	unitField(
		func(p MQTTPayload) string { return p.Unit },
		func(p *MQTTPayload, v string) { p.Unit = v }),
)

// alertCodec — alert events published when a reading exceeds the threshold.
var alertCodec = codex.Struct(
	sensorIDField(
		func(a SensorAlert) string { return a.SensorID },
		func(a *SensorAlert, v string) { a.SensorID = v }),
	valueField(
		func(a SensorAlert) float64 { return a.Value },
		func(a *SensorAlert, v float64) { a.Value = v }),
	codex.RequiredField("threshold",
		codex.Float64(),
		func(a SensorAlert) float64 { return a.Threshold },
		func(a *SensorAlert, v float64) { a.Threshold = v }),
	codex.RequiredField("at",
		codex.String().Refine(validate.DateTime),
		func(a SensorAlert) string { return a.At },
		func(a *SensorAlert, v string) { a.At = v }),
)

// ── Layer 1: API declarations ─────────────────────────────────────────────────
//
// Channel and route declarations are pure value expressions — no side effects,
// no registration. They name and type every API surface of the service and can
// be read as a compact spec independent of the infrastructure wiring in main().
//
// Register/ClientHandle calls (which produce *ChannelHandle and *RouteHandle)
// happen in main() where a builder or mux is already available.

var readingChannel = events.NewChannel(
	"sensors/{sensorID}/data",
	mqttPayloadCodec,
	events.TopicParam{Name: "sensorID", Description: "UUID of the publishing sensor"},
)

var alertChannel = events.NewChannel(
	"alerts/{sensorID}",
	alertCodec,
	events.TopicParam{Name: "sensorID", Description: "UUID of the sensor that triggered the alert"},
)

var createRoute = rest.NewRoute("POST", "/readings",
	createReadingCodec, readingCodec,
)

var getRoute = rest.NewRoute("GET", "/readings/{id}",
	codex.Struct[struct{}](), readingCodec,
	rest.PathParam{Name: "id", Description: "Reading UUID"},
)

// ── Layer 2: Domain functions ─────────────────────────────────────────────────

func newReadingID() string {
	// Deterministic UUID-shaped ID from timestamp — avoids a uuid dependency.
	t := time.Now().UnixNano()
	return fmt.Sprintf("%08x-0001-4000-8000-%012x", t>>32, t&0xffffffffffff)
}

func buildInsertParams(req CreateReadingReq) db.InsertReadingParams {
	return db.InsertReadingParams{
		ID:         newReadingID(),
		SensorID:   req.SensorID,
		Value:      req.Value,
		Unit:       req.Unit,
		RecordedAt: time.Now().UTC().Format(time.RFC3339),
	}
}

func buildInsertParamsFromMQTT(p MQTTPayload) db.InsertReadingParams {
	return db.InsertReadingParams{
		ID:         newReadingID(),
		SensorID:   p.SensorID,
		Value:      p.Value,
		Unit:       p.Unit,
		RecordedAt: time.Now().UTC().Format(time.RFC3339),
	}
}

func shouldAlert(r db.Reading) bool { return r.Value > alertThreshold }

func buildAlert(r db.Reading) SensorAlert {
	return SensorAlert{
		SensorID:  r.SensorID,
		Value:     r.Value,
		Threshold: alertThreshold,
		At:        time.Now().UTC().Format(time.RFC3339),
	}
}

// ── Layer 3: ReadingStore (SQL) ───────────────────────────────────────────────

// ReadingStore wraps the sqlc-generated *db.Queries.
//
// Every write path calls sqladapter.Validate(insertParamsCodec, ...) before
// reaching the DB — codec rejects invalid data so it never reaches SQL.
//
// Every read path calls sqladapter.Validate(readingCodec, ...) after the DB
// returns a row — defence in depth against data written by other clients that
// bypassed the codec.
type ReadingStore struct {
	queries *db.Queries
}

func (s *ReadingStore) Save(ctx context.Context, params db.InsertReadingParams, obs stats.Observer) error {
	validated, err := sqladapter.Validate(insertParamsCodec, params, sqladapter.ValidateOptions{
		Table: "readings", Op: "insert_reading", Observer: obs,
	})
	if err != nil {
		return fmt.Errorf("pre-insert validation: %w", err)
	}
	return s.queries.InsertReading(ctx, validated)
}

func (s *ReadingStore) Get(ctx context.Context, id string, obs stats.Observer) (db.Reading, error) {
	row, err := s.queries.GetReading(ctx, id)
	if err != nil {
		return db.Reading{}, err
	}
	return sqladapter.Validate(readingCodec, row, sqladapter.ValidateOptions{
		Table: "readings", Op: "get_reading", Observer: obs,
	})
}

// ── Layer 3: HTTP handlers ────────────────────────────────────────────────────

func makeCreateHandler(store *ReadingStore, obs stats.Observer) nethttp.HandlerFunc[CreateReadingReq, db.Reading] {
	return func(ctx context.Context, req CreateReadingReq) (db.Reading, error) {
		params := buildInsertParams(req)
		if err := store.Save(ctx, params, obs); err != nil {
			return db.Reading{}, err
		}
		return store.Get(ctx, params.ID, obs)
	}
}

func makeGetHandler(store *ReadingStore, obs stats.Observer) nethttp.HandlerFunc[struct{}, db.Reading] {
	return func(ctx context.Context, _ struct{}) (db.Reading, error) {
		r, _ := nethttp.RequestFromContext(ctx)
		id := r.PathValue("id")
		return store.Get(ctx, id, obs)
	}
}

// ── Layer 3: Mock MQTT client ─────────────────────────────────────────────────

type mockToken struct{ done chan struct{} }

func newMockToken() *mockToken {
	t := &mockToken{done: make(chan struct{})}
	close(t.done)
	return t
}

func (t *mockToken) Wait() bool                       { return true }
func (t *mockToken) WaitTimeout(_ time.Duration) bool { return true }
func (t *mockToken) Done() <-chan struct{}            { return t.done }
func (t *mockToken) Error() error                     { return nil }

type mockMessage struct {
	topic   string
	payload []byte
}

func (m *mockMessage) Duplicate() bool   { return false }
func (m *mockMessage) Qos() byte         { return 0 }
func (m *mockMessage) Retained() bool    { return false }
func (m *mockMessage) Topic() string     { return m.topic }
func (m *mockMessage) MessageID() uint16 { return 0 }
func (m *mockMessage) Payload() []byte   { return m.payload }
func (m *mockMessage) Ack()              {}

type mockClient struct {
	mu       sync.Mutex
	handlers map[string]pahomqtt.MessageHandler
}

func newMockClient() *mockClient {
	return &mockClient{handlers: make(map[string]pahomqtt.MessageHandler)}
}

func (c *mockClient) Publish(_ string, _ byte, _ bool, _ interface{}) pahomqtt.Token {
	return newMockToken()
}

func (c *mockClient) Subscribe(topic string, _ byte, h pahomqtt.MessageHandler) pahomqtt.Token {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.handlers[topic] = h
	return newMockToken()
}

func (c *mockClient) Unsubscribe(_ ...string) pahomqtt.Token { return newMockToken() }

// deliver simulates a sensor publishing a message on topic.
// It finds the first registered subscription filter that matches the topic,
// supporting the MQTT '+' single-level wildcard.
func (c *mockClient) deliver(topic string, payload []byte) {
	c.mu.Lock()
	var h pahomqtt.MessageHandler
	for filter, handler := range c.handlers {
		if mqttMatches(filter, topic) {
			h = handler
			break
		}
	}
	c.mu.Unlock()
	if h != nil {
		h(c, &mockMessage{topic: topic, payload: payload})
	}
}

// mqttMatches reports whether subscription filter matches concrete topic.
// Supports '+' (single-level wildcard) and '#' (multi-level wildcard).
func mqttMatches(filter, topic string) bool {
	if filter == topic {
		return true
	}
	fs := splitTopic(filter)
	ts := splitTopic(topic)
	for i, f := range fs {
		if f == "#" {
			return true
		}
		if i >= len(ts) {
			return false
		}
		if f != "+" && f != ts[i] {
			return false
		}
	}
	return len(fs) == len(ts)
}

func splitTopic(t string) []string {
	var parts []string
	start := 0
	for i := 0; i <= len(t); i++ {
		if i == len(t) || t[i] == '/' {
			parts = append(parts, t[start:i])
			start = i + 1
		}
	}
	return parts
}

func (c *mockClient) IsConnected() bool                            { return true }
func (c *mockClient) IsConnectionOpen() bool                       { return true }
func (c *mockClient) Connect() pahomqtt.Token                      { return newMockToken() }
func (c *mockClient) Disconnect(_ uint)                            {}
func (c *mockClient) AddRoute(_ string, _ pahomqtt.MessageHandler) {}
func (c *mockClient) SubscribeMultiple(_ map[string]byte, _ pahomqtt.MessageHandler) pahomqtt.Token {
	return newMockToken()
}
func (c *mockClient) OptionsReader() pahomqtt.ClientOptionsReader {
	return pahomqtt.ClientOptionsReader{}
}

// ── main ──────────────────────────────────────────────────────────────────────

func main() {
	ctx, cancelPipeline := context.WithCancel(context.Background())

	// ── Observability ──────────────────────────────────────────────────────
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	counting := newCountingObserver()
	obs := stats.NewFanout(counting, stats.NewLoggingObserver(logger))

	// ── Database ───────────────────────────────────────────────────────────
	sqlDB, err := sql.Open("sqlite", "file::memory:?cache=private")
	must(err, "open db")
	defer sqlDB.Close()

	migrator, err := sqladapter.NewMigrator(sqlDB, migrationsFS, "migrations", "sqlite3")
	must(err, "new migrator")
	must(migrator.Up(ctx, sqladapter.MigrateOptions{Observer: obs}), "migrate up")
	fmt.Println("✓ Migrations applied")

	store := &ReadingStore{queries: db.New(sqlDB)}

	// ── HTTP mux (declared early — HandlerLatest wires into it) ───────────
	mux := http.NewServeMux()

	// ── MQTT + stream pipeline ────────────────────────────────────────────
	mqttClient := newMockClient()

	// Register channels with a builder to get typed *ChannelHandle values.
	eventsBuilder := events.NewBuilder(events.Info{Title: "sensor-service", Version: "1.0.0"})
	readingHandle, err := readingChannel.Register(eventsBuilder)
	must(err, "register reading channel")
	alertHandle, err := alertChannel.Register(eventsBuilder)
	must(err, "register alert channel")

	// ── Bridge 1: mqtt.SubscribeStream ────────────────────────────────────
	//
	// mqtt.SubscribeStream replaces the manual rawCh + Subscribe + FromCodec
	// boilerplate. It returns a typed stream AND the MessageHandler to register
	// with the MQTT client. The caller owns the subscription lifecycle.
	//
	//   Before (manual bridge):
	//     rawCh := make(chan []byte, 64)
	//     mqttClient.Subscribe(topic, 0, func(_, msg) { rawCh <- msg.Payload() })
	//     sensors := gstream.FromCodec(ctx, rawCh, format.JSON(codec), srcOpts)
	//
	//   After (bridge helper):
	//     sensors, handler := mqtt.SubscribeStream(ctx, handle, fmt, srcOpts, subOpts)
	//     mqttClient.Subscribe(handle.Topic, 0, handler)
	sensors, mqttSubHandler := adaptermqtt.SubscribeStream(ctx, readingHandle,
		format.JSON(mqttPayloadCodec),
		gstream.SourceOptions{Name: "mqtt/sensors/+/data", Observer: obs, Buffer: 64},
		adaptermqtt.SubscribeOptions{Observer: obs})
	// Subscribe with the MQTT wildcard filter (sensors/+/data), not the API
	// template (sensors/{sensorID}/data). The ChannelHandle stores the API
	// template for spec generation; the MQTT broker needs the MQTT wildcard.
	mqttClient.Subscribe("sensors/+/data", 0, mqttSubHandler)

	// ── Stream pipeline: decode → save → tap → tee → filter → alert ──────
	//
	// Each operator is a typed free function — compose like Unix pipes.

	// Forge function: MQTTPayload → db.Reading (save to DB, return stored row)
	saveReadingFn := forge.NewFunction(
		"saveReading", "1.0.0",
		mqttPayloadCodec, readingCodec,
		func(payload MQTTPayload) (db.Reading, error) {
			params := buildInsertParamsFromMQTT(payload)
			if err := store.Save(ctx, params, obs); err != nil {
				return db.Reading{}, err
			}
			return store.Get(ctx, params.ID, obs)
		},
		forge.FunctionMeta{Description: "Save MQTT sensor payload to DB and return the stored row."},
	)

	readings := gstream.Apply(ctx, sensors, saveReadingFn,
		gstream.ApplyOptions{Observer: obs})

	// Tap: domain event observation — log every computed reading
	readings = gstream.Tap(ctx, readings, func(r db.Reading) {
		logger.Info("reading saved", "sensor", r.SensorID, "value", r.Value, "unit", r.Unit)
	})

	// Tee: fan-out — one copy feeds the HTTP reactive cache, one feeds alerting
	latestReadings, alertReadings := gstream.Tee(ctx, readings)

	// ── Bridge 2: nethttp.HandlerLatest ───────────────────────────────────
	//
	// HandlerLatest serves GET /readings/latest with the most recently saved
	// reading from the stream — no DB query per request. A background goroutine
	// atomically stores each emitted reading. Before the first reading arrives,
	// the handler returns 503 Service Unavailable + NoLatestValueError.
	nethttp.RegisterLatest(mux, latestRoute.ClientHandle(), latestReadings, nethttp.Options{Observer: obs})

	// Filter: keep only readings that cross the alert threshold
	aboveThreshold := gstream.Filter(ctx, alertReadings, shouldAlert)

	// Convert db.Reading → SensorAlert for the MQTT alert topic
	alertPayloads := gstream.FlatMapSlice(ctx, aboveThreshold,
		func(r db.Reading) []SensorAlert { return []SensorAlert{buildAlert(r)} })

	// ── Bridge 3: mqtt.DrainPublish ───────────────────────────────────────
	//
	// DrainPublish replaces the manual Drain + adaptermqtt.Publish loop.
	// It handles encoding, variable substitution, observer calls, and error
	// forwarding — in one call.
	//
	//   Before (manual):
	//     gstream.Drain(ctx, alertPayloads, func(ctx, alert) error {
	//         return adaptermqtt.Publish(ctx, client, handle, ...)
	//     }, logErr, drainOpts)
	//
	//   After (bridge helper):
	//     mqtt.DrainPublish(ctx, client, handle, alertPayloads, fmt, opts)
	//
	pipelineDone := make(chan struct{})
	go func() {
		defer close(pipelineDone)
		adaptermqtt.DrainPublish(ctx, mqttClient, alertHandle, alertPayloads,
			format.JSON(alertCodec),
			adaptermqtt.MQTTDrainPublishOptions{
				Vars: nil, // topic vars resolved per-item below — alertHandle.Topic uses {sensorID}
				OnError: func(err error) {
					var sae gstream.StreamApplyError
					var sde gstream.StreamDecodeError
					switch {
					case errors.As(err, &sae):
						logger.Warn("stream apply error", "error", sae)
					case errors.As(err, &sde):
						logger.Warn("stream decode error", "error", sde)
					default:
						logger.Warn("alert publish error", "error", err)
					}
				},
				Observer: obs,
			})
	}()

	fmt.Println("✓ Stream pipeline active: MQTT → decode → save → tee → filter → alert")

	// ── HTTP — register remaining routes ──────────────────────────────────
	// HandlerLatest for GET /readings/latest was already registered above.
	// Register the other routes here (after the stream pipeline is wired).
	nethttp.Register(mux, createRoute.ClientHandle(), makeCreateHandler(store, obs), nethttp.Options{Observer: obs})
	nethttp.Register(mux, getRoute.ClientHandle(), makeGetHandler(store, obs), nethttp.Options{Observer: obs})

	srv := httptest.NewServer(mux)
	defer srv.Close()
	fmt.Printf("✓ HTTP server started: %s\n\n", srv.URL)

	// ── Demo: MQTT sensor messages ─────────────────────────────────────────
	fmt.Println("── MQTT sensor events ───────────────────────────────────")

	sensorA := "550e8400-e29b-41d4-a716-446655440001"
	sensorB := "550e8400-e29b-41d4-a716-446655440002"

	// Normal reading (below threshold — no alert).
	normalPayload, _ := json.Marshal(map[string]any{
		"sensor_id": sensorA,
		"value":     23.5,
		"unit":      "C",
	})
	mqttClient.deliver("sensors/"+sensorA+"/data", normalPayload)
	fmt.Printf("  → sensor %s: 23.5 C  (below threshold, no alert)\n", sensorA[:8])

	// High reading (above threshold → alert published).
	highPayload, _ := json.Marshal(map[string]any{
		"sensor_id": sensorB,
		"value":     87.3,
		"unit":      "C",
	})
	mqttClient.deliver("sensors/"+sensorB+"/data", highPayload)
	fmt.Printf("  → sensor %s: 87.3 C  (above %.0f° threshold — alert published)\n\n",
		sensorB[:8], alertThreshold)

	// Give the async goroutines time to process both MQTT messages, then
	// cancel the pipeline context to drain and shut down all stream operators.
	// The 100ms sleep ensures the pipeline goroutines have read from the
	// buffered rawCh, decoded both payloads, saved to DB, and updated the
	// HandlerLatest atomic pointer.
	time.Sleep(100 * time.Millisecond)
	cancelPipeline()
	<-pipelineDone // wait for DrainPublish goroutine to finish

	// ── Demo: HTTP requests ────────────────────────────────────────────────
	fmt.Println("── HTTP requests ────────────────────────────────────────")

	sensorC := "550e8400-e29b-41d4-a716-446655440003"

	// POST /readings — valid.
	postBody, _ := json.Marshal(map[string]any{
		"sensor_id": sensorC,
		"value":     31.2,
		"unit":      "C",
	})
	resp, err := http.Post(srv.URL+"/readings", "application/json", bytes.NewReader(postBody))
	must(err, "POST /readings")
	var created db.Reading
	_ = json.NewDecoder(resp.Body).Decode(&created)
	_ = resp.Body.Close()
	fmt.Printf("  POST /readings         → %d  id=%s\n", resp.StatusCode, created.ID)

	// POST /readings — invalid unit (codec rejects before DB).
	badBody, _ := json.Marshal(map[string]any{
		"sensor_id": sensorC,
		"value":     10.0,
		"unit":      "xyz", // not in OneOf list
	})
	resp2, err := http.Post(srv.URL+"/readings", "application/json", bytes.NewReader(badBody))
	must(err, "POST /readings bad")
	_ = resp2.Body.Close()
	fmt.Printf("  POST /readings (bad unit) → %d  (codec rejected before DB)\n", resp2.StatusCode)

	// GET /readings/{id}.
	resp3, err := http.Get(srv.URL + "/readings/" + created.ID)
	must(err, "GET /readings")
	var fetched db.Reading
	_ = json.NewDecoder(resp3.Body).Decode(&fetched)
	_ = resp3.Body.Close()
	fmt.Printf("  GET /readings/%s → %d  value=%.1f %s\n\n",
		created.ID, resp3.StatusCode, fetched.Value, fetched.Unit)

	// ── Bridge 2 demo: nethttp.HandlerLatest ───────────────────────────────
	//
	// GET /readings/latest returns the most recently saved reading from the
	// stream pipeline — no DB query per request. After cancelPipeline() and
	// <-pipelineDone, the Tee goroutine has closed latestReadings and the
	// HandlerLatest background goroutine has processed all values. The atomic
	// pointer still holds the last reading (87.3 C from sensorB).
	fmt.Println("── Bridge demo: GET /readings/latest (HandlerLatest) ────")
	resp4, err := http.Get(srv.URL + "/readings/latest")
	must(err, "GET /readings/latest")
	// Decode into map: codec encodes fields as "sensor_id" / "value" / "unit",
	// but db.Reading has no JSON struct tags so direct struct decode misses them.
	var latestMap map[string]any
	_ = json.NewDecoder(resp4.Body).Decode(&latestMap)
	_ = resp4.Body.Close()
	if resp4.StatusCode == http.StatusOK && latestMap["sensor_id"] != "" {
		sensorID, _ := latestMap["sensor_id"].(string)
		if len(sensorID) > 8 {
			sensorID = sensorID[:8]
		}
		fmt.Printf("  GET /readings/latest   → %d  sensor=%s value=%v %v\n",
			resp4.StatusCode, sensorID, latestMap["value"], latestMap["unit"])
		fmt.Println("  (served from atomic pointer — zero DB queries)")
	} else {
		fmt.Printf("  GET /readings/latest   → %d  (no value yet)\n", resp4.StatusCode)
	}

	// ── Bridge 4 demo: sql.QueryStream ────────────────────────────────────
	//
	// sql.QueryStream polls a query function at a fixed interval and emits
	// each returned row as a typed stream item. Each row is validated through
	// the codec; validation failures go to Stream.Errors as RowValidationError.
	//
	// Here we poll the readings table once (100ms interval with 50ms timeout)
	// and collect all currently stored rows.
	fmt.Println("\n── Bridge demo: sql.QueryStream ─────────────────────────")
	queryCtx, cancelQuery := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancelQuery()

	rowStream := sqladapter.QueryStream(queryCtx, readingCodec,
		func(qctx context.Context) ([]db.Reading, error) {
			return store.queries.ListReadings(qctx)
		},
		100*time.Millisecond,
		sqladapter.QueryStreamOptions{
			Table:    "readings",
			Op:       "list_readings",
			Observer: obs,
		})

	allRows, streamErrs := gstream.Collect(queryCtx, rowStream)
	fmt.Printf("  QueryStream polled %d row(s) from DB", len(allRows))
	if len(streamErrs) > 0 {
		fmt.Printf(", %d validation error(s)", len(streamErrs))
	}
	fmt.Println()
	for _, r := range allRows {
		fmt.Printf("  row: sensor=%s  value=%.1f %s\n", r.SensorID[:8], r.Value, r.Unit)
	}
	fmt.Println()

	// ── Observer summary ───────────────────────────────────────────────────
	counting.Print()

	// ── Stream topology documentation ──────────────────────────────────────
	topo := gstream.NewTopology("Sensor Service MQTT Pipeline", "1.0.0").
		WithDescription("Real-time sensor readings: decode → save → filter → alert.").
		WithSource("mqtt/sensors/+/data", "Raw MQTT payloads from sensor network")
	gstream.WithApply(topo, saveReadingFn)
	topo.WithFilter(fmt.Sprintf("value > %.0f (alert threshold)", alertThreshold)).
		WithSink("mqtt/alerts/{sensorID}", "Low-performance alert events")

	fmt.Println("\n── Stream topology (stream.Topology) ────────────────────")
	spec := topo.Spec()
	for _, step := range spec.Steps {
		if step.Function != nil {
			fmt.Printf("  [%s] %s v%s  hash:%s...\n",
				step.Kind, step.Function.Name, step.Function.Version, step.Function.Hash[:16])
		} else {
			name := step.Name
			if name == "" {
				name = step.Description
			}
			fmt.Printf("  [%s] %s\n", step.Kind, name)
		}
	}

	// ── Demonstrate errors.As chain ────────────────────────────────────────
	_, validateErr := sqladapter.Validate(insertParamsCodec,
		db.InsertReadingParams{
			ID: "not-a-uuid", SensorID: "also-bad",
			Value: 0, Unit: "xyz", RecordedAt: "not-a-date",
		},
		sqladapter.ValidateOptions{Table: "readings", Op: "demo_error"},
	)
	var rve sqladapter.RowValidationError
	if errors.As(validateErr, &rve) {
		fmt.Printf("✓ errors.As → RowValidationError{table:%q, op:%q}\n", rve.Table, rve.Op)
	}
	var ve codex.ValidationErrors
	if errors.As(validateErr, &ve) {
		fmt.Printf("  inner ValidationErrors: %d field(s) failed\n", len(ve))
	}
}

func must(err error, msg string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL %s: %v\n", msg, err)
		os.Exit(1)
	}
}
