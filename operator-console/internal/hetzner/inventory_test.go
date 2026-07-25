package hetzner

import (
	"testing"
)

func testNaming() Naming {
	return Naming{Domain: "example.org", EnvExt: ".dev", ProfileID: "profile-1"}
}

func findingFor(t *testing.T, inventory Inventory, kind ResourceKind, name string) Finding {
	t.Helper()
	for _, finding := range inventory.Findings {
		if finding.Expectation.Kind == kind && finding.Expectation.Name == name {
			return finding
		}
	}
	t.Fatalf("no finding for %s/%s", kind, name)
	return Finding{}
}

func TestExpectationsCoverEveryInspectedKind(t *testing.T) {
	expectations, err := Expectations(testNaming())
	if err != nil {
		t.Fatalf("expectations: %v", err)
	}
	covered := map[ResourceKind]bool{}
	for _, expectation := range expectations {
		covered[expectation.Kind] = true
	}
	for _, kind := range InspectedKinds {
		if !covered[kind] {
			t.Fatalf("kind %s is not inspected", kind)
		}
	}
	names := map[ResourceKind]string{KindPrimaryIP: "smallworlds-ip-dev", KindFirewall: "smallworlds-firewall-dev", KindVolume: "smallworlds-data-dev", KindServer: "cc-pilot-node-01-dev", KindDNSZone: "example.org", KindReverseDNS: "mail.dev.example.org"}
	for kind, want := range names {
		if got := findingNameForKind(expectations, kind); got != want {
			t.Fatalf("%s named %q, want %q", kind, got, want)
		}
	}
	// The apex record belongs to the production profile only.
	for _, expectation := range expectations {
		if expectation.Kind == KindDNSRecord && expectation.Name == "@" {
			t.Fatal("an environment extension must not claim the apex record")
		}
	}
	production, err := Expectations(Naming{Domain: "example.org", ProfileID: "profile-1"})
	if err != nil {
		t.Fatalf("expectations: %v", err)
	}
	if findingNameForKind(production, KindReverseDNS) != "mail.example.org" {
		t.Fatal("production reverse DNS name is wrong")
	}
}

func findingNameForKind(expectations []Expectation, kind ResourceKind) string {
	for _, expectation := range expectations {
		if expectation.Kind == kind {
			return expectation.Name
		}
	}
	return ""
}

func TestNamingValidation(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		naming Naming
		valid  bool
	}{
		{name: "production", naming: Naming{Domain: "example.org", ProfileID: "p"}, valid: true},
		{name: "extension", naming: Naming{Domain: "example.org", EnvExt: ".dev", ProfileID: "p"}, valid: true},
		{name: "no domain", naming: Naming{ProfileID: "p"}},
		{name: "bare hostname", naming: Naming{Domain: "example", ProfileID: "p"}},
		{name: "extension without dot", naming: Naming{Domain: "example.org", EnvExt: "dev", ProfileID: "p"}},
		{name: "nested extension", naming: Naming{Domain: "example.org", EnvExt: ".a.b", ProfileID: "p"}},
		{name: "no profile", naming: Naming{Domain: "example.org"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if err := testCase.naming.Validate(); (err == nil) != testCase.valid {
				t.Fatalf("validate returned %v, want valid=%v", err, testCase.valid)
			}
		})
	}
}

func TestClassifyOwnership(t *testing.T) {
	resources := []Resource{
		{Kind: KindPrimaryIP, ProviderID: "ip-1", Name: "smallworlds-ip-dev", Labels: map[string]string{LabelProfile: "profile-1"}},
		{Kind: KindDNSZone, ProviderID: "zone-1", Name: "example.org"},
		{Kind: KindSSHKey, ProviderID: "key-1", Name: SharedAdminSSHKeyName},
		{Kind: KindFirewall, ProviderID: "fw-1", Name: "smallworlds-firewall-dev"},
		{Kind: KindVolume, ProviderID: "vol-1", Name: "smallworlds-data-dev", Labels: map[string]string{LabelProfile: "profile-2"}},
		{Kind: KindServer, ProviderID: "srv-9", Name: "cc-pilot-node-01dev"},
	}
	inventory, err := Classify(testNaming(), resources)
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	for _, expected := range []struct {
		kind      ResourceKind
		name      string
		ownership Ownership
		decision  bool
	}{
		{KindPrimaryIP, "smallworlds-ip-dev", OwnershipProfileOwned, false},
		{KindDNSZone, "example.org", OwnershipShared, false},
		{KindSSHKey, SharedAdminSSHKeyName, OwnershipShared, false},
		{KindFirewall, "smallworlds-firewall-dev", OwnershipAdoptable, true},
		{KindVolume, "smallworlds-data-dev", OwnershipConflicting, true},
		{KindServer, "cc-pilot-node-01-dev", OwnershipUnknown, true},
		{KindDNSRecord, "files.dev", OwnershipAbsent, false},
	} {
		finding := findingFor(t, inventory, expected.kind, expected.name)
		if finding.Ownership != expected.ownership {
			t.Fatalf("%s/%s classified %s, want %s", expected.kind, expected.name, finding.Ownership, expected.ownership)
		}
		if finding.RequiresDecision != expected.decision {
			t.Fatalf("%s/%s requiresDecision=%v", expected.kind, expected.name, finding.RequiresDecision)
		}
	}
	// The similarly named server is reported, never matched.
	server := findingFor(t, inventory, KindServer, "cc-pilot-node-01-dev")
	if server.Match != nil || len(server.Similar) != 1 || server.Similar[0].ProviderID != "srv-9" {
		t.Fatalf("similar server handled as %+v", server)
	}
}

func TestClassifySurfacesUnmatchedProfileResources(t *testing.T) {
	inventory, err := Classify(testNaming(), []Resource{
		{Kind: KindVolume, ProviderID: "vol-old", Name: "legacy-store", Labels: map[string]string{LabelProfile: "profile-1"}},
		{Kind: KindVolume, ProviderID: "vol-other", Name: "someone-elses-disk"},
	})
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if len(inventory.Unmatched) != 1 || inventory.Unmatched[0].ProviderID != "vol-old" {
		t.Fatalf("unmatched %+v", inventory.Unmatched)
	}
}

func TestInventoryDigestTracksProviderIdentity(t *testing.T) {
	base := []Resource{{Kind: KindPrimaryIP, ProviderID: "ip-1", Name: "smallworlds-ip-dev", Labels: map[string]string{LabelProfile: "profile-1"}}}
	first, err := Classify(testNaming(), base)
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	same, err := Classify(testNaming(), base)
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if first.Digest != same.Digest || first.Digest == "" {
		t.Fatal("digest must be stable for the same inventory")
	}
	recreated, err := Classify(testNaming(), []Resource{{Kind: KindPrimaryIP, ProviderID: "ip-2", Name: "smallworlds-ip-dev", Labels: map[string]string{LabelProfile: "profile-1"}}})
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if recreated.Digest == first.Digest {
		t.Fatal("a re-created resource must change the digest")
	}
}

func TestSimilarNameDetection(t *testing.T) {
	for _, testCase := range []struct {
		candidate string
		expected  string
		similar   bool
	}{
		{"smallworlds-ip-dev", "smallworlds-ip-dev", false},
		{"smallworlds_ip_dev", "smallworlds-ip-dev", true},
		{"SmallWorlds-IP-Dev", "smallworlds-ip-dev", true},
		{"smallworlds-ip-dev-2", "smallworlds-ip-dev", true},
		{"smallworlds-ip", "smallworlds-ip-dev", true},
		{"unrelated", "smallworlds-ip-dev", false},
	} {
		t.Run(testCase.candidate, func(t *testing.T) {
			if got := similarName(testCase.candidate, testCase.expected); got != testCase.similar {
				t.Fatalf("similarName(%q,%q)=%v", testCase.candidate, testCase.expected, got)
			}
		})
	}
}
