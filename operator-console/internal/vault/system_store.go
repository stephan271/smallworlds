package vault

import (
	"context"
	"encoding/base64"
	"errors"

	keyring "github.com/zalando/go-keyring"
)

const keyringService = "smallworlds-bootstrap-launcher"
const keyringAccount = "launcher-vault-wrapping-key"
const keyringProbeAccount = "launcher-vault-capability-probe"

type systemWrappingStore struct{}

func NewSystemWrappingStore() WrappingStore {
	return systemWrappingStore{}
}

func (systemWrappingStore) Available(ctx context.Context) bool {
	if ctx.Err() != nil {
		return false
	}
	_, err := keyring.Get(keyringService, keyringProbeAccount)
	return err == nil || errors.Is(err, keyring.ErrNotFound)
}

func (systemWrappingStore) Load(ctx context.Context) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	encoded, err := keyring.Get(keyringService, keyringAccount)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, ErrCredentialStoreUnavailable
	}
	key, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, false, ErrCredentialStoreUnavailable
	}
	return key, true, nil
}

func (systemWrappingStore) Save(ctx context.Context, key []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := keyring.Set(keyringService, keyringAccount, base64.RawStdEncoding.EncodeToString(key)); err != nil {
		return ErrCredentialStoreUnavailable
	}
	return nil
}
