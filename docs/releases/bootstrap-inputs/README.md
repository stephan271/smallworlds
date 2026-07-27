# Bootstrap release input locks

Each published SmallWorlds bootstrap release has one reviewed JSON lock file in
this directory, named after its release tag—for example `v1.2.25.json`.

The file records the exact K3s installer and Argo CD manifest version, URL, and
SHA-256 digest that the release packager is allowed to download. It contains no
credentials and is reviewed like source code. It is a release-maintainer input,
not an Operator setup form.

Create a new lock by copying the example and replacing every placeholder only
after independently reviewing the official upstream release material. The
GitHub Action validates and consumes this committed lock; it never accepts
arbitrary values from an Operator's browser.

`v1.2.25.json` is the first release candidate. `v1.2.26.json` adds the hardened,
resumable local-node bootstrap contract. `v1.2.27.json` retains the same
reviewed upstream versions while incorporating fixes from the first destructive
browser qualification. `v1.2.28.json` is the first release to also carry the
cross-platform Bootstrap Launcher artifacts, whose packaging script did not yet
exist at `v1.2.27`; it retains the same reviewed upstream versions. They pin K3s
`v1.36.2+k3s1` and Argo CD `v3.4.5`, with digests independently retrieved from
their official release locations on 2026-07-20 and re-verified against those
same locations on 2026-07-27. A lock is not published until a maintainer creates
the matching SmallWorlds tag and explicitly runs the release workflow.

A published release is complete only when **both** release workflows have run
against its tag: **Publish Bootstrap Assets** attaches the server's installation
payload, and **Publish Bootstrap Launcher** attaches the five native launcher
archives with their signed `SHA256SUMS`. A tag carrying only the first is
installable by an existing launcher but offers no way to obtain one.
