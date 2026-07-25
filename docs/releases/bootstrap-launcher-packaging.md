# Bootstrap Launcher native release packaging

The Bootstrap Launcher is released as a self-contained native archive for each
supported Launcher Host. Extract the archive and run the included executable;
the Operator does not need Git, GitHub CLI, OpenTofu, Terraform, Kubernetes
tools, Node.js, or another JavaScript runtime.

| Launcher Host | Artifact |
| --- | --- |
| Linux x86-64 | `smallworlds-bootstrap-launcher_vX.Y.Z_linux_amd64.tar.gz` |
| Linux ARM64 | `smallworlds-bootstrap-launcher_vX.Y.Z_linux_arm64.tar.gz` |
| macOS Intel | `smallworlds-bootstrap-launcher_vX.Y.Z_darwin_amd64.tar.gz` |
| macOS Apple Silicon | `smallworlds-bootstrap-launcher_vX.Y.Z_darwin_arm64.tar.gz` |
| Windows x86-64 | `smallworlds-bootstrap-launcher_vX.Y.Z_windows_amd64.zip` |

Every artifact embeds exactly the Svelte client built for that release and the
versioned Go API. All five variants can provision a remote Linux Cluster Node
over SSH. The same-host Local option is deliberately offered only by a Linux
launcher, where its inspection and privilege model are supported.

## Integrity and provenance

Each GitHub Release contains all five archives, `SHA256SUMS`, its Ed25519
signature `SHA256SUMS.sig`, the corresponding public key
`SHA256SUMS.pub`, an SPDX 2.3 SBOM, and `THIRD-PARTY-NOTICES.txt`. Verify the
archive digest against `SHA256SUMS` before extraction. The release workflow
signs the checksum manifest only after it has built and tested the embedded
client and Go launcher at the immutable release tag. Verify the public-key
fingerprint against the project release-signing record before trusting a
signature downloaded from a release page.

The packaging command is deterministic where practical: it uses the tagged
commit timestamp, `-trimpath`, no VCS build metadata, normalized archive
ownership/timestamps, and a deterministic gzip stream. Rebuilding a tag with
the same Go and web toolchains should produce matching checksums.

Release maintainers run the workflow **Publish Bootstrap Launcher** against an
existing tag. A non-publishing run creates a temporary signing key and uploads
the result as a validation artifact. A publishing run uses the protected
`SMALLWORLDS_RELEASE_ED25519_PRIVATE_KEY_B64` secret and attaches the signed
files to that tag's GitHub Release.

For local release-engineering diagnosis only:

```bash
cd operator-console/web
npm ci && npm run generate:api && npm run check && npm run build

cd ../..
admin-tools/package-bootstrap-launcher.sh \
  --version vX.Y.Z \
  --output-directory /tmp/smallworlds-bootstrap-launcher
```

## Runtime guarantees and failure guidance

The binary runs as one unprivileged process per operating-system user. A second
launch reads the protected loopback rendezvous and opens the existing local
session; it never starts a competing process. Closing a browser does not stop
the process or a durable Workflow Run. The release installs no service and no
auto-start entry.

The platform adapters are contract-tested for loopback browser launch,
per-user rendezvous permissions (including the Windows current-user ACL), the
native credential-store path and passphrase fallback, explicit elevation for
Tailscale/trust operations, and a documented manual Tailscale fallback. Managed
bootstrap downloads come only from the compiled, signed, checksummed catalog.

| Situation | English guidance | Deutsche Hilfe |
| --- | --- | --- |
| Archive will not extract or checksum differs | Delete the archive, download it and `SHA256SUMS` again from the same release, then verify the checksum before running it. | Archiv löschen, Archiv und `SHA256SUMS` erneut aus derselben Release laden und die Prüfsumme vor dem Start prüfen. |
| Browser does not open | The launcher remains on loopback. Copy the URL printed by the launcher into a local browser; no network listener is exposed. | Der Launcher bleibt auf Loopback. Die ausgegebene URL in einen lokalen Browser kopieren; es wird kein Netzwerkdienst freigegeben. |
| Signed bootstrap asset cannot download | Internet access to the release attachment is required for initial bootstrap. Check the connection or proxy, then retry; do not substitute an arbitrary URL or tool. | Für den ersten Bootstrap ist Internetzugang zum Release-Anhang nötig. Verbindung oder Proxy prüfen und erneut versuchen; keine beliebige URL oder ein fremdes Werkzeug verwenden. |
| Tailscale or device trust needs installation | Review the explicit elevation prompt or use the linked official manual installation instructions. | Die explizite Erhöhungsabfrage prüfen oder die verlinkte offizielle manuelle Installation verwenden. |

The Linux, macOS, and Windows release matrix is built on Linux through Go's
cross compiler. Cross-platform workflow/API, profile, Recovery Bundle, and
remote Local-node compatibility are preserved by the shared Go persistence and
API contracts; OS-specific operations remain behind their platform adapters.
