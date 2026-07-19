package recovery

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"filippo.io/age"
	"github.com/stephan271/smallworlds/operator-console/internal/state"
)

const Header = "SWRECOVERY/1\n"

var ErrInvalidBundle = errors.New("invalid recovery bundle")
var ErrCannotDecrypt = errors.New("cannot decrypt recovery bundle")

type Payload struct {
	Format               string                      `json:"format"`
	Version              int                         `json:"version"`
	Profile              state.Profile               `json:"profile"`
	WorkflowSnapshot     WorkflowSnapshot            `json:"workflowSnapshot"`
	InfrastructureState  json.RawMessage             `json:"infrastructureState"`
	Kubeconfig           string                      `json:"kubeconfig"`
	ClusterCA            string                      `json:"clusterCA"`
	VaultMaterial        map[string]string           `json:"vaultMaterial"`
	CredentialReferences []state.CredentialReference `json:"credentialReferences"`
}

type WorkflowSnapshot struct {
	Plans  []state.PlanRecord  `json:"plans"`
	Runs   []state.RunRecord   `json:"runs"`
	Events []state.EventRecord `json:"events"`
}

func ExportWithPassphrase(payload Payload, passphrase string) ([]byte, error) {
	recipient, err := age.NewScryptRecipient(passphrase)
	if err != nil {
		return nil, fmt.Errorf("prepare recovery encryption: %w", err)
	}
	return encrypt(payload, []age.Recipient{recipient})
}

func ExportWithRecipients(payload Payload, recipientTexts []string) ([]byte, error) {
	if len(recipientTexts) == 0 {
		return nil, fmt.Errorf("at least one recovery recipient is required")
	}
	recipients := make([]age.Recipient, 0, len(recipientTexts))
	for _, recipientText := range recipientTexts {
		recipient, err := age.ParseX25519Recipient(recipientText)
		if err != nil {
			return nil, fmt.Errorf("parse recovery recipient: %w", err)
		}
		recipients = append(recipients, recipient)
	}
	return encrypt(payload, recipients)
}

func encrypt(payload Payload, recipients []age.Recipient) ([]byte, error) {
	plaintext, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode recovery payload: %w", err)
	}
	var encrypted bytes.Buffer
	writer, err := age.Encrypt(&encrypted, recipients...)
	if err != nil {
		return nil, fmt.Errorf("encrypt recovery payload: %w", err)
	}
	if _, err := writer.Write(plaintext); err != nil {
		return nil, fmt.Errorf("write recovery payload: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("finish recovery encryption: %w", err)
	}
	return append([]byte(Header), encrypted.Bytes()...), nil
}

func OpenWithPassphrase(bundle []byte, passphrase string) (Payload, error) {
	identity, err := age.NewScryptIdentity(passphrase)
	if err != nil {
		return Payload{}, ErrCannotDecrypt
	}
	return decrypt(bundle, []age.Identity{identity})
}

func OpenWithIdentity(bundle []byte, identityText string) (Payload, error) {
	identity, err := age.ParseX25519Identity(identityText)
	if err != nil {
		return Payload{}, ErrCannotDecrypt
	}
	return decrypt(bundle, []age.Identity{identity})
}

func decrypt(bundle []byte, identities []age.Identity) (Payload, error) {
	if !bytes.HasPrefix(bundle, []byte(Header)) {
		return Payload{}, ErrInvalidBundle
	}
	reader, err := age.Decrypt(bytes.NewReader(bundle[len(Header):]), identities...)
	if err != nil {
		return Payload{}, ErrCannotDecrypt
	}
	plaintext, err := io.ReadAll(io.LimitReader(reader, 16*1024*1024))
	if err != nil {
		return Payload{}, ErrCannotDecrypt
	}
	var payload Payload
	decoder := json.NewDecoder(bytes.NewReader(plaintext))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil || payload.Format != "smallworlds-recovery-bundle" || payload.Version != 1 || payload.Profile.ID == "" || payload.VaultMaterial == nil {
		return Payload{}, ErrInvalidBundle
	}
	return payload, nil
}
