package hetzner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultAPIBaseURL is the Hetzner Cloud API root.
const DefaultAPIBaseURL = "https://api.hetzner.cloud/v1"

var (
	// ErrRateLimited means the provider throttled the call. It is never
	// converted into a negative verdict — a throttled check is inconclusive.
	ErrRateLimited = errors.New("hetzner: provider rate limit reached")
	// ErrUnauthorized means the provider rejected the token.
	ErrUnauthorized = errors.New("hetzner: token was rejected")
	// ErrProvider is a transport or protocol failure.
	ErrProvider = errors.New("hetzner: provider request failed")
)

// pageSize is the page size requested for every listing. The client always
// follows pagination to the end: a partially listed project would look like a
// project with resources missing, which is exactly how a resource gets
// silently duplicated.
const pageSize = 50

// maxPages bounds a pathological pagination loop.
const maxPages = 200

// Client is the read-only Hetzner Cloud boundary. Every method it exposes is a
// GET, with one deliberate exception (the write-authority probe) that is
// constructed to be rejected before it can create anything.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient builds a client against an API root.
func NewClient(baseURL string, httpClient *http.Client) *Client {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = DefaultAPIBaseURL
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), httpClient: httpClient}
}

// Probe establishes what a token can do. Read authority comes from a listing
// call; write authority comes from a deliberately invalid create call, which a
// read-write token rejects as invalid input (422) and a read-only token
// rejects as forbidden (403). The call cannot create a resource: it carries no
// name and no key material, so the provider refuses it either way.
func (client *Client) Probe(ctx context.Context, token string) (TokenProbe, error) {
	probe := TokenProbe{}
	_, err := client.list(ctx, token, "/locations", "locations")
	switch {
	case errors.Is(err, ErrUnauthorized):
		return TokenProbe{Unauthorized: true}, nil
	case errors.Is(err, ErrRateLimited):
		return TokenProbe{RateLimited: true}, nil
	case err != nil:
		return TokenProbe{}, err
	}
	probe.ReadAuthority = true
	// The project has no identifier of its own in the API, so the identity is
	// derived from the resources only this project can see. Server and SSH key
	// identities are stable and project-scoped.
	projectID, err := client.projectIdentity(ctx, token)
	if err != nil {
		return TokenProbe{}, err
	}
	probe.ProjectID = projectID

	status, err := client.writeProbeStatus(ctx, token)
	if err != nil {
		return TokenProbe{}, err
	}
	switch status {
	case http.StatusForbidden:
		probe.WriteAuthority = false
	case http.StatusTooManyRequests:
		probe.RateLimited = true
	case http.StatusUnauthorized:
		return TokenProbe{Unauthorized: true}, nil
	default:
		// Anything else (422 invalid input in practice) means the token was
		// permitted to attempt the write.
		probe.WriteAuthority = true
	}
	return probe, nil
}

// projectIdentity derives a stable identity for the project a token addresses,
// so re-pointing a Cluster Profile at different infrastructure is detectable.
// It uses the project's oldest SSH key or server; a project with neither is
// identified as new, which is correct — there is nothing yet to collide with.
func (client *Client) projectIdentity(ctx context.Context, token string) (string, error) {
	for _, source := range []struct{ path, key string }{{"/ssh_keys", "ssh_keys"}, {"/servers", "servers"}, {"/primary_ips", "primary_ips"}} {
		entries, err := client.list(ctx, token, source.path, source.key)
		if err != nil {
			return "", err
		}
		oldest := ""
		for _, entry := range entries {
			id := identityText(entry["id"])
			if id != "" && (oldest == "" || id < oldest) {
				oldest = id
			}
		}
		if oldest != "" {
			return strings.TrimPrefix(source.path, "/") + ":" + oldest, nil
		}
	}
	return "", nil
}

// writeProbeStatus performs the intentionally invalid create and returns the
// provider's status code.
func (client *Client) writeProbeStatus(ctx context.Context, token string) (int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+"/ssh_keys", strings.NewReader("{}"))
	if err != nil {
		return 0, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.httpClient.Do(request)
	if err != nil {
		return 0, fmt.Errorf("%w: write authority probe: %v", ErrProvider, err)
	}
	defer response.Body.Close()
	return response.StatusCode, nil
}

// Catalog reads the live server-type offerings, their availability per
// location, and the volume and Primary IP prices. Nothing is compiled in: a
// stale price is the financial surprise this is meant to prevent.
func (client *Client) Catalog(ctx context.Context, token string) (PriceCatalog, error) {
	pricing, err := client.get(ctx, token, "/pricing")
	if err != nil {
		return PriceCatalog{}, err
	}
	catalog := PriceCatalog{ObservedAt: time.Now().UTC()}
	prices, _ := pricing["pricing"].(map[string]any)
	catalog.VolumeMonthlyEURPerGB = grossMonthly(prices["volume"])
	catalog.PrimaryIPMonthlyEUR = primaryIPMonthly(prices["primary_ips"])

	locations, err := client.list(ctx, token, "/locations", "locations")
	if err != nil {
		return PriceCatalog{}, err
	}
	for _, location := range locations {
		if name := text(location["name"]); name != "" {
			catalog.Locations = append(catalog.Locations, name)
		}
	}

	serverPrices := serverTypeMonthlyPrices(prices["server_types"])
	serverTypes, err := client.list(ctx, token, "/server_types", "server_types")
	if err != nil {
		return PriceCatalog{}, err
	}
	for _, serverType := range serverTypes {
		name := text(serverType["name"])
		if name == "" || boolean(serverType["deprecated"]) {
			continue
		}
		offering := ServerOffering{
			Name:         name,
			VCPU:         integer(serverType["cores"]),
			MemoryGB:     integer(serverType["memory"]),
			DiskGB:       integer(serverType["disk"]),
			Architecture: text(serverType["architecture"]),
			MonthlyEUR:   serverPrices[name],
		}
		for _, entry := range slice(serverType["prices"]) {
			price, _ := entry.(map[string]any)
			if price == nil {
				continue
			}
			// Hetzner reports availability per location as of the 1.54 API by
			// listing a price for each location the type can be created in.
			if location := text(price["location"]); location != "" {
				offering.AvailableLocations = append(offering.AvailableLocations, location)
			}
		}
		if offering.MonthlyEUR > 0 && offering.MemoryGB > 0 {
			catalog.Offerings = append(catalog.Offerings, offering)
		}
	}
	if err := catalog.Validate(); err != nil {
		return PriceCatalog{}, fmt.Errorf("%w: %v", ErrProvider, err)
	}
	return catalog, nil
}

// Inventory lists every inspected resource kind in the project, following
// pagination to the end of each listing. It reads only: no listing call can
// change a project.
func (client *Client) Inventory(ctx context.Context, token, domain string) ([]Resource, error) {
	resources := make([]Resource, 0)
	simple := []struct {
		path, key string
		kind      ResourceKind
		detail    func(map[string]any) string
	}{
		{"/primary_ips", "primary_ips", KindPrimaryIP, func(entry map[string]any) string { return text(entry["ip"]) }},
		{"/ssh_keys", "ssh_keys", KindSSHKey, func(entry map[string]any) string { return text(entry["fingerprint"]) }},
		{"/firewalls", "firewalls", KindFirewall, func(entry map[string]any) string {
			return fmt.Sprintf("%d rules", len(slice(entry["rules"])))
		}},
		{"/volumes", "volumes", KindVolume, func(entry map[string]any) string {
			return fmt.Sprintf("%d GB", integer(entry["size"]))
		}},
		{"/servers", "servers", KindServer, func(entry map[string]any) string {
			serverType, _ := entry["server_type"].(map[string]any)
			return text(serverType["name"])
		}},
	}
	for _, source := range simple {
		entries, err := client.list(ctx, token, source.path, source.key)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			resources = append(resources, Resource{
				Kind:       source.kind,
				ProviderID: identityText(entry["id"]),
				Name:       text(entry["name"]),
				Location:   locationName(entry),
				Labels:     labels(entry["labels"]),
				Detail:     source.detail(entry),
			})
		}
	}
	// Reverse DNS lives on the Primary IP rather than as its own resource, so
	// it is inventoried from the same listing under its own kind.
	primaryIPs, err := client.list(ctx, token, "/primary_ips", "primary_ips")
	if err != nil {
		return nil, err
	}
	for _, entry := range primaryIPs {
		for _, pointer := range slice(entry["dns_ptr"]) {
			record, _ := pointer.(map[string]any)
			if name := text(record["dns_ptr"]); name != "" {
				resources = append(resources, Resource{
					Kind:       KindReverseDNS,
					ProviderID: identityText(entry["id"]) + ":" + text(record["ip"]),
					Name:       name,
					Labels:     labels(entry["labels"]),
					Detail:     text(record["ip"]),
				})
			}
		}
	}

	zones, err := client.list(ctx, token, "/zones", "zones")
	if err != nil {
		return nil, err
	}
	for _, zone := range zones {
		zoneName, zoneID := text(zone["name"]), identityText(zone["id"])
		resources = append(resources, Resource{Kind: KindDNSZone, ProviderID: zoneID, Name: zoneName, Labels: labels(zone["labels"]), Detail: text(zone["mode"])})
		if zoneName != domain || zoneID == "" {
			continue
		}
		records, err := client.list(ctx, token, "/zones/"+url.PathEscape(zoneID)+"/rrsets", "rrsets")
		if err != nil {
			return nil, err
		}
		for _, record := range records {
			if strings.ToUpper(text(record["type"])) != "A" {
				continue
			}
			resources = append(resources, Resource{
				Kind:       KindDNSRecord,
				ProviderID: zoneID + ":" + text(record["id"]),
				Name:       text(record["name"]),
				Labels:     labels(record["labels"]),
				Detail:     text(record["type"]),
			})
		}
	}
	return resources, nil
}

// Nameservers reports the nameservers the *registrar* currently publishes for
// the domain (the zone's delegated set), which is what decides whether public
// DNS and certificate issuance will work. An absent or empty set is returned as
// nil, which the delegation check treats as inconclusive rather than as proof
// the domain is misconfigured.
func (client *Client) Nameservers(ctx context.Context, token, domain string) ([]string, error) {
	zones, err := client.list(ctx, token, "/zones", "zones")
	if err != nil {
		return nil, err
	}
	for _, zone := range zones {
		if text(zone["name"]) != domain {
			continue
		}
		nameservers, _ := zone["authoritative_nameservers"].(map[string]any)
		observed := make([]string, 0)
		for _, entry := range slice(nameservers["delegated"]) {
			if nameserver := text(entry); nameserver != "" {
				observed = append(observed, nameserver)
			}
		}
		return observed, nil
	}
	return nil, nil
}

// list follows pagination to the end of a collection.
func (client *Client) list(ctx context.Context, token, path, key string) ([]map[string]any, error) {
	entries := make([]map[string]any, 0)
	for page := 1; page <= maxPages; page++ {
		payload, err := client.get(ctx, token, fmt.Sprintf("%s?page=%d&per_page=%d", path, page, pageSize))
		if err != nil {
			return nil, err
		}
		for _, entry := range slice(payload[key]) {
			if record, ok := entry.(map[string]any); ok {
				entries = append(entries, record)
			}
		}
		meta, _ := payload["meta"].(map[string]any)
		pagination, _ := meta["pagination"].(map[string]any)
		if pagination == nil || pagination["next_page"] == nil {
			return entries, nil
		}
	}
	return nil, fmt.Errorf("%w: pagination did not terminate for %s", ErrProvider, path)
}

func (client *Client) get(ctx context.Context, token, path string) (map[string]any, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, client.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json")
	response, err := client.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrProvider, err)
	}
	defer response.Body.Close()
	switch {
	case response.StatusCode == http.StatusUnauthorized:
		return nil, ErrUnauthorized
	case response.StatusCode == http.StatusTooManyRequests:
		return nil, ErrRateLimited
	case response.StatusCode == http.StatusForbidden:
		return nil, ErrUnauthorized
	case response.StatusCode < 200 || response.StatusCode >= 300:
		return nil, fmt.Errorf("%w: %s returned %s", ErrProvider, path, response.Status)
	}
	var payload map[string]any
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("%w: decode %s", ErrProvider, path)
	}
	return payload, nil
}

// serverTypeMonthlyPrices reads the gross monthly price per server type from
// the pricing document.
func serverTypeMonthlyPrices(raw any) map[string]float64 {
	prices := map[string]float64{}
	for _, entry := range slice(raw) {
		serverType, _ := entry.(map[string]any)
		name := text(serverType["name"])
		if name == "" {
			continue
		}
		for _, priceEntry := range slice(serverType["prices"]) {
			price, _ := priceEntry.(map[string]any)
			if monthly := grossMonthly(price); monthly > 0 {
				prices[name] = monthly
				break
			}
		}
	}
	return prices
}

func primaryIPMonthly(raw any) float64 {
	for _, entry := range slice(raw) {
		primaryIP, _ := entry.(map[string]any)
		if strings.EqualFold(text(primaryIP["type"]), "ipv4") {
			for _, priceEntry := range slice(primaryIP["prices"]) {
				price, _ := priceEntry.(map[string]any)
				if monthly := grossMonthly(price); monthly > 0 {
					return monthly
				}
			}
		}
	}
	return 0
}

// grossMonthly reads price_monthly.gross from a price object, which is the
// figure an operator is actually billed.
func grossMonthly(raw any) float64 {
	price, _ := raw.(map[string]any)
	monthly, _ := price["price_monthly"].(map[string]any)
	value, err := strconv.ParseFloat(text(monthly["gross"]), 64)
	if err != nil {
		return 0
	}
	return value
}

func locationName(entry map[string]any) string {
	if location, ok := entry["location"].(map[string]any); ok {
		return text(location["name"])
	}
	if datacenter, ok := entry["datacenter"].(map[string]any); ok {
		if location, ok := datacenter["location"].(map[string]any); ok {
			return text(location["name"])
		}
	}
	return ""
}

func labels(raw any) map[string]string {
	source, _ := raw.(map[string]any)
	if len(source) == 0 {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = text(value)
	}
	return result
}

func slice(raw any) []any {
	values, _ := raw.([]any)
	return values
}

func text(raw any) string {
	switch value := raw.(type) {
	case string:
		return value
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(value)
	default:
		return ""
	}
}

func integer(raw any) int {
	value, _ := raw.(float64)
	return int(value)
}

func boolean(raw any) bool {
	value, _ := raw.(bool)
	return value
}

// identityText renders a provider id, which the API returns as a number, as the
// stable string identity used everywhere else.
func identityText(raw any) string {
	if value, ok := raw.(float64); ok {
		return strconv.FormatInt(int64(value), 10)
	}
	return text(raw)
}
