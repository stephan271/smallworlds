#!/usr/bin/env python3
"""A stand-in for kubectl in the Update Desk's tests: a small Ring 1 cluster.

State lives in the JSON file $FAKE_KUBE_STATE. Applications are listed from it;
`patch` moves the root pin, after which the next two reads show the cluster
rolling and the third shows every Application Synced and Healthy at the commits
the overlay names (read from the overlay repository in $FAKE_KUBE_OVERLAY).
Every call is appended to $FAKE_KUBE_STATE.log.
"""
import json
import os
import re
import subprocess
import sys

STATE = os.environ["FAKE_KUBE_STATE"]
OVERLAY = os.environ.get("FAKE_KUBE_OVERLAY", "")
BASE = "https://github.com/stephan271/smallworlds.git"


def load():
    with open(STATE) as handle:
        return json.load(handle)


def save(state):
    with open(STATE, "w") as handle:
        json.dump(state, handle, indent=1)


def base_ref_at(overlay_sha):
    text = subprocess.run(["git", "-C", OVERLAY, "show", f"{overlay_sha}:kustomization.yaml"],
                          capture_output=True, text=True).stdout
    m = re.search(r"\?ref=(\S+)", text)
    return m.group(1) if m else None


def application(app, state):
    return {"metadata": {"name": app["name"]},
            "spec": {"source": {"repoURL": app["repoURL"], "targetRevision": app["targetRevision"]},
                     "destination": {"namespace": app.get("namespace", app["name"])}},
            "status": {"sync": {"status": app["sync"], "revision": app["revision"]},
                       "health": {"status": app["health"]},
                       "operationState": {"phase": "Succeeded"}}}


def main(argv):
    while argv and argv[0] == "--kubeconfig":
        argv = argv[2:]
    if argv and argv[0] == "kustomize":  # rendering is real
        os.execv(os.environ["REAL_KUBECTL"], ["kubectl", *argv])
    with open(STATE + ".log", "a") as log:
        log.write(" ".join(argv) + "\n")
    state = load()
    if state.get("unreachable"):
        print("The connection to the server was refused", file=sys.stderr)
        return 1
    words = [w for w in argv if not w.startswith("-")]
    if "get" in argv and "applications.argoproj.io" in argv:
        if state.get("rolling") is not None:
            state["rolling"] -= 1
            if state["rolling"] <= 0:
                root = next(a for a in state["applications"] if a["name"] == "smallworlds-root")
                base = base_ref_at(root["targetRevision"])
                for app in state["applications"]:
                    app["sync"], app["health"] = "Synced", "Healthy"
                    if app["repoURL"] == BASE:
                        app["revision"] = state["tags"].get(base, base)
                    elif app["name"] == "smallworlds-root":
                        app["revision"] = app["targetRevision"]
                    else:
                        app["revision"] = subprocess.run(
                            ["git", "-C", OVERLAY, "rev-parse", "origin/main"],
                            capture_output=True, text=True).stdout.strip()
                state["rolling"] = None
            else:
                for app in state["applications"]:
                    if app["name"] != "smallworlds-root":
                        app["sync"] = "OutOfSync"
            save(state)
        print(json.dumps({"items": [application(a, state) for a in state["applications"]]}))
        return 0
    if "get" in argv and "clusters.postgresql.cnpg.io" in argv:
        print(json.dumps({"items": [{"metadata": {"name": d["name"], "namespace": d["namespace"]},
                                     "spec": {"plugins": [{"name": "barman-cloud.cloudnative-pg.io", "isWALArchiver": True}]},
                                     "status": {"phase": "Cluster in healthy state"}} for d in state["databases"]]}))
        return 0
    if "get" in argv and "backups.postgresql.cnpg.io" in argv:
        selector = argv[argv.index("-l") + 1]
        items = [b for b in state.get("backups", []) if selector in b["metadata"].get("labels", {})]
        for b in items:
            b.setdefault("status", {"phase": "completed"})
        print(json.dumps({"items": items}))
        return 0
    if "patch" in argv:
        patch = json.loads(argv[argv.index("-p") + 1])
        root = next(a for a in state["applications"] if a["name"] == "smallworlds-root")
        root["targetRevision"] = patch["spec"]["source"]["targetRevision"]
        state["rolling"] = 2
        save(state)
        print("application.argoproj.io/smallworlds-root patched")
        return 0
    if "annotate" in argv:
        print(f"application.argoproj.io/{words[-2]} annotated")
        return 0
    if "create" in argv:
        manifest = json.loads(sys.stdin.read())
        state.setdefault("backups", []).append(manifest)
        save(state)
        print(f"backup.postgresql.cnpg.io/{manifest['metadata']['name']} created")
        return 0
    print("fake kubectl: unknown call " + " ".join(argv), file=sys.stderr)
    return 1


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
