package vectorstore

import (
	"context"

	"github.com/kubercloud/ani/pkg/ports"
)

type NotConfigured struct{}

var _ ports.VectorStore = NotConfigured{}

func (NotConfigured) Health(context.Context) error {
	return ports.ErrNotConfigured
}

func (NotConfigured) EnsureCollection(context.Context, ports.VectorCollectionRef, int) error {
	return ports.ErrNotConfigured
}

func (NotConfigured) Upsert(context.Context, ports.VectorCollectionRef, []ports.VectorRecord) error {
	return ports.ErrNotConfigured
}

func (NotConfigured) Search(context.Context, ports.VectorSearchQuery) ([]ports.VectorSearchResult, error) {
	return nil, ports.ErrNotConfigured
}

func (NotConfigured) Delete(context.Context, ports.VectorCollectionRef, []string) error {
	return ports.ErrNotConfigured
}

func (NotConfigured) DeleteByExpr(context.Context, ports.VectorCollectionRef, string) (int, error) {
	return 0, ports.ErrNotConfigured
}

func (NotConfigured) CollectionHealth(context.Context, ports.VectorCollectionRef) (ports.VectorCollectionHealth, error) {
	return ports.VectorCollectionHealth{}, ports.ErrNotConfigured
}
