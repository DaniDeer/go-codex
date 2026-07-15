package adapters

import (
	"context"

	nethttp "github.com/DaniDeer/go-codex/adapters/nethttp"
	"github.com/DaniDeer/go-codex/examples/sensor-service/db"
	"github.com/DaniDeer/go-codex/examples/sensor-service/domain"
)

// NewCreateHandler returns the POST /readings handler: map request → insert
// params (domain rule), save, return the stored row.
func NewCreateHandler(store *ReadingStore) nethttp.HandlerFunc[domain.CreateReadingReq, db.Reading] {
	return func(ctx context.Context, req domain.CreateReadingReq) (db.Reading, error) {
		params := domain.BuildInsertParams(req)
		if err := store.Save(ctx, params); err != nil {
			return db.Reading{}, err
		}
		return store.Get(ctx, params.ID)
	}
}

// NewGetHandler returns the GET /readings/{id} handler.
func NewGetHandler(store *ReadingStore) nethttp.HandlerFunc[struct{}, db.Reading] {
	return func(ctx context.Context, _ struct{}) (db.Reading, error) {
		r, _ := nethttp.RequestFromContext(ctx)
		id := r.PathValue("id")
		return store.Get(ctx, id)
	}
}
