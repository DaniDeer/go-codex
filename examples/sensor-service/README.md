# sensor-service

The go-codex flagship example: a small but complete sensor-readings service,
structured like a real project. It tells **one coherent story** and shows how
protocol-agnostic **ports** make every IO boundary of a pipeline composable —
the pipeline code never imports an adapter; swapping MQTT → ZeroMQ or
SQL → HTTP changes only `main.go`.

## The use case

1. **Ingest** — sensors publish readings over MQTT.
2. **Persist** — every reading is validated and written to the database.
3. **Alert** — readings above a threshold (configured via env var) publish an
   MQTT alert.
4. **Query** — the time series of one sensor is served over REST, queried
   from the database.
5. **Export** — a REST call triggers an export of all readings, written to a
   typed JSON file.

## Data flow — every hop is a port

```
                    MQTT sensors/{sensorID}/data
                                │
                    ┌───────────▼───────────┐
                    │ Sensors  SourcePort   │  EventPattern (topic + params)
                    └───────────┬───────────┘
                                │ Stream[MQTTPayload]
                     Apply(buildInsertParams)      ← pure forge function
                                │ Stream[InsertReadingParams]
                    ┌───────────▼───────────┐
                    │ Readings  IOPort      │  SQLPattern{readings, insert_reading}
                    └───────────┬───────────┘      → sql.QueryEachAdapter
                                │ Stream[db.Reading]   (validated, stored rows)
                       Tap(log) │ Tee ──────────────────► GET /readings/latest
                                │                         (reactive cache)
                     Filter(value > $APP_ALERT_THRESHOLD)
                                │ FlatMap(buildAlert)
                    ┌───────────▼───────────┐
                    │ Alerts   SinkPort     │  EventPattern
                    └───────────┬───────────┘      → mqtt.PublishAdapter
                                ▼
                    MQTT alerts/{sensorID}

  GET /sensors/{sensorID}/readings          POST /export
  ┌────────────────────────┐                ┌────────────────────────┐
  │ HistoryTool  ToolPort  │ RESTPattern    │ ExportTool  ToolPort   │ RESTPattern
  └───────────┬────────────┘                └───────────┬────────────┘
              │ Single(SensorQuery)                     │ Single(ExportRequest)
  ┌───────────▼────────────┐                ┌───────────▼────────────┐
  │ History  IOPort        │ SQLPattern     │ ExportQuery  IOPort    │ SQLPattern
  └───────────┬────────────┘                └───────────┬────────────┘
              │ Stream[TimeSeries]                      │ Stream[ExportSnapshot]
              ▼                                Tap ─────┼──► Exports SinkPort
        200 TimeSeries                                  │    FilePattern
                                       Apply(buildExportResult)  {exportID}.json
                                                        ▼         → file adapter
                                          201 ExportResult{File, Count}
```

## Package layout

| Package          | Responsibility | Imports (internal) |
|------------------|----------------|--------------------|
| `domain/`        | Models, codecs, field factories, topic constraint, pure business rules (`BuildInsertParamsFromMQTT`, `NewShouldAlert`, `NewExportSnapshot`, …) | `db` |
| `pipeline/`      | Business logic: pure forge functions + stream topology. Persistence and queries go through ports passed in as dependencies | `domain`, `db` |
| `ioports/`       | The service's complete IO surface: every port and route, declared once with its `Pattern` (`EventPattern`/`SQLPattern`/`FilePattern`/`RESTPattern`) + the shared `EventsBuilder`/`RESTBuilder` | `domain`, `db` |
| `observability/` | Cross-cutting `CountingObserver` — one instance, fanned out with a `LoggingObserver`, stored once in the context | — |
| `adapters/`      | Infrastructure edge: mock MQTT client, SQL `ReadingStore`, HTTP handler factories | `domain`, `db` |
| `db/`            | sqlc-generated queries + goose migrations | — |
| `main.go`        | **Wiring only**: `app.New` owns the root context (observer pre-injected) and the LIFO teardown (`OnShutdown` hooks for the exports port and HTTP server); config, DB, adapter binds | everything above |
| `demo.go`        | The runnable demo scenario (drives the wired service, prints the specs) | everything above |

Import direction is strictly acyclic; `domain` imports nothing internal but `db`.

## Ports declared in `ioports/`

| Port | Type | Pattern | Bound adapter (main.go) |
|------|------|---------|-------------------------|
| `Sensors` | `SourcePort[MQTTPayload]` | `EventPattern` `sensors/{sensorID}/data` | `mqtt.SubscribeAdapter` |
| `Readings` | `IOPort[InsertReadingParams, Reading]` | `SQLPattern{readings, insert_reading}` | `sql.QueryEachAdapter` |
| `Alerts` | `SinkPort[SensorAlert]` | `EventPattern` `alerts/{sensorID}` | `mqtt.PublishAdapter` |
| `History` | `IOPort[SensorQuery, TimeSeries]` | `SQLPattern{readings, list_by_sensor}` | `sql.QueryEachAdapter` |
| `ExportQuery` | `IOPort[ExportRequest, ExportSnapshot]` | `SQLPattern{readings, list_readings}` | `sql.QueryEachAdapter` |
| `NewExportsPort(dir)` | `SinkPort[ExportSnapshot]` | `FilePattern` `{exportID}.json` | `file.DrainWriteFileAdapter` (request-fed via `Start`/`Push`/`Close`) |
| `HistoryTool` | `ToolPort[struct{}, TimeSeries]` | `RESTPattern` `GET /sensors/{sensorID}/readings` | `nethttp.PipelineAdapter` |
| `ExportTool` | `ToolPort[ExportRequest, ExportResult]` | `RESTPattern` `POST /export` | `nethttp.PipelineAdapter` |
| `Latest` | `LatestPort[db.Reading]` | `RESTPattern` `GET /readings/latest` | `nethttp.LatestAdapter` |

Alongside the ports, `ioports` declares the three classic REST routes and
registers them against the same shared builder at declaration time:

| Route | Handle | Endpoint | Wired via (main.go) |
|-------|--------|----------|---------------------|
| `CreateRoute` | `CreateHandle` | `POST /readings` | `nethttp.Register` + handler factory |
| `GetRoute` | `GetHandle` | `GET /readings/{id}` | `nethttp.Register` + handler factory |

## Spec generation — the declarations are the spec

The `ioports` declarations are the single source of truth; the demo renders
three artifacts from them, without any separate spec-authoring step:

- **AsyncAPI** (`EventsBuilder.AsyncAPISpec()`) — both event ports appear as
  channels + receive/send operations with full payload schemas and topic
  parameters. Each `EventPattern` was registered internally by its port's
  constructor against the shared `EventsBuilder` (which also enforces the
  topic-format constraints at construction time — an invalid topic fails
  port construction, not spec rendering).
- **OpenAPI** (`RESTBuilder.OpenAPISpec()`) — covers **all five** HTTP
  endpoints with request/response schemas: the two `RESTPattern`-based tool
  ports register internally at port construction, and the three classic
  routes register explicitly next to their declarations
  (`CreateHandle = codex.Must(CreateRoute.Register(RESTBuilder))`).
  Header fields declared with codecs ride along: `ExportTool`'s
  `rest.HeaderParam{Name: "X-Api-Key", Required: true}.WithCodec(domain.APIKeyCodec)`
  is enforced by the adapter *before* the pipeline runs (400 +
  `rest.HeaderParamError`, observer location `"header"`) **and** appears in
  the spec as an `in: header` parameter — one declaration, both behaviors.
- **Stream topology** (`pipeline.Topology(...).Spec()`) — the MQTT pipeline
  shape as a machine-readable spec, including the pure forge functions'
  governance metadata (name, version, content hash).

The same declare-once principle covers the file boundary: the export
response's file path comes from the *same* `FilePattern` declaration that
writes the file (`FileHandle.BuildPath`).

## HTTP endpoints

| Endpoint | Purpose |
|----------|---------|
| `POST /readings` | Create a reading (codec-validated before the DB) |
| `GET /readings/{id}` | Fetch one reading |
| `GET /readings/latest` | Most recent reading — served from the `Latest` cache port's atomic cell, zero DB queries |
| `GET /sensors/{sensorID}/readings` | Time series of one sensor, queried from the DB through the `History` port |
| `POST /export` | Export all readings to a typed JSON file through the `Exports` port. Requires the codec-validated `X-Api-Key` header (`sk-` prefix, `domain.APIKeyCodec`) — missing or malformed keys get 400 before the pipeline runs |

## Configuration

| Env var | Default | Contract |
|---------|---------|----------|
| `APP_ALERT_THRESHOLD` | `50.0` | `domain.AlertConfigCodec` — `float64`, `MinFloat(0)`; loaded once in `main()` via `format.FromEnv`, pipeline functions close over the typed config |

## Run

```sh
go run ./examples/sensor-service

# raise the alert threshold — the 87.3 °C reading no longer alerts:
APP_ALERT_THRESHOLD=90 go run ./examples/sensor-service
```

The demo runs the full story in-process (mock MQTT client, in-memory SQLite,
httptest server) and prints each scene, the observer summary, the stream
topology, and the AsyncAPI/OpenAPI specs. Lifecycle is managed by
[`app.New`](../../docs/features/app.md): the demo ends with `a.Shutdown()`
(ordered LIFO teardown); a real service would call `a.Run(ctx)` and get
SIGINT/SIGTERM handling on the same teardown path.

After a run, the exported snapshot sits right here in
[`exports/`](exports/) (`{exportID}.json`, per the `FilePattern` declaration)
— open it to see the codec-shaped file the `Exports` port wrote. The
directory is wiped and recreated on each run and is gitignored.

## Regenerating the database layer

```sh
cd examples/sensor-service
sqlc generate   # query/readings.sql → db/
```

Migrations in `migrations/` are applied at startup via
`sqladapter.NewMigrator` (goose).
