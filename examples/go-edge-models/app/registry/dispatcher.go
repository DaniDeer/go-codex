package registry

import (
	"context"
	"net/http"

	mcpgo "github.com/DaniDeer/go-codex/adapters/mcpgo"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/app/registry/dockerhub"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/app/registry/ghcr"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/app/registry/mcr"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker"
	regmodels "github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker/registry"
	dockerhubmodels "github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker/registry/dockerhub"
	ghcrmodels "github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker/registry/ghcr"
	mcrmodels "github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker/registry/mcr"
)

// This file holds Dispatcher — THE recommended, registry-agnostic entry
// point for a caller who only has an image URL and doesn't want to deal
// with per-registry host awareness at all. It bundles EVERY
// registry-client operation (GetTags/GetTagsFiltered/GetImageMetadata,
// plus the MCP tool-handler constructors NewGetTagsToolHandler/
// NewGetImageMetadataToolHandler) into ONE constructed object that picks
// the right CONCRETE per-registry client (dockerhub/ghcr/mcr — built
// once, held internally) per call, based on the image URL's resolved
// registry host — falling back to NewGenericClient for any OTHER
// OCI-compliant registry.
//
// Dispatcher safely imports dockerhub/ghcr/mcr (each FULLY
// SELF-CONTAINED, importing neither this package nor each other) with NO
// import cycle — this is the whole reason those 3 siblings were made
// self-contained rather than embedding a shared app/registry.Client the
// way an earlier round of this codebase did (that shape would have
// required THIS file to live in a separate package one level above all
// 4, the way app/registry/dispatch used to; folding it in here instead
// is only possible because the dependency now only ever goes ONE way).

// DispatcherOption configures [NewDispatcher]. Per-registry TYPED
// credentials (dockerhub.Credentials/ghcr.Credentials/mcr.Credentials are
// now 3 DISTINCT Go types — see dockerhub.Credentials' own doc comment
// for why) mean a single generic option set can no longer configure all
// 3 self-contained clients uniformly the way one []registry.Option used
// to — WithDockerHubOptions/WithGHCROptions/WithMCROptions/
// WithGenericOptions each configure ONE of the 4 clients Dispatcher
// builds (the 3 known ones, plus the generic-fallback path for any OTHER
// registry).
type DispatcherOption func(*dispatcherOptions)

type dispatcherOptions struct {
	dockerhubOpts []dockerhub.Option
	ghcrOpts      []ghcr.Option
	mcrOpts       []mcr.Option
	genericOpts   []Option
}

func resolveDispatcherOptions(opts []DispatcherOption) dispatcherOptions {
	var o dispatcherOptions
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// WithDockerHubOptions passes opts (e.g. dockerhub.WithCredentials) to
// the Dispatcher's own internally-built dockerhub.Client.
func WithDockerHubOptions(opts ...dockerhub.Option) DispatcherOption {
	return func(o *dispatcherOptions) { o.dockerhubOpts = opts }
}

// WithGHCROptions passes opts (e.g. ghcr.WithCredentials) to the
// Dispatcher's own internally-built ghcr.Client.
func WithGHCROptions(opts ...ghcr.Option) DispatcherOption {
	return func(o *dispatcherOptions) { o.ghcrOpts = opts }
}

// WithMCROptions passes opts (e.g. mcr.WithCredentials) to the
// Dispatcher's own internally-built mcr.Client.
func WithMCROptions(opts ...mcr.Option) DispatcherOption {
	return func(o *dispatcherOptions) { o.mcrOpts = opts }
}

// WithGenericOptions passes opts (e.g. WithCredentials/
// WithCredentialsByRegistry) to every [NewGenericClient] the Dispatcher
// builds on demand for a registry host OTHER than the 3 known ones.
func WithGenericOptions(opts ...Option) DispatcherOption {
	return func(o *dispatcherOptions) { o.genericOpts = opts }
}

// Dispatcher bundles every registry-client operation behind ONE
// constructed object, holding the 3 concrete per-registry clients
// (dockerhub/ghcr/mcr) UNDER THE HOOD.
type Dispatcher struct {
	httpClient  *http.Client
	genericOpts []Option
	byHost      map[string]Client
}

// NewDispatcher constructs the 3 concrete per-registry clients
// (dockerhub.New/ghcr.New/mcr.New) ONCE, eagerly — each is cheap to
// build, so doing so up front (rather than lazily per call) is simple and
// costs nothing observable.
func NewDispatcher(httpClient *http.Client, opts ...DispatcherOption) *Dispatcher {
	o := resolveDispatcherOptions(opts)
	return &Dispatcher{
		httpClient:  httpClient,
		genericOpts: o.genericOpts,
		byHost: map[string]Client{
			dockerhubmodels.RegistryHost: dockerhub.New(httpClient, o.dockerhubOpts...),
			ghcrmodels.RegistryHost:      ghcr.New(httpClient, o.ghcrOpts...),
			mcrmodels.RegistryHost:       mcr.New(httpClient, o.mcrOpts...),
		},
	}
}

// resolve parses imageURL and returns the Client to dispatch to — a
// pre-built concrete one for a known host, or a freshly-built
// NewGenericClient for any OTHER OCI-compliant registry host (preserves
// this module's "works with ANY OCI-compliant registry" guarantee) —
// alongside the parsed regmodels.ImageRef (Repository/Reference) the
// caller's own operation needs.
func (d *Dispatcher) resolve(imageURL string) (Client, regmodels.ImageRef, error) {
	ref, err := regmodels.ParseImageRef(imageURL)
	if err != nil {
		return nil, regmodels.ImageRef{}, err
	}
	if client, ok := d.byHost[ref.Registry]; ok {
		return client, ref, nil
	}
	return NewGenericClient(d.httpClient, ref.Registry, d.genericOpts...), ref, nil
}

// GetTags lists every tag for imageURL's repository, dispatching to the
// concrete per-registry Client imageURL's registry host resolves to.
func (d *Dispatcher) GetTags(ctx context.Context, imageURL string) (regmodels.TagsList, error) {
	client, ref, err := d.resolve(imageURL)
	if err != nil {
		return regmodels.TagsList{}, err
	}
	return client.GetTags(ctx, ref.Repository)
}

// GetTagsFiltered calls GetTags, then sorts/limits the result's Tags via
// docker.FilterTags(list.Tags, filterOpts...) — no new HTTP call, no new
// wire type, now dispatched per-registry.
func (d *Dispatcher) GetTagsFiltered(ctx context.Context, imageURL string, filterOpts []docker.FilterTagsOpt) (regmodels.TagsList, error) {
	list, err := d.GetTags(ctx, imageURL)
	if err != nil {
		return regmodels.TagsList{}, err
	}
	list.Tags = docker.FilterTags(list.Tags, filterOpts...)
	return list, nil
}

// GetImageMetadata fetches lean manifest metadata for req.ImageURL,
// dispatching to the concrete per-registry Client req.ImageURL's
// registry host resolves to.
func (d *Dispatcher) GetImageMetadata(ctx context.Context, req regmodels.GetImageMetadataReq) (regmodels.ManifestMetadata, error) {
	client, ref, err := d.resolve(req.ImageURL)
	if err != nil {
		return regmodels.ManifestMetadata{}, err
	}
	return client.GetImageMetadata(ctx, ref.Repository, ref.Reference, req.Platform)
}

// NewGetTagsToolHandler returns an mcpgo.HandlerFunc that calls
// d.GetTagsFiltered for every tool invocation.
func (d *Dispatcher) NewGetTagsToolHandler() mcpgo.HandlerFunc[regmodels.GetTagsToolReq, regmodels.TagsList] {
	return func(ctx context.Context, req regmodels.GetTagsToolReq) (regmodels.TagsList, error) {
		return d.GetTagsFiltered(ctx, req.ImageURL, FilterOptsFor(req))
	}
}

// NewGetImageMetadataToolHandler returns an mcpgo.HandlerFunc that calls
// d.GetImageMetadata for every tool invocation.
func (d *Dispatcher) NewGetImageMetadataToolHandler() mcpgo.HandlerFunc[regmodels.GetImageMetadataReq, regmodels.ManifestMetadata] {
	return func(ctx context.Context, req regmodels.GetImageMetadataReq) (regmodels.ManifestMetadata, error) {
		return d.GetImageMetadata(ctx, req)
	}
}
