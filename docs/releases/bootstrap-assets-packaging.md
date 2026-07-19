# Build a bootstrap release payload

Use this release-engineering command to construct the first signed Linux amd64
bootstrap payload. It is deterministic: identical reviewed inputs produce the
same archive bytes, and no value defaults to a floating upstream release.

The command takes the K3s installer and Argo CD install manifest as explicit
inputs. Obtain their exact versioned HTTPS release URLs and SHA-256 checksums
from the relevant upstream release records, review them, then run:

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

The release engineer must next upload that exact archive to an immutable direct
HTTPS location, calculate its SHA-256, sign that digest with the private
SmallWorlds Ed25519 release key, and publish the public manifest as described
in [the publication contract](bootstrap-assets-publication.md). Do not commit
the private key or a production archive to this repository.

For a local structural and reproducibility check, run:

```bash
admin-tools/test-build-bootstrap-assets.sh
```
