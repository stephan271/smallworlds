---
status: accepted
---

# Bootstrap inputs are pins in the repository, not a packaged archive

What a new Cluster Node installs before Argo CD takes over — the K3s version,
the K3s install script and the Argo CD install manifest — is named in one file,
`infrastructure/bootstrap-pins.env`, as a version, an immutable URL and a
SHA-256 each. Every bootstrap path reads that file, downloads the two files on
the node, and refuses to continue if either is not byte for byte what was
pinned. There is no bootstrap archive any more: nothing is built, cached,
signed or attached to a release. This supersedes `docs/adr/0046`.

## Why

`docs/adr/0046` packaged those two files, with the bootstrap script, into a
signed archive on each GitHub Release. Its consumer was the Bootstrap Launcher,
a binary that had to verify what it downloaded against a key compiled into it.
`docs/adr/0051` replaced the launcher with a wizard that runs from a checkout,
and from then on the archive had no reader. The scripted route kept building
the same archive locally on every install, from a per-release lock file,
purely to unpack it again on the node.

That left three problems.

- **Machinery without a purpose.** A release needed a lock file, a packaging
  script, a publishing workflow and a signing key to produce an archive that no
  install downloaded. The checkout the Operator runs already *is* the reviewed
  release; a second signature over two files it names adds nothing.
- **Two routes that disagreed.** The local route installed pinned, checked
  inputs. The Hetzner route piped `https://get.k3s.io` into a shell and applied
  Argo CD from its `stable` branch, so the same release installed different
  software depending on where it ran and when.
- **A pin that drifted.** The lock named `https://get.k3s.io`, which serves the
  script on K3s's main branch. When upstream edited it, every install from the
  release failed its checksum before reaching the machine.

## How it works

- **One file.** `infrastructure/bootstrap-pins.env` holds six `KEY=value`
  lines. Shell sources it; Terraform parses it. It is versioned with the tree,
  so a release's pins are whatever the tag contains — there is no per-release
  copy to make.
- **Immutable URLs only.** The K3s installer is pinned by commit, the Argo CD
  manifest by tag. `admin-tools/test-bootstrap-pins.sh` rejects a moving
  address and, with `--online`, re-checks both digests against upstream.
- **The node verifies.** `bootstrap-local-node.sh` is copied to the machine
  with the pins beside it and fetches both files before it changes anything.
  The cloud-init template receives the same values from Terraform and does the
  same before K3s exists. The golden image build uses them too.
- **A release is a tag.** Cutting one no longer involves packaging, signing or
  attachments.

## Consequences

- The checksums travel with the script that checks them, instead of the files
  being verified on the Operator's computer first. Both come from the same
  checkout, so the trust is the same; what is lost is detection of a checkout
  tampered with between review and install, which the archive did not provide
  either once it was built locally.
- A Hetzner node now gets the pinned K3s and Argo CD rather than the newest
  upstream. The pins were moved to the versions upstream called stable on the
  day of this change (K3s v1.36.5+k3s1, Argo CD v3.5.4) so that rebuilding an
  existing node does not downgrade it. From here on, upgrading either is an
  edit to the pins file, and applies to new and rebuilt nodes only.
- The node needs `curl` and `sha256sum` and outbound HTTPS to GitHub during
  bootstrap. It needed the network already: the K3s installer downloads K3s.
- The deactivated Bootstrap Launcher can no longer install a new release: it
  fetches the archive this decision stops publishing, and its bootstrap script
  no longer accepts one. Its code, its publishing workflow and the release
  signing key are left in place with the rest of the console
  (`docs/adr/0051`). Reviving it means restoring the packaging from the tree at
  `v1.2.43` or giving it a reader for the pins.
- Releases up to `v1.2.43` keep their archives and lock files in their tags.
