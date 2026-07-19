package nodeinspect

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
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

// ProbeHostKey performs only the SSH transport handshake. It deliberately
// does not authenticate, execute a command, or trust the observed key; the
// caller must present the returned fingerprint for explicit confirmation.
func ProbeHostKey(ctx context.Context, target Target) (string, error) {
	if err := target.Validate("linux"); err != nil {
		return "", fmt.Errorf("invalid remote target: %w", err)
	}
	if target.Kind != RemoteTarget {
		return "", fmt.Errorf("invalid remote target: target is not remote")
	}
	dialer := net.Dialer{Timeout: 10 * time.Second}
	connection, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(target.Host, strconv.Itoa(target.Port)))
	if err != nil {
		return "", fmt.Errorf("dial remote SSH target: %w", err)
	}
	defer connection.Close()
	fingerprint := ""
	config := &ssh.ClientConfig{User: target.Username, HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
		fingerprint = HostKeyFingerprint(key)
		return ErrHostKeyUntrusted
	}}
	_, _, _, handshakeErr := ssh.NewClientConn(connection, net.JoinHostPort(target.Host, strconv.Itoa(target.Port)), config)
	if fingerprint != "" {
		return fingerprint, nil
	}
	if handshakeErr == nil {
		return "", fmt.Errorf("SSH server did not offer a host key")
	}
	return "", fmt.Errorf("read remote SSH host key: %w", handshakeErr)
}

// DialTrusted creates an authenticated SSH client only after the server's key
// matches the durable profile pin. It is intentionally narrower than a generic
// remote command API; inspection supplies its own fixed command contract.
func DialTrusted(ctx context.Context, target Target, credentials Credentials, fingerprint string) (*ssh.Client, error) {
	if err := target.Validate("linux"); err != nil {
		return nil, fmt.Errorf("invalid remote target: %w", err)
	}
	if target.Kind != RemoteTarget {
		return nil, fmt.Errorf("invalid remote target: target is not remote")
	}
	auth, err := credentials.AuthMethod()
	if err != nil {
		return nil, err
	}
	dialer := net.Dialer{Timeout: 10 * time.Second}
	connection, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(target.Host, strconv.Itoa(target.Port)))
	if err != nil {
		return nil, fmt.Errorf("dial trusted SSH target: %w", err)
	}
	config := &ssh.ClientConfig{User: target.Username, Auth: []ssh.AuthMethod{auth}, HostKeyCallback: PinnedHostKeyCallback(fingerprint), Timeout: 15 * time.Second}
	clientConnection, channels, requests, err := ssh.NewClientConn(connection, net.JoinHostPort(target.Host, strconv.Itoa(target.Port)), config)
	if err != nil {
		connection.Close()
		return nil, fmt.Errorf("establish trusted SSH connection: %w", err)
	}
	return ssh.NewClient(clientConnection, channels, requests), nil
}

// ValidateSudoCredential tests only sudo authorization when the SSH account is
// not root and an explicit separate sudo password was supplied. It never runs
// a privileged provisioning command.
func ValidateSudoCredential(client *ssh.Client, password string) error {
	if password == "" {
		return nil
	}
	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("start sudo validation: %w", err)
	}
	defer session.Close()
	session.Stdin = strings.NewReader(password + "\n")
	if err := session.Run("sudo -S -p '' -v"); err != nil {
		return fmt.Errorf("validate sudo credential: %w", err)
	}
	return nil
}
