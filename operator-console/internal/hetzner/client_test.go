package hetzner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const probeToken = validTokenValue

// providerStub is a deterministic stand-in for the Hetzner Cloud API. It
// records every request so the read-only contract stays observable.
type providerStub struct {
	mutex        sync.Mutex
	requests     []string
	writeStatus  int
	rateLimitAll bool
	unauthorized bool
	sshKeyPages  int
}

func (stub *providerStub) record(request *http.Request) {
	stub.mutex.Lock()
	defer stub.mutex.Unlock()
	stub.requests = append(stub.requests, request.Method+" "+request.URL.Path)
}

func (stub *providerStub) methods() []string {
	stub.mutex.Lock()
	defer stub.mutex.Unlock()
	return append([]string(nil), stub.requests...)
}

func (stub *providerStub) server(t *testing.T) *httptest.Server {
	t.Helper()
	handler := http.NewServeMux()
	writeJSON := func(response http.ResponseWriter, payload any) {
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(payload)
	}
	page := func(request *http.Request) int {
		if value := request.URL.Query().Get("page"); value == "2" {
			return 2
		}
		return 1
	}
	handler.HandleFunc("/", func(response http.ResponseWriter, request *http.Request) {
		stub.record(request)
		if request.Header.Get("Authorization") != "Bearer "+probeToken {
			response.WriteHeader(http.StatusUnauthorized)
			return
		}
		if stub.rateLimitAll {
			response.WriteHeader(http.StatusTooManyRequests)
			return
		}
		if stub.unauthorized {
			response.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/v1/ssh_keys":
			response.WriteHeader(stub.writeStatus)
		case request.URL.Path == "/v1/locations":
			writeJSON(response, map[string]any{"locations": []any{map[string]any{"name": "nbg1"}, map[string]any{"name": "fsn1"}}, "meta": lastPage()})
		case request.URL.Path == "/v1/ssh_keys":
			// Two pages, so a client that stops after the first misses the key
			// that decides the project identity.
			if page(request) == 1 && stub.sshKeyPages > 1 {
				writeJSON(response, map[string]any{"ssh_keys": []any{map[string]any{"id": float64(900), "name": "unrelated-key", "fingerprint": "aa:bb"}}, "meta": nextPage(2)})
				return
			}
			writeJSON(response, map[string]any{"ssh_keys": []any{map[string]any{"id": float64(41), "name": SharedAdminSSHKeyName, "fingerprint": "cc:dd"}}, "meta": lastPage()})
		case request.URL.Path == "/v1/servers":
			writeJSON(response, map[string]any{"servers": []any{map[string]any{"id": float64(77), "name": "cc-pilot-node-01", "server_type": map[string]any{"name": "cx43"}, "datacenter": map[string]any{"location": map[string]any{"name": "nbg1"}}, "labels": map[string]any{LabelProfile: "profile-1"}}}, "meta": lastPage()})
		case request.URL.Path == "/v1/primary_ips":
			writeJSON(response, map[string]any{"primary_ips": []any{map[string]any{"id": float64(12), "name": "smallworlds-ip", "ip": "203.0.113.10", "dns_ptr": []any{map[string]any{"ip": "203.0.113.10", "dns_ptr": "mail.example.org"}}}}, "meta": lastPage()})
		case request.URL.Path == "/v1/firewalls":
			writeJSON(response, map[string]any{"firewalls": []any{map[string]any{"id": float64(31), "name": "smallworlds-firewall", "rules": []any{map[string]any{}, map[string]any{}}}}, "meta": lastPage()})
		case request.URL.Path == "/v1/volumes":
			writeJSON(response, map[string]any{"volumes": []any{map[string]any{"id": float64(55), "name": "smallworlds-data", "size": float64(200), "location": map[string]any{"name": "nbg1"}}}, "meta": lastPage()})
		case request.URL.Path == "/v1/zones":
			writeJSON(response, map[string]any{"zones": []any{map[string]any{"id": float64(5), "name": "example.org", "mode": "primary", "authoritative_nameservers": map[string]any{"delegated": []any{"hydrogen.ns.hetzner.com", "oxygen.ns.hetzner.com", "helium.ns.hetzner.de"}}}}, "meta": lastPage()})
		case request.URL.Path == "/v1/zones/5/rrsets":
			writeJSON(response, map[string]any{"rrsets": []any{map[string]any{"id": "files/A", "name": "files", "type": "A"}, map[string]any{"id": "example.org/SOA", "name": "@", "type": "SOA"}}, "meta": lastPage()})
		case request.URL.Path == "/v1/server_types":
			writeJSON(response, map[string]any{"server_types": []any{
				map[string]any{"name": "cx43", "cores": float64(8), "memory": float64(16), "disk": float64(160), "architecture": "x86", "prices": []any{map[string]any{"location": "nbg1"}, map[string]any{"location": "fsn1"}}},
				map[string]any{"name": "cx11", "cores": float64(1), "memory": float64(2), "disk": float64(20), "architecture": "x86", "deprecated": true, "prices": []any{map[string]any{"location": "nbg1"}}},
			}, "meta": lastPage()})
		case request.URL.Path == "/v1/pricing":
			writeJSON(response, map[string]any{"pricing": map[string]any{
				"volume":       map[string]any{"price_monthly": map[string]any{"gross": "0.0440"}},
				"primary_ips":  []any{map[string]any{"type": "ipv4", "prices": []any{map[string]any{"price_monthly": map[string]any{"gross": "0.6000"}}}}},
				"server_types": []any{map[string]any{"name": "cx43", "prices": []any{map[string]any{"location": "nbg1", "price_monthly": map[string]any{"gross": "16.4000"}}}}},
			}})
		default:
			response.WriteHeader(http.StatusNotFound)
		}
	})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}

func lastPage() map[string]any {
	return map[string]any{"pagination": map[string]any{"page": float64(1), "next_page": nil}}
}

func nextPage(next int) map[string]any {
	return map[string]any{"pagination": map[string]any{"page": float64(1), "next_page": float64(next)}}
}

func newTestClient(t *testing.T, stub *providerStub) *Client {
	t.Helper()
	server := stub.server(t)
	return NewClient(server.URL+"/v1", server.Client())
}

func TestProbeClassifiesAuthority(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		stub      func() *providerStub
		wantState TokenState
		wantWrite bool
	}{
		{name: "read-write token", stub: func() *providerStub {
			return &providerStub{writeStatus: http.StatusUnprocessableEntity, sshKeyPages: 1}
		}, wantState: TokenValid, wantWrite: true},
		{name: "read-only token", stub: func() *providerStub {
			return &providerStub{writeStatus: http.StatusForbidden, sshKeyPages: 1}
		}, wantState: TokenReadOnly},
		{name: "throttled probe", stub: func() *providerStub { return &providerStub{rateLimitAll: true} }, wantState: TokenInconclusive},
		{name: "rejected token", stub: func() *providerStub { return &providerStub{unauthorized: true} }, wantState: TokenUnauthorized},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			stub := testCase.stub()
			client := newTestClient(t, stub)
			probe, err := client.Probe(context.Background(), probeToken)
			if err != nil {
				t.Fatalf("probe: %v", err)
			}
			if probe.WriteAuthority != testCase.wantWrite {
				t.Fatalf("write authority %v", probe.WriteAuthority)
			}
			if assessment := AssessToken(probeToken, probe, ""); assessment.State != testCase.wantState {
				t.Fatalf("assessed %s, want %s", assessment.State, testCase.wantState)
			}
			// The only non-GET the client ever issues is the write probe, and it
			// can create nothing.
			for _, request := range stub.methods() {
				if strings.HasPrefix(request, "POST ") && request != "POST /v1/ssh_keys" {
					t.Fatalf("unexpected mutating request %q", request)
				}
			}
		})
	}
}

func TestProbeIdentifiesTheProjectAcrossPages(t *testing.T) {
	stub := providerStub{writeStatus: http.StatusUnprocessableEntity, sshKeyPages: 2}
	client := newTestClient(t, &stub)
	probe, err := client.Probe(context.Background(), probeToken)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	// The identity must be the oldest key in the project, which only a client
	// that followed pagination can see.
	if probe.ProjectID != "ssh_keys:41" {
		t.Fatalf("project identity %q", probe.ProjectID)
	}
	if AssessToken(probeToken, probe, "ssh_keys:99").State != TokenProjectMismatch {
		t.Fatal("a token for another project must be refused")
	}
}

func TestInventoryCoversEveryKindAndPaginates(t *testing.T) {
	stub := providerStub{writeStatus: http.StatusUnprocessableEntity, sshKeyPages: 2}
	client := newTestClient(t, &stub)
	resources, err := client.Inventory(context.Background(), probeToken, "example.org")
	if err != nil {
		t.Fatalf("inventory: %v", err)
	}
	seen := map[ResourceKind]int{}
	for _, resource := range resources {
		seen[resource.Kind]++
		if resource.ProviderID == "" {
			t.Fatalf("resource %+v has no stable provider identity", resource)
		}
	}
	for _, kind := range InspectedKinds {
		if seen[kind] == 0 {
			t.Fatalf("inventory did not cover %s", kind)
		}
	}
	if seen[KindSSHKey] != 2 {
		t.Fatalf("expected both ssh key pages, got %d", seen[KindSSHKey])
	}
	// Only A records are inventoried; the zone's own SOA is not a record the
	// installation owns.
	if seen[KindDNSRecord] != 1 {
		t.Fatalf("dns records %d", seen[KindDNSRecord])
	}
	for _, request := range stub.methods() {
		if strings.HasPrefix(request, "POST") || strings.HasPrefix(request, "PUT") || strings.HasPrefix(request, "DELETE") {
			t.Fatalf("inventory issued a mutating request %q", request)
		}
	}
}

func TestInventoryLabelsDriveOwnership(t *testing.T) {
	stub := providerStub{sshKeyPages: 1}
	client := newTestClient(t, &stub)
	resources, err := client.Inventory(context.Background(), probeToken, "example.org")
	if err != nil {
		t.Fatalf("inventory: %v", err)
	}
	inventory, err := Classify(Naming{Domain: "example.org", ProfileID: "profile-1"}, resources)
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	// The stub's server carries this profile's label; the unlabelled firewall
	// must still require an explicit adoption decision.
	server := findingFor(t, inventory, KindServer, "cc-pilot-node-01")
	if server.Ownership != OwnershipProfileOwned || server.Match.Detail != "cx43" {
		t.Fatalf("server finding %+v", server)
	}
	firewall := findingFor(t, inventory, KindFirewall, "smallworlds-firewall")
	if firewall.Ownership != OwnershipAdoptable {
		t.Fatalf("firewall finding %+v", firewall)
	}
}

func TestCatalogReadsLivePricesAndAvailability(t *testing.T) {
	stub := providerStub{sshKeyPages: 1}
	client := newTestClient(t, &stub)
	catalog, err := client.Catalog(context.Background(), probeToken)
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	if len(catalog.Offerings) != 1 {
		t.Fatalf("deprecated types must not be offered: %+v", catalog.Offerings)
	}
	offering := catalog.Offerings[0]
	if offering.Name != "cx43" || offering.MonthlyEUR != 16.40 || !offering.AvailableIn("fsn1") || offering.AvailableIn("hel1") {
		t.Fatalf("offering %+v", offering)
	}
	if catalog.VolumeMonthlyEURPerGB != 0.044 || catalog.PrimaryIPMonthlyEUR != 0.60 || catalog.ObservedAt.IsZero() {
		t.Fatalf("catalog %+v", catalog)
	}
}

func TestNameserversReportTheRegistrarDelegation(t *testing.T) {
	stub := providerStub{sshKeyPages: 1}
	client := newTestClient(t, &stub)
	observed, err := client.Nameservers(context.Background(), probeToken, "example.org")
	if err != nil {
		t.Fatalf("nameservers: %v", err)
	}
	if delegation := CheckDelegation("example.org", observed, "hetzner"); delegation.Status != DelegationConfirmed {
		t.Fatalf("delegation %+v", delegation)
	}
	missing, err := client.Nameservers(context.Background(), probeToken, "other.example")
	if err != nil || len(missing) != 0 {
		t.Fatalf("unknown zone returned %v/%v", missing, err)
	}
}

func TestProviderFailuresStayDistinguishable(t *testing.T) {
	stub := providerStub{rateLimitAll: true}
	client := newTestClient(t, &stub)
	if _, err := client.Inventory(context.Background(), probeToken, "example.org"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("rate limit surfaced as %v", err)
	}
	rejected := providerStub{unauthorized: true}
	rejectedClient := newTestClient(t, &rejected)
	if _, err := rejectedClient.Inventory(context.Background(), probeToken, "example.org"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("rejection surfaced as %v", err)
	}
	if _, err := newTestClient(t, &providerStub{}).Inventory(context.Background(), "wrong-token", "example.org"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("wrong token surfaced as %v", err)
	}
}

func TestProviderErrorsNeverEchoTheToken(t *testing.T) {
	stub := providerStub{rateLimitAll: true}
	client := newTestClient(t, &stub)
	_, err := client.Catalog(context.Background(), probeToken)
	if err == nil {
		t.Fatal("expected a failure")
	}
	if strings.Contains(fmt.Sprint(err), probeToken) {
		t.Fatal("provider error leaked the token")
	}
}
