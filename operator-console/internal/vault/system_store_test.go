package vault_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/stephan271/smallworlds/operator-console/internal/vault"
	keyring "github.com/zalando/go-keyring"
)

func TestSystemWrappingStoreRoundTrip(t *testing.T) {
	keyring.MockInit()
	store := vault.NewSystemWrappingStore()
	ctx := context.Background()
	if !store.Available(ctx) {
		t.Fatal("mocked system credential store is not reported as available")
	}
	if _, found, err := store.Load(ctx); err != nil || found {
		t.Fatalf("empty system store load = found %t, error %v; want not found without error", found, err)
	}
	want := []byte("random wrapping key bytes")
	if err := store.Save(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, found, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !found || !bytes.Equal(got, want) {
		t.Fatalf("system store load = %q, found %t; want saved wrapping key", got, found)
	}
}
