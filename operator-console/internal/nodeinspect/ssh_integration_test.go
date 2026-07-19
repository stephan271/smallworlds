package nodeinspect_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/nodeinspect"
	"golang.org/x/crypto/ssh"
)

func TestProbeThenPinnedPasswordConnectionUsesActualSSHHostKey(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	config := &ssh.ServerConfig{PasswordCallback: func(connection ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
		if connection.User() == "operator" && string(password) == "node-password" {
			return nil, nil
		}
		return nil, ssh.ErrNoAuth
	}}
	config.AddHostKey(signer)
	var served sync.WaitGroup
	served.Add(2)
	go func() {
		for range 2 {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer served.Done()
				defer connection.Close()
				serverConnection, channels, requests, err := ssh.NewServerConn(connection, config)
				if err == nil {
					go ssh.DiscardRequests(requests)
					for channel := range channels {
						channel.Reject(ssh.UnknownChannelType, "inspection test does not run commands")
					}
					serverConnection.Close()
				}
			}()
		}
	}()
	address := listener.Addr().(*net.TCPAddr)
	target := nodeinspect.Target{Kind: nodeinspect.RemoteTarget, Host: "127.0.0.1", Port: address.Port, Username: "operator"}
	context, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	fingerprint, err := nodeinspect.ProbeHostKey(context, target)
	if err != nil {
		t.Fatal(err)
	}
	client, err := nodeinspect.DialTrusted(context, target, nodeinspect.Credentials{Kind: nodeinspect.PasswordAuthentication, Password: "node-password"}, fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	client.Close()
	served.Wait()
}
