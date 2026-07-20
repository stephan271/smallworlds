# Build a bootstrap release payload

Use the manually-triggered **Publish Bootstrap Assets** GitHub Action to
construct and publish the first signed Linux amd64 bootstrap payload. It reads a
reviewed input lock and produces the same archive bytes from identical inputs.

This is not an Operator setup step. It is run by a SmallWorlds release
maintainer before a GitHub Release is published. Cluster Operators later select
only the SmallWorlds release in the Launcher.

Before running the Action, a maintainer adds a reviewed input lock at
`docs/releases/bootstrap-inputs/<release>.json`, using the format documented in
[the input-lock directory](bootstrap-inputs/README.md). The Action's default
non-publishing mode proves that the lock can build and sign an archive using a
temporary key. Selecting `publish: true` requires the repository secret
`SMALLWORLDS_RELEASE_ED25519_PRIVATE_KEY_B64`; it then attaches the archive,
checksum, signature, and public manifest to the matching existing tag's GitHub
Release. The one-time setup is documented in
[the signing setup guide](github-release-signing-setup.md).

The underlying local command remains available for release-maintainer diagnosis:

```bash
admin-tools/build-bootstrap-assets.sh \
  --release v1.2.24 \
  --k3s-version vX.Y.Z+k3sN \
  --k3s-installer-url https://example.org/path/to/k3s-install.sh \
  --k3s-installer-sha256 64-lowercase-hex-characters \
  --argocd-version vX.Y.Z \
  --argocd-manifest-url https://example.org/path/to/argocd-install.yaml \
  --argocd-manifest-sha256 64-lowercase-hex-characters \
  --output-directory dist/bootstrap-assets
```

This writes `smallworlds-bootstrap-v1.2.24-linux-amd64.tar.gz`. Its contents
are the release-pinned local bootstrap script, a runner that identifies the
asset directory, K3s and Argo CD inputs, and `metadata.json` recording their
versions, URLs, and digests. It contains no credentials, operator configuration,
secrets, kubeconfigs, or signing key.

This is the release-packaging foundation only. The current Local Node workflow
does not yet consume these packaged inputs; wiring the verified payload into
that workflow belongs to the later bootstrap issue. Consequently it neither
claims an offline installation nor makes the legacy script's network downloads
safe by itself.

Do not commit the private key or a production archive to this repository.

For a local structural and reproducibility check, run:

```bash
admin-tools/test-build-bootstrap-assets.sh
```
