package nodeinspect

import (
	"errors"
	"fmt"
	"net"
	"os"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

var ErrHostKeyUntrusted = errors.New("SSH host key requires explicit confirmation")
var ErrHostKeyMismatch = errors.New("SSH host key does not match pinned identity")
var ErrAuthenticationUnavailable = errors.New("SSH authentication material is unavailable")

type AuthenticationKind string

const (
	AgentAuthentication      AuthenticationKind = "agent"
	PrivateKeyAuthentication AuthenticationKind = "private-key"
	PasswordAuthentication   AuthenticationKind = "password"
)

type Credentials struct {
	Kind          AuthenticationKind
	Password      string
	PrivateKey    []byte
	KeyPassphrase string
	SudoPassword  string
}

func (credentials Credentials) AuthMethod() (ssh.AuthMethod, error) {
	switch credentials.Kind {
	case PasswordAuthentication:
		if credentials.Password == "" {
			return nil, ErrAuthenticationUnavailable
		}
		return ssh.Password(credentials.Password), nil
	case PrivateKeyAuthentication:
		if len(credentials.PrivateKey) == 0 {
			return nil, ErrAuthenticationUnavailable
		}
		var signer ssh.Signer
		var err error
		if credentials.KeyPassphrase == "" {
			signer, err = ssh.ParsePrivateKey(credentials.PrivateKey)
		} else {
			signer, err = ssh.ParsePrivateKeyWithPassphrase(credentials.PrivateKey, []byte(credentials.KeyPassphrase))
		}
		if err != nil {
			return nil, fmt.Errorf("parse SSH private key: %w", err)
		}
		return ssh.PublicKeys(signer), nil
	case AgentAuthentication:
		socket := os.Getenv("SSH_AUTH_SOCK")
		if socket == "" {
			return nil, ErrAuthenticationUnavailable
		}
		connection, err := net.DialTimeout("unix", socket, 3*time.Second)
		if err != nil {
			return nil, fmt.Errorf("connect to SSH agent: %w", err)
		}
		return ssh.PublicKeysCallback(agent.NewClient(connection).Signers), nil
	default:
		return nil, ErrAuthenticationUnavailable
	}
}

func PinnedHostKeyCallback(expectedFingerprint string) ssh.HostKeyCallback {
	return func(_ string, _ net.Addr, key ssh.PublicKey) error {
		actual := HostKeyFingerprint(key)
		if expectedFingerprint == "" {
			return fmt.Errorf("%w: %s", ErrHostKeyUntrusted, actual)
		}
		if actual != expectedFingerprint {
			return fmt.Errorf("%w: expected %s, received %s", ErrHostKeyMismatch, expectedFingerprint, actual)
		}
		return nil
	}
}
