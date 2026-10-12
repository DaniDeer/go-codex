package registry

import (
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker"
	regmodels "github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker/registry"
)

// FilterOptsFor maps a GetTagsToolReq's wire-friendly Limit/Sort fields to
// docker.FilterTagsOpt values — Sort decodes via regmodels.SortModeCodec,
// the SAME codec GetTagsToolReqCodec's own "sort" field validates
// against, so there is exactly ONE place the 3 allowed values (plus the
// empty-string "absent means default" exception) are spelled, rather
// than this function hand-duplicating them in its own switch. A decode
// error falls back to docker.SortByVersionDesc rather than propagating —
// GetTagsToolReqCodec's own validation already guarantees Sort is either
// "" or one of regmodels' allowed values by the time a handler ever sees
// it, so this is purely a defensive fallback for a caller that built a
// GetTagsToolReq by hand, bypassing that codec. EXPORTED so this
// package's own Dispatcher.NewGetTagsToolHandler (dispatcher.go) reuses
// this SAME mapping.
func FilterOptsFor(req regmodels.GetTagsToolReq) []docker.FilterTagsOpt {
	mode, err := regmodels.SortModeCodec.Decode(req.Sort)
	if err != nil {
		mode = docker.SortByVersionDesc
	}
	return []docker.FilterTagsOpt{docker.WithLimit(req.Limit), docker.WithSort(mode)}
}
