package hetzner

import (
	"fmt"
	"sort"
	"strings"
)

// ResourceKind is one class of provider resource a SmallWorlds installation
// occupies. The set mirrors infrastructure/terraform/main.tf plus the two
// resources smallworlds-init.sh deliberately creates outside Terraform state
// (the DNS zone and the shared admin SSH key).
type ResourceKind string

const (
	KindPrimaryIP  ResourceKind = "primary-ip"
	KindDNSZone    ResourceKind = "dns-zone"
	KindSSHKey     ResourceKind = "ssh-key"
	KindFirewall   ResourceKind = "firewall"
	KindVolume     ResourceKind = "volume"
	KindServer     ResourceKind = "server"
	KindDNSRecord  ResourceKind = "dns-record"
	KindReverseDNS ResourceKind = "reverse-dns"
)

// InspectedKinds is the ordered set an inspection must cover. Inspecting fewer
// kinds is a failed inspection, not a partial one.
var InspectedKinds = []ResourceKind{KindPrimaryIP, KindDNSZone, KindSSHKey, KindFirewall, KindVolume, KindServer, KindDNSRecord, KindReverseDNS}

// The provider labels that make ownership explicit. Only a resource carrying
// LabelProfile is treated as belonging to a Cluster Profile; nothing is
// inferred from a name alone.
const (
	LabelProfile = "smallworlds-profile"
	LabelManaged = "smallworlds-managed"
)

// SharedAdminSSHKeyName is the one admin key smallworlds-init.sh uploads once
// per project and every profile reuses (see ensure_ssh_key()).
const SharedAdminSSHKeyName = "SmallWorlds Admin Key"

// DefaultRecordNames are the subdomains Terraform maintains as A records.
var DefaultRecordNames = []string{"identity", "dashboard", "files", "photos", "git", "mail", "webmail", "monitoring", "whiteboard", "meet", "office", "plan", "deploy", "vpn"}

// Naming derives every expected resource name from the profile's domain and
// environment extension, exactly as main.tf does (dots belong to DNS, so
// resource names use a dash form of the extension).
type Naming struct {
	Domain    string `json:"domain"`
	EnvExt    string `json:"envExt"`
	ProfileID string `json:"profileId"`
}

// Validate enforces a domain and a well-formed extension.
func (naming Naming) Validate() error {
	domain := strings.TrimSpace(naming.Domain)
	if domain == "" || strings.Contains(domain, "/") || !strings.Contains(domain, ".") || strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return fmt.Errorf("%w: domain", ErrInvalidNaming)
	}
	if ext := naming.EnvExt; ext != "" && (!strings.HasPrefix(ext, ".") || strings.Contains(ext[1:], ".")) {
		return fmt.Errorf("%w: environment extension", ErrInvalidNaming)
	}
	if strings.TrimSpace(naming.ProfileID) == "" {
		return fmt.Errorf("%w: profile", ErrInvalidNaming)
	}
	return nil
}

func (naming Naming) slug() string { return strings.ReplaceAll(naming.EnvExt, ".", "-") }

// Expectation is one resource an installation needs, identified by the exact
// name it must carry. It is the only basis for matching an existing resource:
// an inventory entry either has this exact name or it does not.
type Expectation struct {
	Kind ResourceKind `json:"kind"`
	Name string       `json:"name"`
	// Shared marks resources deliberately shared by every profile in the
	// project (the DNS zone and the admin SSH key), so reusing one is normal
	// rather than an adoption decision.
	Shared bool `json:"shared"`
}

// Expectations lists every resource the installation occupies, in inspection
// order.
func Expectations(naming Naming) ([]Expectation, error) {
	if err := naming.Validate(); err != nil {
		return nil, err
	}
	slug := naming.slug()
	expectations := []Expectation{
		{Kind: KindPrimaryIP, Name: "smallworlds-ip" + slug},
		{Kind: KindDNSZone, Name: naming.Domain, Shared: true},
		{Kind: KindSSHKey, Name: SharedAdminSSHKeyName, Shared: true},
		{Kind: KindFirewall, Name: "smallworlds-firewall" + slug},
		{Kind: KindVolume, Name: "smallworlds-data" + slug},
		{Kind: KindServer, Name: "cc-pilot-node-01" + slug},
	}
	if naming.EnvExt == "" {
		expectations = append(expectations, Expectation{Kind: KindDNSRecord, Name: "@"})
	}
	for _, record := range DefaultRecordNames {
		expectations = append(expectations, Expectation{Kind: KindDNSRecord, Name: record + naming.EnvExt})
	}
	expectations = append(expectations, Expectation{Kind: KindReverseDNS, Name: "mail" + naming.EnvExt + "." + naming.Domain})
	return expectations, nil
}

// Resource is one inventoried provider resource. ProviderID is the stable
// provider identity — matching, adoption, and the plan digest all key on it, so
// a renamed or re-created resource can never be mistaken for the same one.
type Resource struct {
	Kind       ResourceKind      `json:"kind"`
	ProviderID string            `json:"providerId"`
	Name       string            `json:"name"`
	Location   string            `json:"location,omitempty"`
	Labels     map[string]string `json:"labels,omitempty"`
	// Detail is a short non-secret descriptor (server type, volume size, IP
	// address) shown to the operator to make a decision informed.
	Detail string `json:"detail,omitempty"`
}

// Ownership is how an existing resource relates to this Cluster Profile.
type Ownership string

const (
	// OwnershipShared is a project-wide resource every profile reuses.
	OwnershipShared Ownership = "shared"
	// OwnershipProfileOwned is labelled as belonging to this profile.
	OwnershipProfileOwned Ownership = "profile-owned"
	// OwnershipAdoptable exactly matches an expected name and carries no
	// conflicting ownership label — reusable, but only by explicit adoption.
	OwnershipAdoptable Ownership = "adoptable"
	// OwnershipConflicting is labelled as belonging to a different profile.
	OwnershipConflicting Ownership = "conflicting"
	// OwnershipUnknown is a similarly named resource of unclear provenance. It
	// is never adoptable — the operator must resolve it in the provider.
	OwnershipUnknown Ownership = "unknown"
	// OwnershipAbsent means nothing matched; the resource will be created.
	OwnershipAbsent Ownership = "absent"
)

// Finding is the classification of one expectation against the inventory.
type Finding struct {
	Expectation Expectation `json:"expectation"`
	// Match is the exactly named resource, when one exists.
	Match *Resource `json:"match,omitempty"`
	// Similar are resources whose names resemble the expectation without
	// matching it. They are reported so a near-miss is visible, and are never
	// adopted.
	Similar   []Resource `json:"similar,omitempty"`
	Ownership Ownership  `json:"ownership"`
	// RequiresDecision marks a finding the operator must explicitly resolve
	// before a plan can be built (adoption or conflict).
	RequiresDecision bool   `json:"requiresDecision"`
	ReasonKey        string `json:"reasonKey"`
}

// Inventory is a complete read-only inspection of a project.
type Inventory struct {
	ProjectID  string     `json:"projectId"`
	Findings   []Finding  `json:"findings"`
	Unmatched  []Resource `json:"unmatched,omitempty"`
	Digest     string     `json:"digest"`
	Incomplete bool       `json:"incomplete,omitempty"`
}

// Classify matches an expectation set against the inventoried resources. It
// applies one rule: identity comes from labels and exact names, never from
// resemblance.
func Classify(naming Naming, resources []Resource) (Inventory, error) {
	expectations, err := Expectations(naming)
	if err != nil {
		return Inventory{}, err
	}
	claimed := map[string]bool{}
	inventory := Inventory{Findings: make([]Finding, 0, len(expectations))}
	for _, expectation := range expectations {
		finding := Finding{Expectation: expectation, Ownership: OwnershipAbsent, ReasonKey: "resource-absent-will-be-created"}
		for _, resource := range resources {
			if resource.Kind != expectation.Kind {
				continue
			}
			switch {
			case resource.Name == expectation.Name:
				match := resource
				finding.Match = &match
				claimed[resourceKey(resource)] = true
			case similarName(resource.Name, expectation.Name):
				finding.Similar = append(finding.Similar, resource)
				claimed[resourceKey(resource)] = true
			}
		}
		if finding.Match != nil {
			finding.Ownership, finding.ReasonKey = classifyMatch(*finding.Match, expectation, naming.ProfileID)
		} else if len(finding.Similar) > 0 {
			finding.Ownership, finding.ReasonKey = OwnershipUnknown, "similar-name-never-adopted"
		}
		finding.RequiresDecision = finding.Ownership == OwnershipAdoptable || finding.Ownership == OwnershipConflicting || finding.Ownership == OwnershipUnknown
		inventory.Findings = append(inventory.Findings, finding)
	}
	// Resources labelled for this profile that no expectation covers are
	// surfaced too — they are the residue of a renamed or partial installation
	// and must not disappear from the operator's view.
	for _, resource := range resources {
		if !claimed[resourceKey(resource)] && resource.Labels[LabelProfile] != "" {
			inventory.Unmatched = append(inventory.Unmatched, resource)
		}
	}
	sort.SliceStable(inventory.Unmatched, func(left, right int) bool {
		return resourceKey(inventory.Unmatched[left]) < resourceKey(inventory.Unmatched[right])
	})
	inventory.Digest = inventoryDigest(inventory.Findings)
	return inventory, nil
}

func classifyMatch(resource Resource, expectation Expectation, profileID string) (Ownership, string) {
	switch owner := resource.Labels[LabelProfile]; {
	case owner == profileID && owner != "":
		return OwnershipProfileOwned, "resource-owned-by-this-profile"
	case owner != "":
		return OwnershipConflicting, "resource-owned-by-another-profile"
	case expectation.Shared:
		return OwnershipShared, "resource-shared-across-profiles"
	default:
		return OwnershipAdoptable, "resource-requires-explicit-adoption"
	}
}

// similarName reports whether an existing name resembles an expected one
// closely enough that an operator could mistake the two. Case, separators, and
// a shared prefix all count as resemblance — deliberately generous, because the
// consequence of resemblance is *refusing* to adopt.
func similarName(candidate, expected string) bool {
	normalize := func(value string) string {
		return strings.Map(func(letter rune) rune {
			if letter == '-' || letter == '_' || letter == ' ' || letter == '.' {
				return -1
			}
			return letter
		}, strings.ToLower(strings.TrimSpace(value)))
	}
	left, right := normalize(candidate), normalize(expected)
	if left == "" || right == "" || candidate == expected {
		return false
	}
	return left == right || strings.HasPrefix(left, right) || strings.HasPrefix(right, left)
}

func resourceKey(resource Resource) string {
	return string(resource.Kind) + "/" + resource.ProviderID
}

// inventoryDigest binds a plan to the exact inventory it was derived from:
// kinds, names, stable provider identities, and ownership. Any provider-side
// change to a covered resource changes the digest, so an approved plan can be
// detected as stale before anything is provisioned.
func inventoryDigest(findings []Finding) string {
	lines := make([]string, 0, len(findings))
	for _, finding := range findings {
		providerID := ""
		if finding.Match != nil {
			providerID = finding.Match.ProviderID
		}
		lines = append(lines, fmt.Sprintf("%s|%s|%s|%s|%d", finding.Expectation.Kind, finding.Expectation.Name, providerID, finding.Ownership, len(finding.Similar)))
	}
	sort.Strings(lines)
	return digestOf(strings.Join(lines, "\n"))
}
