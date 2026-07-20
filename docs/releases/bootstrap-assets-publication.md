# Bootstrap asset publication contract

The Bootstrap Launcher accepts only release-specific assets attached to the
matching official `stephan271/smallworlds` GitHub Release and signed by the
trusted SmallWorlds release key. This document describes the release-engineering
material required by that source; it does not make Offline Bundle bootstrap
available.

1. Build one immutable archive per supported platform with
   `admin-tools/build-bootstrap-assets.sh`. It requires exact K3s and Argo CD
   input versions, direct HTTPS URLs, and their verified SHA-256 digests; it
   refuses `latest`, credential-bearing, query-string, and fragment URLs. The
   resulting archive contains the reviewed local bootstrap script, the fetched
   inputs, and a machine-readable input manifest. It is a release payload, not
   an Offline Bundle: its contents do not make its downstream installation
   dependencies air-gapped.
2. Calculate the archive SHA-256 in lowercase hexadecimal.
3. Sign the ASCII SHA-256 text with the release Ed25519 private key.
4. Create or update the matching GitHub Release and attach the archive, its
   checksum, and its signature. The Launcher source URL has the fixed shape
   `https://github.com/stephan271/smallworlds/releases/download/<tag>/<asset>`.
5. Add that fixed GitHub Release attachment URL, digest, base64 signature, and
   base64 public key to the versioned manifest shape shown in
   [`bootstrap-assets.manifest.example.json`](bootstrap-assets.manifest.example.json),
   then add the descriptor and trusted public key to the launcher’s compiled
   release catalog only after independent verification of the uploaded bytes.

The manifest may not contain credentials, query-string tokens, mutable URLs,
ambient paths, or browser-supplied executable locations. GitHub's controlled
release-asset redirect is an implementation detail of the trusted URL, not an
Operator-selected destination. A failed or unavailable release must leave the
Launcher in its current explicit “artifact not published” state.
