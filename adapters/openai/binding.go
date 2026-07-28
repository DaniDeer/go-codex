package openai

import (
	"context"
	"net/http"

	"github.com/DaniDeer/go-codex/api/llm"
	"github.com/DaniDeer/go-codex/ports"
	gstream "github.com/DaniDeer/go-codex/stream"
)

// CallAdapter returns a [ports.IOAdapter][Req,Resp] that fulfills the port's
// Connect/Call by completing a Chat Completions request against an
// OpenAI-compatible endpoint. Use with [ports.IOPort.Bind]:
//
//	handle, _ := domain.Summarize.PluginLLMPattern(domain.SummarizePattern)
//	domain.Summarize.Bind(ctx, openai.CallAdapter(httpClient, handle, openai.CallAdapterOptions{
//	    Model: "gpt-4o-mini", APIKey: os.Getenv("OPENAI_API_KEY"),
//	}))
//
// Every request is validated at the API level via
// [llm.CallHandle.ResponseSchema] (OpenAI-style "strict structured
// outputs") AND re-validated locally through
// [llm.CallHandle.DecodeResponse] — belt-and-suspenders: the JSON Schema
// constrains the shape at generation time, [codex.Codec.Refine] catches what
// a bare schema cannot express (cross-field invariants, custom constraints).
// [CallAdapterOptions.MaxRetries] bounds a re-prompt loop for the latter case.
func CallAdapter[Req, Resp any](
	client *http.Client,
	handle *llm.CallHandle[Req, Resp],
	opts CallAdapterOptions,
) ports.IOAdapter[Req, Resp] {
	return &callAdapter[Req, Resp]{client: client, handle: handle, opts: opts}
}

type callAdapter[Req, Resp any] struct {
	client *http.Client
	handle *llm.CallHandle[Req, Resp]
	opts   CallAdapterOptions
}

func (a *callAdapter[Req, Resp]) AdapterName() string { return "openai.CallAdapter" }

func (a *callAdapter[Req, Resp]) Transform(ctx context.Context, src gstream.Stream[Req]) gstream.Stream[Resp] {
	values := make(chan Resp, cap(src.Values)+1)
	errs := make(chan error, cap(src.Errors)+1)

	go func() {
		defer close(values)
		defer close(errs)
		valCh := src.Values
		errCh := src.Errors
		for valCh != nil || errCh != nil {
			select {
			case <-ctx.Done():
				return
			case req, ok := <-valCh:
				if !ok {
					valCh = nil
					continue
				}
				resp, err := complete[Req, Resp](ctx, a.client, a.handle, req, a.opts)
				if err != nil {
					select {
					case errs <- err:
					case <-ctx.Done():
						return
					}
					continue
				}
				select {
				case values <- resp:
				case <-ctx.Done():
					return
				}
			case e, ok := <-errCh:
				if !ok {
					errCh = nil
					continue
				}
				select {
				case errs <- e:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return gstream.Stream[Resp]{Values: values, Errors: errs}
}
