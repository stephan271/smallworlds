package hetznerprovision_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/stephan271/smallworlds/operator-console/internal/hetzner"
	"github.com/stephan271/smallworlds/operator-console/internal/hetznerprovision"
)

// completePlan covers every resource kind an installation occupies, the way a
// real approved plan does. The volume is adopted, everything else is created or
// reused, so the renderer's create/adopt/read split is exercised in one pass.
func completePlan() hetzner.ChangePlan {
	plan := hetzner.ChangePlan{
		ProfileID: "profile-1",
		ProjectID: "project-9",
		Domain:    "example.org",
		Choice: hetzner.Choice{
			Tier: hetzner.PresetRecommended, Location: "nbg1", ServerType: "cx43", VolumeGB: 200,
		},
		Delegation: hetzner.Delegation{Domain: "example.org", Status: hetzner.DelegationConfirmed},
		Items: []hetzner.PlanItem{
			{Kind: hetzner.KindPrimaryIP, Name: "smallworlds-ip", Action: hetzner.ActionCreate},
			{Kind: hetzner.KindDNSZone, Name: "example.org", Action: hetzner.ActionReuseShared, ProviderID: "zone-1"},
			{Kind: hetzner.KindSSHKey, Name: hetzner.SharedAdminSSHKeyName, Action: hetzner.ActionReuseShared, ProviderID: "key-1"},
			{Kind: hetzner.KindFirewall, Name: "smallworlds-firewall", Action: hetzner.ActionCreate},
			{Kind: hetzner.KindVolume, Name: "smallworlds-data", Action: hetzner.ActionAdopt, ProviderID: "vol-77"},
			{Kind: hetzner.KindServer, Name: "cc-pilot-node-01", Action: hetzner.ActionCreate},
			{Kind: hetzner.KindReverseDNS, Name: "mail.example.org", Action: hetzner.ActionCreate},
		},
		InventoryDigest: inventoryDigest,
		Digest:          planDigest,
	}
	for _, record := range []string{"@", "identity", "files", "mail"} {
		plan.Items = append(plan.Items, hetzner.PlanItem{Kind: hetzner.KindDNSRecord, Name: record, Action: hetzner.ActionCreate})
	}
	return plan
}

func moduleInput() hetznerprovision.ModuleInput {
	return hetznerprovision.ModuleInput{
		CloudInit:       "#cloud-config\nruncmd:\n  - echo bootstrap\n",
		Access:          hetznerprovision.AdministrationAccess{Open: true, OperatorSources: []string{"198.51.100.7/32"}},
		ProviderVersion: "1.54.0",
	}
}

func renderComplete(t *testing.T, input hetznerprovision.ModuleInput) hetznerprovision.Module {
	t.Helper()
	module, err := hetznerprovision.RenderModule(mustBind(t), completePlan(), input)
	if err != nil {
		t.Fatalf("RenderModule: %v", err)
	}
	return module
}

func TestRenderModuleManagesOnlyApprovedResources(t *testing.T) {
	module := renderComplete(t, moduleInput())
	configuration := module.Files[hetznerprovision.ModuleFileName]

	for _, address := range []string{
		hetznerprovision.AddressPrimaryIP, hetznerprovision.AddressFirewall, hetznerprovision.AddressVolume,
		hetznerprovision.AddressServer, hetznerprovision.AddressRecords, hetznerprovision.AddressReverse,
		hetznerprovision.AddressAttach,
	} {
		if !contains(module.Managed, address) {
			t.Fatalf("managed addresses %v missing %q", module.Managed, address)
		}
	}
	// The shared zone and admin key are read, never managed: a destroy of this
	// profile must not be able to take a project-wide resource with it.
	if strings.Contains(configuration, `resource "hcloud_zone"`) || strings.Contains(configuration, `resource "hcloud_ssh_key"`) {
		t.Fatal("the shared DNS zone and admin SSH key must be data sources, not managed resources")
	}
	if !strings.Contains(configuration, `data "hcloud_zone" "existing"`) || !strings.Contains(configuration, `data "hcloud_ssh_key" "admin"`) {
		t.Fatal("the shared resources must be read as data sources")
	}
	// Every managed resource carries this profile's ownership labels, so the next
	// inspection classifies it as profile-owned rather than offering it to
	// another profile as adoptable.
	if strings.Count(configuration, "labels        = local.profile_labels")+strings.Count(configuration, "labels   = local.profile_labels")+strings.Count(configuration, "labels      = local.profile_labels")+strings.Count(configuration, "labels  = local.profile_labels")+strings.Count(configuration, "labels = local.profile_labels") < 5 {
		t.Fatalf("not every managed resource carries the profile ownership labels:\n%s", configuration)
	}
	if !strings.Contains(configuration, `"smallworlds-profile" = "profile-1"`) {
		t.Fatalf("profile ownership label missing:\n%s", configuration)
	}
}

func TestRenderModuleImportsOnlyExplicitAdoptions(t *testing.T) {
	module := renderComplete(t, moduleInput())
	if len(module.Imports) != 1 {
		t.Fatalf("imports = %+v, want exactly the approved volume adoption", module.Imports)
	}
	if module.Imports[0].Address != hetznerprovision.AddressVolume || module.Imports[0].ProviderID != "vol-77" {
		t.Fatalf("import = %+v, want the volume at its managed address", module.Imports[0])
	}
}

// An adoption the binding never approved must fail rendering rather than be
// silently created alongside — that is precisely how a duplicate paid resource
// appears.
func TestRenderModuleRefusesUnapprovedAdoption(t *testing.T) {
	binding := mustBind(t)
	binding.Adoptions = nil
	_, err := hetznerprovision.RenderModule(binding, completePlan(), moduleInput())
	if !errors.Is(err, hetznerprovision.ErrInvalidModule) {
		t.Fatalf("err = %v, want ErrInvalidModule", err)
	}
}

func TestRenderModuleRefusesPlanThatIsNotTheApprovedOne(t *testing.T) {
	plan := completePlan()
	plan.Digest = strings.Repeat("e", 64)
	if _, err := hetznerprovision.RenderModule(mustBind(t), plan, moduleInput()); !errors.Is(err, hetznerprovision.ErrInvalidModule) {
		t.Fatalf("err = %v, want a plan that is not the approved one to be unrenderable", err)
	}
}

func TestRenderModuleRefusesBlockedPlanAndBadInput(t *testing.T) {
	blocked := completePlan()
	blocked.Blockers = []hetzner.Blocker{{Code: "ownership-conflict"}}
	if _, err := hetznerprovision.RenderModule(mustBind(t), blocked, moduleInput()); !errors.Is(err, hetznerprovision.ErrInvalidModule) {
		t.Fatalf("err = %v, want a blocked plan to be unrenderable", err)
	}
	for name, mutate := range map[string]func(*hetznerprovision.ModuleInput){
		"no cloud-init":          func(i *hetznerprovision.ModuleInput) { i.CloudInit = "  " },
		"provider version range": func(i *hetznerprovision.ModuleInput) { i.ProviderVersion = "~> 1.54" },
		"malformed source range": func(i *hetznerprovision.ModuleInput) { i.Access.OperatorSources = []string{"; rm -rf /"} },
	} {
		t.Run(name, func(t *testing.T) {
			input := moduleInput()
			mutate(&input)
			if _, err := hetznerprovision.RenderModule(mustBind(t), completePlan(), input); !errors.Is(err, hetznerprovision.ErrInvalidModule) {
				t.Fatalf("err = %v, want ErrInvalidModule", err)
			}
		})
	}
}

// The plan may not cover a resource kind partially: a missing server or volume
// would render a configuration that silently provisions less than the operator
// approved and paid for.
func TestRenderModuleRefusesIncompletePlan(t *testing.T) {
	for _, kind := range []hetzner.ResourceKind{hetzner.KindServer, hetzner.KindVolume, hetzner.KindPrimaryIP, hetzner.KindDNSZone, hetzner.KindFirewall} {
		plan := completePlan()
		kept := plan.Items[:0]
		for _, item := range plan.Items {
			if item.Kind != kind {
				kept = append(kept, item)
			}
		}
		plan.Items = kept
		if _, err := hetznerprovision.RenderModule(mustBind(t), plan, moduleInput()); !errors.Is(err, hetznerprovision.ErrInvalidModule) {
			t.Fatalf("dropping %s: err = %v, want ErrInvalidModule", kind, err)
		}
	}
}

// The temporary administration path is the one thing that widens exposure, so
// its three states must each be visible in the rendered configuration.
func TestRenderModuleScopesTemporaryAdministrationAccess(t *testing.T) {
	scoped := renderComplete(t, moduleInput()).Files[hetznerprovision.ModuleFileName]
	if !strings.Contains(scoped, `"198.51.100.7/32"`) {
		t.Fatalf("scoped administration source missing:\n%s", scoped)
	}
	if strings.Count(scoped, `port      = "22"`) != 1 || strings.Count(scoped, `port      = "6443"`) != 1 {
		t.Fatal("the open administration path must render exactly one SSH and one Kubernetes API rule")
	}
	// The service ports the community genuinely needs stay public and are not
	// affected by scoping the administration path.
	for _, port := range []string{"80", "443", "25", "587", "993", "10000"} {
		if !strings.Contains(scoped, `port      = "`+port+`"`) {
			t.Fatalf("public service port %s missing from the firewall", port)
		}
	}

	input := moduleInput()
	input.Access = hetznerprovision.AdministrationAccess{Open: true}
	unscoped := renderComplete(t, input).Files[hetznerprovision.ModuleFileName]
	if !strings.Contains(unscoped, "open to the internet") {
		t.Fatal("an unscoped administration path must say so in the configuration")
	}

	input.Access = hetznerprovision.AdministrationAccess{}
	closed := renderComplete(t, input).Files[hetznerprovision.ModuleFileName]
	if strings.Contains(closed, `port      = "22"`) || strings.Contains(closed, `port      = "6443"`) {
		t.Fatalf("a closed administration path must render no SSH or Kubernetes API rule:\n%s", closed)
	}
	if !strings.Contains(closed, `port      = "443"`) {
		t.Fatal("closing the administration path must not close the community's own services")
	}
}

// The project token reaches the provider through the environment. A token
// written into the configuration would also land in the plan file, the state,
// and any diff the operator is shown.
func TestRenderModuleNeverInlinesCredentials(t *testing.T) {
	module := renderComplete(t, moduleInput())
	configuration := module.Files[hetznerprovision.ModuleFileName]
	if !strings.Contains(configuration, `variable "hcloud_token"`) || !strings.Contains(configuration, "sensitive = true") {
		t.Fatal("the project token must be a declared sensitive variable")
	}
	if strings.Contains(configuration, "token = \"") {
		t.Fatalf("a literal token appears in the configuration:\n%s", configuration)
	}
	if !strings.Contains(configuration, "sensitive   = true") {
		t.Fatal("outputs that can carry device or credential detail must be marked sensitive")
	}
	if module.Files[hetznerprovision.CloudInitFileName] != moduleInput().CloudInit {
		t.Fatal("the cloud-init payload must be written beside the configuration, not interpolated into it")
	}
	if !strings.Contains(configuration, `user_data = file("${path.module}/`+hetznerprovision.CloudInitFileName+`")`) {
		t.Fatal("the server must read cloud-init from the module file")
	}
}

// Rendering is deterministic: the same approved plan must produce byte-identical
// configuration, or OpenTofu would see changes on every reconciliation.
func TestRenderModuleIsDeterministic(t *testing.T) {
	first := renderComplete(t, moduleInput())
	second := renderComplete(t, moduleInput())
	if first.Digest != second.Digest {
		t.Fatal("rendering the same plan twice produced different configuration")
	}
	if first.Files[hetznerprovision.ModuleFileName] != second.Files[hetznerprovision.ModuleFileName] {
		t.Fatal("configuration is not byte-identical across renders")
	}

	input := moduleInput()
	input.Access.OperatorSources = []string{"203.0.113.0/24"}
	if changed := renderComplete(t, input); changed.Digest == first.Digest {
		t.Fatal("changing the administration scope must change the module digest")
	}
}

// Names come from the plan and are already validated, but a name carrying a
// quote or an interpolation sigil must not be able to close the string and
// append configuration of its own.
func TestRenderModuleEscapesHCLStrings(t *testing.T) {
	plan := completePlan()
	for index := range plan.Items {
		if plan.Items[index].Kind == hetzner.KindServer {
			plan.Items[index].Name = `evil" ${var.hcloud_token} %{if true}`
		}
	}
	binding := mustBind(t)
	binding.PlanDigest = plan.Digest
	module, err := hetznerprovision.RenderModule(binding, plan, moduleInput())
	if err != nil {
		t.Fatalf("RenderModule: %v", err)
	}
	configuration := module.Files[hetznerprovision.ModuleFileName]
	// The sigils are doubled and the quote escaped, so HCL reads the whole thing
	// as one literal name rather than closing the string, expanding the token
	// variable, and opening a template block.
	if !strings.Contains(configuration, `name        = "evil\" $${var.hcloud_token} %%{if true}"`) {
		t.Fatalf("name was not escaped into a single literal:\n%s", configuration)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
