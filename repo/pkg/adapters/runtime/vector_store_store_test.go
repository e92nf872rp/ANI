package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestMetadataVectorStoreStoreKnowledgeBaseLinkUsesInjectedClock(t *testing.T) {
	want := time.Unix(1234, 0).UTC()
	tx := &fakeMetadataTx{}
	store := NewMetadataVectorStoreStore(
		fakeMetadataStore{tx: tx},
		WithVectorStoreStoreClock(func() time.Time { return want }),
	)
	err := store.SetKnowledgeBaseLink(
		context.Background(),
		"11111111-1111-1111-1111-111111111111",
		"vector-a",
		ports.VectorStoreKnowledgeBaseRef{ID: "kb-a", Name: "KB A", Source: "manual"},
	)
	if err != nil {
		t.Fatalf("SetKnowledgeBaseLink() error = %v", err)
	}
	if len(tx.args) < 6 || tx.args[5] != want {
		t.Fatalf("last Exec args = %#v, want injected timestamp %s", tx.args, want)
	}
}
