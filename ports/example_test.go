package ports_test

import (
	"context"
	"fmt"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/ports"
	gstream "github.com/DaniDeer/go-codex/stream"
)

// ExampleNewSourcePort shows the inside-out workflow: the port declares its
// communication pattern once (topic + params); main() derives the handle and
// binds a transport adapter — here a channel-backed test adapter.
func ExampleNewSourcePort() {
	ctx := context.Background()

	// domain/pipeline.go — zero adapter imports; the Pattern IS the declaration.
	sensors := codex.Must(ports.NewSourcePort[int]("sensors", codex.Int(),
		ports.PortOptions{
			Buffer: 4,
			Patterns: []ports.Pattern{
				ports.EventPattern{Topic: "sensors/{sensorID}/data", Opts: []events.ChannelOpt{
					events.TopicParam{Name: "sensorID"},
				}},
			},
		}))

	// main.go — the handle comes FROM the port; swap the adapter freely.
	handle, _ := ports.EventHandle[int](sensors)
	fmt.Println("declared topic:", handle.Topic)

	ch := make(chan int, 2)
	ch <- 21
	ch <- 42
	close(ch)
	sensors.Bind(ctx, ports.ChanSourceAdapter(ch)) // test adapter — no broker needed

	vals, _ := gstream.Collect(ctx, sensors.Stream(ctx))
	fmt.Println("received:", vals)
	// Output:
	// declared topic: sensors/{sensorID}/data
	// received: [21 42]
}

// ExampleSinkPort_Push shows the request-fed sink lifecycle: Start owns the
// drain goroutine, Push submits items from anywhere (e.g. a request
// handler), Close waits for the adapter to finish draining.
func ExampleSinkPort_Push() {
	ctx := context.Background()

	exports := codex.Must(ports.NewSinkPort[string]("exports", codex.String(),
		ports.PortOptions{Buffer: 4}))

	out := make(chan string, 4)
	exports.Bind(ctx, ports.ChanSinkAdapter(out))

	exports.Start(ctx)
	_ = exports.Push(ctx, "snapshot-1")
	_ = exports.Push(ctx, "snapshot-2")
	_ = exports.Close() // waits for in-flight Push + adapter drain

	close(out)
	for v := range out {
		fmt.Println("written:", v)
	}
	// Pushing after Close returns a structured error.
	err := exports.Push(ctx, "late")
	fmt.Println("after close:", err != nil)
	// Output:
	// written: snapshot-1
	// written: snapshot-2
	// after close: true
}

// ExampleNewLatestPort shows the reactive-cache port: Feed drains a stream
// into the port's atomic cell; the cache outlives the stream, and bound
// adapters (HTTP GET, ZeroMQ REP, MCP tool) answer every request from it.
func ExampleNewLatestPort() {
	ctx := context.Background()

	latest := codex.Must(ports.NewLatestPort[float64]("latest-reading", codex.Float64(),
		ports.PortOptions{}))

	if _, ok := latest.Latest(); !ok {
		fmt.Println("empty before first value")
	}

	src := make(chan float64, 2)
	src <- 23.5
	src <- 87.3
	close(src)
	latest.Feed(ctx, gstream.From(ctx, src)) // returns when src terminates

	// The cache outlives the stream.
	v, ok := latest.Latest()
	fmt.Println("cached:", v, ok)
	// Output:
	// empty before first value
	// cached: 87.3 true
}
