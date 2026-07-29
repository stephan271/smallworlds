// Package kubeclient is a minimal Kubernetes API client for the in-cluster
// Operator Console. It speaks the REST API directly over stdlib HTTP
// rather than pulling in client-go, keeping the console's dependency surface as
// small as the rest of this module and its container image correspondingly
// small.
//
// The only objects it writes are the console's own Change Plan and Workflow Run
// records (ADR 0025). Everything the console observes is read-only, and every
// cluster mutation goes through the GitOps Overlay as a proposal (ADR 0008) —
// which is what keeps the deployed ServiceAccount's RBAC reviewable: get/list on
// what it observes, write on nothing but its own Activity Record.
package kubeclient

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	serviceAccountDir = "/var/run/secrets/kubernetes.io/serviceaccount"
	// tokenRefreshInterval bounds how long a projected ServiceAccount token is
	// reused. The kubelet rotates those tokens well before expiry, so the file is
	// re-read periodically rather than once at startup — a console that cached the
	// token forever would start failing every read an hour after rotation.
	tokenRefreshInterval = time.Minute
	// responseLimit bounds a single API response. The console reads individual
	// objects and small namespaced lists; an unbounded read of a large list would
	// be a memory hazard in a memory-limited pod.
	responseLimit  = 8 << 20
	requestTimeout = 15 * time.Second
)

// ErrNotFound is returned when the API server reports 404 for an object. It is
// deliberately distinct from a read failure: an object that does not exist yet
// is evidence ("declared, awaiting delivery"), while a failed read is the
// absence of evidence.
var ErrNotFound = errors.New("kubeclient: object not found")

// ErrForbidden is returned when the console's ServiceAccount may not read an
// object. Like ErrNotFound it is distinguished from a transport failure so the
// observers can report a missing permission honestly instead of as an outage.
var ErrForbidden = errors.New("kubeclient: read forbidden")

// Client reads objects from the Kubernetes API server.
type Client struct {
	BaseURL    string
	Namespace  string
	HTTPClient *http.Client

	// TokenSource returns the current bearer token. InCluster wires it to the
	// projected ServiceAccount token file; tests inject a constant.
	TokenSource func() (string, error)
}

// InCluster builds a Client from the ServiceAccount credentials the kubelet
// projects into every pod.
func InCluster() (*Client, error) {
	host, port := os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" || port == "" {
		return nil, errors.New("kubeclient: not running inside a cluster (KUBERNETES_SERVICE_HOST is unset)")
	}
	authority, err := os.ReadFile(serviceAccountDir + "/ca.crt")
	if err != nil {
		return nil, fmt.Errorf("kubeclient: read api server certificate authority: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(authority) {
		return nil, errors.New("kubeclient: api server certificate authority is not usable")
	}
	namespace, err := os.ReadFile(serviceAccountDir + "/namespace")
	if err != nil {
		return nil, fmt.Errorf("kubeclient: read own namespace: %w", err)
	}
	return &Client{
		BaseURL:   "https://" + net.JoinHostPort(host, port),
		Namespace: strings.TrimSpace(string(namespace)),
		HTTPClient: &http.Client{
			Timeout: requestTimeout,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
			},
		},
		TokenSource: fileTokenSource(serviceAccountDir + "/token"),
	}, nil
}

// fileTokenSource re-reads a projected token file at most once per refresh
// interval.
func fileTokenSource(path string) func() (string, error) {
	var (
		mu      sync.Mutex
		token   string
		readAt  time.Time
		clockFn = time.Now
	)
	return func() (string, error) {
		mu.Lock()
		defer mu.Unlock()
		if token != "" && clockFn().Sub(readAt) < tokenRefreshInterval {
			return token, nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("kubeclient: read service account token: %w", err)
		}
		token, readAt = strings.TrimSpace(string(raw)), clockFn()
		return token, nil
	}
}

// Get reads one object into target. The path is an API path such as
// "/apis/apps/v1/namespaces/nextcloud/deployments/nextcloud".
func (client *Client) Get(ctx context.Context, path string, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, client.BaseURL+path, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	if client.TokenSource != nil {
		token, err := client.TokenSource()
		if err != nil {
			return err
		}
		request.Header.Set("Authorization", "Bearer "+token)
	}
	httpClient := client.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: requestTimeout}
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("kubeclient: get %s: %w", path, err)
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return ErrNotFound
	case http.StatusForbidden, http.StatusUnauthorized:
		return ErrForbidden
	default:
		return fmt.Errorf("kubeclient: get %s returned %d", path, response.StatusCode)
	}
	if target == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, responseLimit))
		return nil
	}
	return json.NewDecoder(io.LimitReader(response.Body, responseLimit)).Decode(target)
}

// Put creates or replaces one object. It is used only for the console's own
// Change Plan and Workflow Run records; a create that races another writer is
// retried once as a replace, which is enough for records the console alone owns.
func (client *Client) Put(ctx context.Context, collectionPath, name string, object any) error {
	body, err := json.Marshal(object)
	if err != nil {
		return err
	}
	err = client.send(ctx, http.MethodPost, collectionPath, body, http.StatusCreated, http.StatusOK)
	if !errors.Is(err, errAlreadyExists) {
		return err
	}
	return client.send(ctx, http.MethodPut, collectionPath+"/"+url.PathEscape(name), body, http.StatusOK, http.StatusCreated)
}

var errAlreadyExists = errors.New("kubeclient: object already exists")

func (client *Client) send(ctx context.Context, method, path string, body []byte, accepted ...int) error {
	request, err := http.NewRequestWithContext(ctx, method, client.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	if client.TokenSource != nil {
		token, err := client.TokenSource()
		if err != nil {
			return err
		}
		request.Header.Set("Authorization", "Bearer "+token)
	}
	httpClient := client.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: requestTimeout}
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("kubeclient: %s %s: %w", method, path, err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, responseLimit))
	for _, status := range accepted {
		if response.StatusCode == status {
			return nil
		}
	}
	switch response.StatusCode {
	case http.StatusConflict:
		return errAlreadyExists
	case http.StatusForbidden, http.StatusUnauthorized:
		return ErrForbidden
	case http.StatusNotFound:
		return ErrNotFound
	default:
		return fmt.Errorf("kubeclient: %s %s returned %d", method, path, response.StatusCode)
	}
}

// NamespacedPath builds an API path for one namespaced object. Every segment is
// escaped, so a capability id or object name taken from configuration can never
// traverse into a different API path.
func NamespacedPath(apiRoot, namespace, resource, name string) string {
	path := strings.TrimRight(apiRoot, "/") + "/namespaces/" + url.PathEscape(namespace) + "/" + url.PathEscape(resource)
	if name != "" {
		path += "/" + url.PathEscape(name)
	}
	return path
}

// CoreAPI and the group roots the console reads from.
const (
	CoreAPI        = "/api/v1"
	AppsAPI        = "/apis/apps/v1"
	BatchAPI       = "/apis/batch/v1"
	NetworkingAPI  = "/apis/networking.k8s.io/v1"
	ArgoAPI        = "/apis/argoproj.io/v1alpha1"
	TraefikAPI     = "/apis/traefik.io/v1alpha1"
	CertManagerAPI = "/apis/cert-manager.io/v1"
	AdminAPI       = "/apis/admin.smallworlds.network/v1alpha1"
)
