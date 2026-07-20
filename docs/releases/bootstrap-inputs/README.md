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
