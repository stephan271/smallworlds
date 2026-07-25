package hetznerprovision

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/stephan271/smallworlds/operator-console/internal/hetzner"
)

// ErrInvalidModule is returned when an approved plan cannot be rendered into a
// configuration that provably touches only what was approved.
var ErrInvalidModule = errors.New("hetzner provisioning module is invalid")

// ModuleFileName is the single configuration file the module renders to, plus
// the cloud-init payload the server boots from. Both are written into the
// profile's isolated workspace, never into a shared directory.
const (
	ModuleFileName    = "smallworlds.tf"
	CloudInitFileName = "cloud-init.yaml"
)

// Resource addresses are fixed rather than derived, so a configuration rendered
// twice from the same plan produces the same addresses and OpenTofu sees no
// spurious replacement.
const (
	AddressPrimaryIP = "hcloud_primary_ip.main"
	AddressFirewall  = "hcloud_firewall.k8s"
	AddressVolume    = "hcloud_volume.data"
	AddressServer    = "hcloud_server.node"
	AddressRecords   = "hcloud_zone_rrset.records"
	AddressReverse   = "hcloud_rdns.mail"
	AddressAttach    = "hcloud_volume_attachment.data"
)

// AdministrationAccess describes how the temporary public administration path
// (SSH and the Kubernetes API) is exposed while the private handoff is being
// established. Scoping it to the Operator's own address is preferred; an
// operator behind a changing address may have to accept a wider range, which is
// why the wider case is explicit rather than a silent default.
type AdministrationAccess struct {
	// Open is false once the temporary path has been closed. A closed path
	// renders no SSH or Kubernetes API rule at all.
	Open bool `json:"open"`
	// OperatorSources are the CIDR ranges permitted to reach SSH and the
	// Kubernetes API. Empty with Open true means unscoped public access, which
	// the renderer records in the module so it is visible in a diff.
	OperatorSources []string `json:"operatorSources,omitempty"`
}

// Scoped reports whether the temporary path is restricted to the Operator
// rather than open to the internet.
func (access AdministrationAccess) Scoped() bool {
	return access.Open && len(access.OperatorSources) > 0
}

// ModuleInput is everything the renderer needs beyond the approved plan itself.
type ModuleInput struct {
	// CloudInit is the bootstrap payload the server boots from. It is written
	// beside the configuration and referenced by path, so no cluster secret is
	// ever interpolated into the OpenTofu configuration or its state as a plain
	// literal the state file would then carry twice.
	CloudInit string
	// Access controls the temporary public administration path.
	Access AdministrationAccess
	// ProviderVersion is the exact pinned hcloud provider version. An operator
	// approving a reproducible plan gets exactly one provider version, so the
	// constraint is an equality rather than a range.
	ProviderVersion string
}

// Module is the rendered configuration for one approved plan.
type Module struct {
	// Files maps a file name to its contents. Callers write them into the
	// profile's locked workspace.
	Files map[string]string `json:"-"`
	// Managed are the resource addresses this configuration will create or keep.
	Managed []string `json:"managed"`
	// Imports are the explicit adoptions: an existing provider resource brought
	// under management at a fixed address. Nothing is imported that the operator
	// did not choose.
	Imports []Import `json:"imports"`
	// Digest binds the rendered configuration, so a run can prove it applied the
	// configuration it rendered.
	Digest string `json:"digest"`
}

// Import is one explicitly approved adoption.
type Import struct {
	Address    string `json:"address"`
	ProviderID string `json:"providerId"`
}

// RenderModule turns an approved, still-bound Change Plan into an OpenTofu
// configuration.
//
// Two properties are the reason this is generated rather than templated from a
// checked-in root: the configuration contains a resource block for exactly the
// items the operator approved creating, an import block for exactly the ones
// they approved adopting, and a data block for the shared resources it may only
// read — so "only approved profile resources" is a property of the file rather
// than a promise about the runtime. And every managed resource carries this
// profile's ownership labels, so the next inspection classifies it as
// profile-owned instead of offering it to another profile as adoptable.
func RenderModule(binding Binding, plan hetzner.ChangePlan, input ModuleInput) (Module, error) {
	if err := binding.Validate(); err != nil {
		return Module{}, fmt.Errorf("%w: binding", ErrInvalidModule)
	}
	if plan.Digest == "" || plan.Digest != binding.PlanDigest {
		return Module{}, fmt.Errorf("%w: plan does not match the approved binding", ErrInvalidModule)
	}
	if !plan.Approvable() {
		return Module{}, fmt.Errorf("%w: plan is blocked", ErrInvalidModule)
	}
	if strings.TrimSpace(input.CloudInit) == "" {
		return Module{}, fmt.Errorf("%w: cloud-init payload", ErrInvalidModule)
	}
	if !pinnedVersion.MatchString(input.ProviderVersion) {
		return Module{}, fmt.Errorf("%w: provider version must be pinned exactly", ErrInvalidModule)
	}
	for _, source := range input.Access.OperatorSources {
		if !safeCIDR.MatchString(source) {
			return Module{}, fmt.Errorf("%w: operator source range", ErrInvalidModule)
		}
	}

	items, err := indexItems(binding, plan)
	if err != nil {
		return Module{}, err
	}
	builder := &moduleBuilder{binding: binding, plan: plan, input: input, items: items}
	configuration, err := builder.render()
	if err != nil {
		return Module{}, err
	}
	module := Module{
		Files:   map[string]string{ModuleFileName: configuration, CloudInitFileName: input.CloudInit},
		Managed: builder.managed,
		Imports: builder.imports,
	}
	module.Digest = moduleDigest(module.Files)
	return module, nil
}

// itemIndex groups the approved plan items by kind so the renderer can answer
// "was this approved?" per resource rather than trusting the plan's order.
type itemIndex struct {
	byKind map[hetzner.ResourceKind][]hetzner.PlanItem
}

func (index itemIndex) single(kind hetzner.ResourceKind) (hetzner.PlanItem, bool) {
	items := index.byKind[kind]
	if len(items) != 1 {
		return hetzner.PlanItem{}, false
	}
	return items[0], true
}

// indexItems validates that every plan item carries an action the renderer can
// honour, and that every adoption in the plan is one the binding approved. An
// item whose action the renderer does not understand is a refusal, never a
// skip: skipping would quietly drop a resource the operator was told about.
func indexItems(binding Binding, plan hetzner.ChangePlan) (itemIndex, error) {
	index := itemIndex{byKind: map[hetzner.ResourceKind][]hetzner.PlanItem{}}
	for _, item := range plan.Items {
		switch item.Action {
		case hetzner.ActionCreate, hetzner.ActionKeep, hetzner.ActionReuseShared:
		case hetzner.ActionAdopt:
			if item.ProviderID == "" || !binding.Adopted(item.ProviderID) {
				return itemIndex{}, fmt.Errorf("%w: adoption of %s was not approved", ErrInvalidModule, item.Name)
			}
		default:
			return itemIndex{}, fmt.Errorf("%w: unsupported action %q", ErrInvalidModule, item.Action)
		}
		index.byKind[item.Kind] = append(index.byKind[item.Kind], item)
	}
	for _, kind := range []hetzner.ResourceKind{hetzner.KindPrimaryIP, hetzner.KindDNSZone, hetzner.KindSSHKey, hetzner.KindFirewall, hetzner.KindVolume, hetzner.KindServer} {
		if _, ok := index.single(kind); !ok {
			return itemIndex{}, fmt.Errorf("%w: plan does not cover exactly one %s", ErrInvalidModule, kind)
		}
	}
	return index, nil
}

type moduleBuilder struct {
	binding Binding
	plan    hetzner.ChangePlan
	input   ModuleInput
	items   itemIndex
	managed []string
	imports []Import
}

func (builder *moduleBuilder) render() (string, error) {
	var out strings.Builder
	builder.header(&out)
	if err := builder.shared(&out); err != nil {
		return "", err
	}
	if err := builder.primaryIP(&out); err != nil {
		return "", err
	}
	if err := builder.firewall(&out); err != nil {
		return "", err
	}
	if err := builder.volume(&out); err != nil {
		return "", err
	}
	if err := builder.server(&out); err != nil {
		return "", err
	}
	if err := builder.dns(&out); err != nil {
		return "", err
	}
	builder.outputs(&out)
	sort.Strings(builder.managed)
	sort.Slice(builder.imports, func(left, right int) bool {
		return builder.imports[left].Address < builder.imports[right].Address
	})
	return out.String(), nil
}

func (builder *moduleBuilder) header(out *strings.Builder) {
	fmt.Fprintf(out, `# Generated by the SmallWorlds Operator Console for Cluster Profile %s.
# Rendered from approved plan %s (inventory %s). Do not edit: the launcher
# re-renders this file from the approved plan on every reconciliation, and any
# resource not present here is one the Operator did not approve.

terraform {
  required_providers {
    hcloud = {
      source  = "hetznercloud/hcloud"
      version = %s
    }
  }
}

# The project token is supplied through the environment by the launcher and is
# never written into this configuration, a variables file, or a log.
variable "hcloud_token" {
  type      = string
  sensitive = true
}

provider "hcloud" {
  token = var.hcloud_token
}

locals {
  profile_labels = {
    %s = %s
    %s = "true"
  }
}

`,
		builder.binding.ProfileID, shortDigest(builder.plan.Digest), shortDigest(builder.plan.InventoryDigest),
		hclString(builder.input.ProviderVersion),
		hclString(hetzner.LabelProfile), hclString(builder.binding.ProfileID), hclString(hetzner.LabelManaged))
}

// shared renders the two project-wide resources as data sources. They are read,
// never managed: the DNS zone and the admin SSH key are shared by every profile
// in the project, so a destroy of this profile must not be able to take them
// with it.
func (builder *moduleBuilder) shared(out *strings.Builder) error {
	zone, ok := builder.items.single(hetzner.KindDNSZone)
	if !ok || zone.Action != hetzner.ActionReuseShared {
		return fmt.Errorf("%w: the DNS zone must be reused, not owned", ErrInvalidModule)
	}
	key, ok := builder.items.single(hetzner.KindSSHKey)
	if !ok || key.Action != hetzner.ActionReuseShared {
		return fmt.Errorf("%w: the shared admin SSH key must be reused, not owned", ErrInvalidModule)
	}
	fmt.Fprintf(out, `# Shared across every profile in this project: read only, never managed here.
data "hcloud_zone" "existing" {
  name = %s
}

data "hcloud_ssh_key" "admin" {
  name = %s
}

`, hclString(zone.Name), hclString(key.Name))
	return nil
}

func (builder *moduleBuilder) primaryIP(out *strings.Builder) error {
	item, _ := builder.items.single(hetzner.KindPrimaryIP)
	builder.claim(AddressPrimaryIP, item)
	fmt.Fprintf(out, `resource "hcloud_primary_ip" "main" {
  name          = %s
  type          = "ipv4"
  datacenter    = %s
  assignee_type = "server"
  auto_delete   = false
  labels        = local.profile_labels
}

`, hclString(item.Name), hclString(builder.plan.Choice.Location))
	return nil
}

// firewall renders the ingress rules. The public service ports are always open;
// SSH and the Kubernetes API are the temporary administration path, so they are
// rendered only while it is open and are scoped to the Operator's own ranges
// when one is known.
func (builder *moduleBuilder) firewall(out *strings.Builder) error {
	item, _ := builder.items.single(hetzner.KindFirewall)
	builder.claim(AddressFirewall, item)
	fmt.Fprintf(out, `resource "hcloud_firewall" "k8s" {
  name   = %s
  labels = local.profile_labels
`, hclString(item.Name))
	for _, rule := range publicServiceRules {
		writeFirewallRule(out, rule.protocol, rule.port, publicSources, rule.comment)
	}
	if builder.input.Access.Open {
		sources, comment := publicSources, "temporary public administration path: open to the internet"
		if builder.input.Access.Scoped() {
			sources, comment = builder.input.Access.OperatorSources, "temporary administration path, scoped to the Operator"
		}
		writeFirewallRule(out, "tcp", "22", sources, comment)
		writeFirewallRule(out, "tcp", "6443", sources, comment)
	}
	out.WriteString("}\n\n")
	return nil
}

func (builder *moduleBuilder) volume(out *strings.Builder) error {
	item, _ := builder.items.single(hetzner.KindVolume)
	builder.claim(AddressVolume, item)
	// The data volume holds everything the community cannot recreate, so it is
	// protected against destroy and its growth is one-way by the provider's own
	// rules. Both facts were stated in the cost estimate the Operator approved.
	fmt.Fprintf(out, `resource "hcloud_volume" "data" {
  name     = %s
  size     = %d
  location = %s
  format   = "ext4"
  labels   = local.profile_labels

  lifecycle {
    prevent_destroy = true
  }
}

resource "hcloud_volume_attachment" "data" {
  volume_id = hcloud_volume.data.id
  server_id = hcloud_server.node.id
  automount = true
}

`, hclString(item.Name), builder.plan.Choice.VolumeGB, hclString(builder.plan.Choice.Location))
	builder.managed = append(builder.managed, AddressAttach)
	return nil
}

func (builder *moduleBuilder) server(out *strings.Builder) error {
	item, _ := builder.items.single(hetzner.KindServer)
	builder.claim(AddressServer, item)
	fmt.Fprintf(out, `resource "hcloud_server" "node" {
  name        = %s
  image       = "ubuntu-24.04"
  server_type = %s
  location    = %s
  labels      = local.profile_labels

  ssh_keys     = [data.hcloud_ssh_key.admin.id]
  firewall_ids = [hcloud_firewall.k8s.id]

  user_data = file("${path.module}/%s")

  public_net {
    ipv4         = hcloud_primary_ip.main.id
    ipv6_enabled = true
  }

  lifecycle {
    ignore_changes = [user_data]
  }
}

`, hclString(item.Name), hclString(builder.plan.Choice.ServerType), hclString(builder.plan.Choice.Location), CloudInitFileName)
	return nil
}

// dns renders one A record per approved record name and the reverse DNS entry.
// The names come from the plan, so a record the Operator did not see in the plan
// cannot appear in the zone.
func (builder *moduleBuilder) dns(out *strings.Builder) error {
	records := builder.items.byKind[hetzner.KindDNSRecord]
	if len(records) == 0 {
		return fmt.Errorf("%w: plan covers no DNS records", ErrInvalidModule)
	}
	names := make([]string, 0, len(records))
	for _, record := range records {
		names = append(names, record.Name)
		if record.Action == hetzner.ActionAdopt {
			builder.imports = append(builder.imports, Import{Address: fmt.Sprintf("%s[%q]", AddressRecords, record.Name), ProviderID: record.ProviderID})
		}
	}
	sort.Strings(names)
	quoted := make([]string, 0, len(names))
	for _, name := range names {
		quoted = append(quoted, "    "+hclString(name))
	}
	builder.managed = append(builder.managed, AddressRecords)
	fmt.Fprintf(out, `resource "hcloud_zone_rrset" "records" {
  for_each = toset([
%s
  ])

  zone    = data.hcloud_zone.existing.id
  name    = each.value
  type    = "A"
  ttl     = 3600
  labels  = local.profile_labels
  records = [{ value = hcloud_primary_ip.main.ip_address }]
}

`, strings.Join(quoted, ",\n"))

	reverse, ok := builder.items.single(hetzner.KindReverseDNS)
	if !ok {
		return fmt.Errorf("%w: plan does not cover exactly one reverse DNS entry", ErrInvalidModule)
	}
	builder.claim(AddressReverse, reverse)
	fmt.Fprintf(out, `resource "hcloud_rdns" "mail" {
  primary_ip_id = hcloud_primary_ip.main.id
  ip_address    = hcloud_primary_ip.main.ip_address
  dns_ptr       = %s
}

`, hclString(reverse.Name))
	return nil
}

// outputs exposes only what the launcher needs to continue the journey, and
// marks anything that could carry credential material sensitive so OpenTofu
// itself refuses to print it.
func (builder *moduleBuilder) outputs(out *strings.Builder) {
	out.WriteString(`output "server_ipv4" {
  value       = hcloud_primary_ip.main.ip_address
  description = "The public address of the node."
}

output "server_id" {
  value       = hcloud_server.node.id
  description = "The stable provider identity of the node."
}

output "data_volume_device" {
  value       = hcloud_volume.data.linux_device
  description = "The Linux device name for the persistent data volume."
  sensitive   = true
}
`)
}

// claim records a resource as managed and, when the plan adopts it, records the
// explicit import. An adopted resource is imported at exactly the address the
// renderer manages it at, so adoption cannot silently become "create a second
// one alongside".
func (builder *moduleBuilder) claim(address string, item hetzner.PlanItem) {
	builder.managed = append(builder.managed, address)
	if item.Action == hetzner.ActionAdopt {
		builder.imports = append(builder.imports, Import{Address: address, ProviderID: item.ProviderID})
	}
}

type firewallRule struct {
	protocol string
	port     string
	comment  string
}

// publicServiceRules are the ports the community's services genuinely need from
// the internet. They are fixed here rather than configurable: they mirror
// infrastructure/terraform/main.tf, and a plan cannot widen them.
var publicServiceRules = []firewallRule{
	{"tcp", "80", "HTTP (ACME challenge and redirect)"},
	{"tcp", "443", "HTTPS"},
	{"tcp", "25", "SMTP"},
	{"tcp", "587", "mail submission"},
	{"tcp", "993", "IMAPS"},
	{"udp", "10000", "Jitsi video bridge"},
}

var publicSources = []string{"0.0.0.0/0", "::/0"}

func writeFirewallRule(out *strings.Builder, protocol, port string, sources []string, comment string) {
	quoted := make([]string, 0, len(sources))
	for _, source := range sources {
		quoted = append(quoted, "      "+hclString(source))
	}
	// The attribute alignment matches `tofu fmt`, so an operator reading the
	// generated file sees the same shape as a hand-written one and a diff of two
	// renders shows only real changes.
	fmt.Fprintf(out, `
  # %s
  rule {
    direction = "in"
    protocol  = %s
    port      = %s
    source_ips = [
%s
    ]
  }
`, comment, hclString(protocol), hclString(port), strings.Join(quoted, ",\n"))
}

// hclString quotes a value for HCL. Every value reaching it has already been
// validated by the plan or the binding; escaping is the second line of defence
// so a name that somehow carried a quote cannot close the string and append
// configuration of its own.
func hclString(value string) string {
	runes := []rune(value)
	var escaped strings.Builder
	escaped.WriteByte('"')
	for index, character := range runes {
		next := rune(0)
		if index+1 < len(runes) {
			next = runes[index+1]
		}
		switch {
		case character == '"' || character == '\\':
			escaped.WriteByte('\\')
			escaped.WriteRune(character)
		case (character == '$' || character == '%') && next == '{':
			// ${...} is HCL interpolation and %{...} a template directive; both
			// are escaped by doubling the sigil.
			escaped.WriteRune(character)
			escaped.WriteRune(character)
		case character < 0x20 || character == 0x7f:
			escaped.WriteString(fmt.Sprintf("\\u%04x", character))
		default:
			escaped.WriteRune(character)
		}
	}
	escaped.WriteByte('"')
	return escaped.String()
}

func shortDigest(digest string) string {
	if len(digest) > 12 {
		return digest[:12]
	}
	return digest
}

func moduleDigest(files map[string]string) string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	hash := sha256.New()
	for _, name := range names {
		hash.Write([]byte(name))
		hash.Write([]byte{0})
		hash.Write([]byte(strconv.Itoa(len(files[name]))))
		hash.Write([]byte{0})
		hash.Write([]byte(files[name]))
	}
	return hex.EncodeToString(hash.Sum(nil))
}
