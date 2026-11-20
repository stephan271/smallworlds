#!/usr/bin/env python3
"""Tests for generate_member_network_patches.py.

Run: python3 admin-tools/test-member-network-patches.py

Writes overlay kustomizations into a temporary directory, runs the generator
on them as prepare-community-repo.sh does, and checks the files. Where
`kubectl` is installed it also renders the result, with the Component URLs
pointed at this checkout, so a block that kustomize would reject fails here.
"""
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

try:  # as in test-object-storage.py: CI's Python has no PyYAML
    import yaml
except ImportError:
    yaml = None

HERE = Path(__file__).resolve().parent
REPO = HERE.parent
GENERATOR = HERE / "generate_member_network_patches.py"
STORAGE = HERE / "generate_storage_patches.py"
BASE_URL = "https://github.com/stephan271/smallworlds.git/infrastructure/kubernetes"

passed = failed = 0


def check(name, condition, detail=""):
    global passed, failed
    if condition:
        passed += 1
        print(f"PASS {name}")
    else:
        failed += 1
        print(f"FAIL {name}" + (f"\n  {detail}" if detail else ""))


def run(*args):
    return subprocess.run([sys.executable, str(GENERATOR), *args], capture_output=True, text=True)


def overlay(tmp, app, tenant=None, extra=""):
    path = Path(tmp) / app / "kustomization.yaml"
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(
        "resources:\n"
        f"  - {BASE_URL}/tenants/{tenant or app}?ref=v2.1.0\n\n"
        f"# Add your patches for {app} here\n{extra}", encoding="utf-8")
    return path


def generate(path, app, mode="on", apps="nextcloud immich forgejo stalwart bulwark",
             backend="gallery", domain="example.org", ext=".dev"):
    return run("--app", app, "--mode", mode, "--apps", apps, "--photo-backend", backend,
               "--domain", domain, "--ext", ext, "--version", "v2.1.0",
               "--kustomization-file", str(path))


def render(path):
    """kubectl kustomize with every base URL replaced by this checkout."""
    if not shutil.which("kubectl") or yaml is None:
        return None
    local = path.parent / "local"
    local.mkdir(exist_ok=True)
    # The overlay sits at <tmp>/<app>/local/; reach the repo from there.
    text = path.read_text(encoding="utf-8").replace(
        BASE_URL, str(Path("..", "..", "..") / REPO.name / "infrastructure" / "kubernetes"))
    text = text.replace("?ref=v2.1.0", "")
    (local / "kustomization.yaml").write_text(text, encoding="utf-8")
    link = local.parent.parent.parent / REPO.name
    if not link.exists():
        link.symlink_to(REPO)
    result = subprocess.run(["kubectl", "kustomize", "--load-restrictor", "LoadRestrictionsNone",
                             str(local)], capture_output=True, text=True)
    if result.returncode != 0:
        return result.stderr
    return list(yaml.safe_load_all(result.stdout))


with tempfile.TemporaryDirectory() as outer:
    tmp = Path(outer) / "overlay"
    tmp.mkdir()

    # --- on: a members-only application -----------------------------------
    path = overlay(tmp, "forgejo")
    before = path.read_text()
    result = generate(path, "forgejo")
    text = path.read_text()
    check("forgejo: the generator succeeds", result.returncode == 0, result.stderr)
    check("forgejo: the Component block comes first",
          text.startswith("# >>> smallworlds:member-network"), text[:120])
    check("forgejo: it names members-only at the overlay's version",
          f"{BASE_URL}/components/member-network/members-only?ref=v2.1.0" in text)
    check("forgejo: no patches are needed", "member-network-patches" not in text)
    again = generate(path, "forgejo")
    check("a second run changes nothing", path.read_text() == text and again.stdout == "",
          again.stdout)
    generate(path, "forgejo", mode="off")
    check("off removes it again, byte for byte", path.read_text() == before, path.read_text())

    # --- on: Nextcloud, with its guest Ingress host ------------------------
    path = overlay(tmp, "nextcloud")
    generate(path, "nextcloud")
    text = path.read_text()
    check("nextcloud: its own Component",
          "/components/member-network/nextcloud?ref=v2.1.0" in text)
    check("nextcloud: nextcloud-guest gets the community's host",
          "name: nextcloud-guest" in text and "value: files.dev.example.org" in text, text)

    # --- Gallery and Immich ------------------------------------------------
    path = overlay(tmp, "immich", tenant="gallery")
    generate(path, "immich")
    check("gallery: the gallery Component", "/components/member-network/gallery?ref=" in path.read_text())
    result = run("--check", "--app", "root", "--mode", "on", "--apps", "nextcloud immich",
                 "--photo-backend", "immich")
    check("plain Immich is refused with the Member Network on",
          result.returncode == 2 and "Gallery" in result.stderr, result.stderr)
    result = run("--check", "--app", "root", "--mode", "off", "--apps", "immich",
                 "--photo-backend", "immich")
    check("plain Immich is fine with it off", result.returncode == 0, result.stderr)
    result = run("--check", "--app", "root", "--mode", "on", "--apps", "nextcloud",
                 "--photo-backend", "immich")
    check("the photo answer does not matter without Photos", result.returncode == 0, result.stderr)

    # --- applications that stay public -------------------------------------
    path = overlay(tmp, "join")
    generate(path, "join")
    text = path.read_text()
    check("join: stays public, but its done page learns the member Headscale",
          "components:" not in text and "path: /data/MEMBER_NETWORK_URL" in text
          and "value: https://network.dev.example.org" in text, text)

    for app in ("jitsi", "keycloak", "euro-office"):
        path = overlay(tmp, app)
        before = path.read_text()
        generate(path, app)
        check(f"{app}: untouched", path.read_text() == before, path.read_text())

    # --- the member Headscale ----------------------------------------------
    path = overlay(tmp, "member-network")
    generate(path, "member-network", apps="nextcloud immich stalwart excalidraw jitsi")
    text = path.read_text()
    block = text.split("name: MEMBER_HOSTS", 1)[1].split("containers:", 1)[0]
    hosts = " ".join(line.strip() for line in block.splitlines()[2:] if line.strip())
    check("member-network: the hosts of the selected member apps and the dashboard, in order",
          hosts.split() == ["files.dev.example.org", "photos.dev.example.org",
                            "whiteboard.dev.example.org", "dashboard.dev.example.org",
                            "mail.dev.example.org"], hosts)
    check("member-network: Jitsi stays public, so no meet record", "meet." not in hosts)
    check("member-network: server URL, issuer and callback follow the domain",
          "https://network.dev.example.org" in text
          and "https://identity.dev.example.org/realms/smallworlds" in text
          and "network.dev.example.org/oidc/callback" in text, text)

    # --- an operator's own components: list --------------------------------
    path = overlay(tmp, "bulwark", extra="components:\n  - ../my-own\n")
    before = path.read_text()
    result = generate(path, "bulwark")
    check("an operator's own components: list stops it, untouched",
          result.returncode == 2 and path.read_text() == before, result.stderr)

    # --- beside the object-storage block -----------------------------------
    path = overlay(tmp, "stalwart")
    storage = [sys.executable, str(STORAGE), "--app", "stalwart", "--mode", "external",
               "--endpoint", "https://s3.example.com", "--region", "eu", "--bucket-prefix",
               "sw-example", "--kustomization-file", str(path)]
    subprocess.run(storage, check=True, capture_output=True)
    generate(path, "stalwart")
    subprocess.run(storage, check=True, capture_output=True)   # a re-run moves its block last
    generate(path, "stalwart")
    if yaml is None:
        print("SKIP stalwart: valid beside the object-storage block (no PyYAML)")
    else:
        docs = yaml.safe_load(path.read_text())
        check("stalwart: valid beside the object-storage block, both present",
              docs.get("components") and len(docs.get("patches", [])) >= 1
              and "object-storage" in path.read_text(), path.read_text())

    # --- the root ----------------------------------------------------------
    root = Path(tmp) / "kustomization.yaml"
    root.write_text(f"""resources:
  - {BASE_URL}?ref=v2.1.0
  # Include the ArgoCD Application manifest for member-network
  - https://raw.githubusercontent.com/stephan271/smallworlds/v2.1.0/infrastructure/kubernetes/apps/member-network.yaml
  - https://raw.githubusercontent.com/stephan271/smallworlds/v2.1.0/infrastructure/kubernetes/apps/forgejo.yaml

patches:
  - target:
      group: argoproj.io
      kind: Application
      name: member-network
    patch: |-
      - op: replace
        path: /spec/source/path
        value: member-network
  - target:
      group: argoproj.io
      kind: Application
      name: forgejo
    patch: |-
      - op: replace
        path: /spec/source/path
        value: forgejo
""", encoding="utf-8")
    generate(root, "root", mode="off")
    text = root.read_text()
    check("root, off: the member-network Application and its routing are gone",
          "member-network" not in text and "apps/forgejo.yaml" in text and "value: forgejo" in text,
          text)

    # --- rendered, where kubectl is available -------------------------------
    for app, tenant in (("member-network", None), ("stalwart", None), ("bulwark", None),
                        ("join", None)):
        path = overlay(tmp, f"render-{app}", tenant=tenant or app)
        generate(path, app, apps="nextcloud stalwart bulwark")
        out = render(path)
        if out is None:
            print(f"SKIP {app}: renders (no kubectl or PyYAML)")
            continue
        ok = isinstance(out, list)
        check(f"{app}: renders with kustomize", ok, out if not ok else "")
        if not ok:
            continue
        kinds = {(d["kind"], d["metadata"]["name"]): d for d in out if d}
        if app == "member-network":
            dep = kinds[("Deployment", "headscale")]
            env = dep["spec"]["template"]["spec"]["initContainers"][0]["env"]
            check("member-network: the rendered MEMBER_HOSTS",
                  any(e["name"] == "MEMBER_HOSTS" and "webmail.dev.example.org" in e["value"]
                      for e in env), env)
        if app == "stalwart":
            svc = kinds[("Service", "stalwart-mail")]
            check("stalwart: rendered, only 25 and 8080 published",
                  sorted(p["port"] for p in svc["spec"]["ports"]) == [25, 8080])
        if app == "join":
            cm = kinds[next(k for k in kinds if k[0] == "ConfigMap" and k[1].startswith("join-config"))]
            check("join: rendered with the member Headscale's address",
                  cm["data"].get("MEMBER_NETWORK_URL") == "https://network.dev.example.org", cm["data"])
        if app == "bulwark":
            ing = kinds[("Ingress", "bulwark-ingress")]
            check("bulwark: rendered on `members`",
                  ing["metadata"]["annotations"].get(
                      "traefik.ingress.kubernetes.io/router.entrypoints") == "members")

print(f"{passed} passed, {failed} failed")
sys.exit(1 if failed else 0)
