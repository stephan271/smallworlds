# One-time GitHub Release signing setup

Do this once, only when the release workflow and an initial reviewed input lock
are ready. It is not needed to run the Operator Console or create a cluster.

## 1. Create and protect the signing material

On a trusted Linux machine, outside the SmallWorlds repository, run:

```bash
mkdir -p ~/smallworlds-release-keys
cd ~/smallworlds-release-keys
/path/to/smallworlds/admin-tools/generate-release-signing-key.sh \
  --output-directory ./first-key
```

The command creates three files. Keep the whole `first-key` directory private,
make an encrypted backup, and never commit or send its private files:

| File | Purpose | Who may receive it |
| --- | --- | --- |
| `smallworlds-release-ed25519.pem` | Actual private signing key | Nobody except the protected release process |
| `smallworlds-release-ed25519-private.b64` | The same private key in a form GitHub Actions accepts | GitHub Actions repository secret only |
| `smallworlds-release-ed25519-public.b64` | Public verification key | Safe to give to the Launcher source/release catalog |

## 2. Add the private key to GitHub Actions

In `stephan271/smallworlds`, open **Settings** → **Secrets and variables** →
**Actions** → **New repository secret**. Create exactly this secret:

```text
Name:  SMALLWORLDS_RELEASE_ED25519_PRIVATE_KEY_B64
Value: contents of smallworlds-release-ed25519-private.b64
```

Do not create a personal access token for this workflow. Its checked-in
workflow requests GitHub's built-in `GITHUB_TOKEN` with repository contents
write permission only while it publishes a release.

If repository policy prevents the workflow from publishing, a repository owner
must allow GitHub Actions workflow permissions to use **Read and write**
contents. Do not broaden the permission beyond this repository.

## 3. Give the public value to the release catalog

Provide the single line in `smallworlds-release-ed25519-public.b64` to the
maintainer implementing Issue 7. It is not secret. The Launcher compiles this
public key into its trust catalog; that is what lets it reject a downloaded
archive that was not signed by your private key.

## 4. First safe workflow run

When a reviewed input lock and matching Git tag exist, open **Actions** →
**Publish Bootstrap Assets** → **Run workflow**. Enter the tag, leave
**publish** unchecked, and run it. This builds and signs a temporary validation
package but makes no GitHub Release changes. Only after that run succeeds should
you repeat it with **publish** checked.
