#!/usr/bin/env python3
"""Tests for the Update Desk (admin-tools/update-desk/). No network, no cluster:
the base repository is built in a temporary directory, verdicts are given, and
kubectl is tests/fake_kubectl.py.

    python3 admin-tools/update-desk/test_desk.py
"""
from __future__ import annotations

import json
import os
import subprocess
import sys
import tempfile
import textwrap
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.abspath(os.path.join(HERE, "..", ".."))
sys.path.insert(0, HERE)

import basegit  # noqa: E402
import community  # noqa: E402
import engine  # noqa: E402
import miniyaml  # noqa: E402
import path as path_module  # noqa: E402
import pins  # noqa: E402
import units  # noqa: E402
import versions  # noqa: E402

K8S = "infrastructure/kubernetes/"
B = "https://github.com/stephan271/smallworlds.git"


def git(cwd, *args, env=None):
    return subprocess.run(["git", "-C", cwd, *args], check=True, capture_output=True, text=True,
                          env={**os.environ, "GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@invalid",
                               "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@invalid",
                               **(env or {})}).stdout.strip()


def write(root, files: dict[str, str | None]):
    for name, text in files.items():
        full = os.path.join(root, name)
        if text is None:
            os.remove(full)
            continue
        os.makedirs(os.path.dirname(full), exist_ok=True)
        with open(full, "w") as handle:
            handle.write(textwrap.dedent(text))


def application(name, tenant):
    return f"""\
    apiVersion: argoproj.io/v1alpha1
    kind: Application
    metadata:
      name: {name}
      namespace: argocd
    spec:
      source:
        repoURL: '{B}'
        targetRevision: HEAD
        path: infrastructure/kubernetes/tenants/{tenant}
      destination:
        namespace: {name}
    """


def _read(path):
    with open(path) as handle:
        return handle.read()


class FakeVerdicts:
    def __init__(self, states):
        self.states = states
        self.lookup_error = None

    def lookup(self, shas, since):
        pass

    def of(self, sha):
        return {"state": self.states.get(sha), "at": "2026-11-20T10:00:00Z"} if sha in self.states else {"state": None}

    def load_ring0(self):
        pass

    def now(self):
        return {"now": {"state": "up-to-date"}, "updated": None, "error": None}


class Fixture:
    """A small base repository with a few commits, and its verdicts."""

    def __init__(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.origin = os.path.join(self.tmp.name, "origin")
        self.base = os.path.join(self.tmp.name, "base")
        os.makedirs(self.origin)
        git(self.origin, "init", "-q", "-b", "main")
        write(self.origin, {
            K8S + "kustomization.yaml": """\
                resources:
                  - apps/keycloak.yaml
                """,
            K8S + "apps/keycloak.yaml": application("keycloak", "keycloak"),
            K8S + "apps/jitsi.yaml": application("jitsi", "jitsi"),
            K8S + "apps/stalwart.yaml": application("stalwart", "stalwart"),
            K8S + "tenants/keycloak/kustomization.yaml": "resources: [db.yaml]\n",
            K8S + "tenants/keycloak/db.yaml": "image: quay.io/keycloak/keycloak:26.6.4\n",
            K8S + "tenants/jitsi/kustomization.yaml": """\
                helmCharts:
                  - name: jitsi-meet
                    repo: https://example
                    version: 2.20.0
                """,
            K8S + "tenants/stalwart/kustomization.yaml": "resources: []\n",
            K8S + "update-units.yaml": _read(os.path.join(REPO, K8S, "update-units.yaml")),
            K8S + "operator-steps.yaml": "steps: []\n",
            "README.md": "base\n",
        })
        self.commit("start")
        self.start = self.head()
        self.states = {}
        # 1: Jitsi's chart, verified.
        write(self.origin, {K8S + "tenants/jitsi/kustomization.yaml": """\
            helmCharts:
              - name: jitsi-meet
                repo: https://example
                version: 3.12.0
            """})
        self.jitsi = self.commit("Update jitsi (#1)", "success")
        # 2: Stalwart only: no step for a community without mail.
        write(self.origin, {K8S + "tenants/stalwart/kustomization.yaml": "resources: [] # changed\n"})
        self.stalwart = self.commit("Update stalwart (#2)", "success")
        # 3 and 4: a rejected Keycloak update and its revert cancel out.
        write(self.origin, {K8S + "tenants/keycloak/db.yaml": "image: quay.io/keycloak/keycloak:26.7.0\n"})
        self.rejected = self.commit("Update keycloak (#3)", "failure")
        write(self.origin, {K8S + "tenants/keycloak/db.yaml": "image: quay.io/keycloak/keycloak:26.6.4\n"})
        self.revert = self.commit("Revert keycloak (#4)", "success")
        # 5: Keycloak migrates, without a pre-update backup hook.
        write(self.origin, {K8S + "tenants/keycloak/db.yaml": "image: quay.io/keycloak/keycloak:26.8.2\n"})
        self.keycloak = self.commit("Update keycloak to 26.8.2 (#5)", "success")
        # 6: docs only, not yet verified.
        write(self.origin, {"README.md": "base, later\n"})
        self.docs = self.commit("Docs", "pending")
        git(self.tmp.name, "clone", "-q", self.origin, self.base)

    def commit(self, message, state=None):
        git(self.origin, "add", "-A")
        git(self.origin, "commit", "-q", "-m", message)
        sha = self.head()
        if state:
            self.states[sha] = state
        return sha

    def head(self):
        return git(self.origin, "rev-parse", "HEAD")

    def overlay(self, ref, apps=("jitsi",)):
        """An overlay in the shape prepare-community-repo.sh writes, with a
        bare remote of its own."""
        root = os.path.join(self.tmp.name, "overlay-src")
        files = {"kustomization.yaml": "resources:\n"
                 f"  - https://github.com/stephan271/smallworlds.git/infrastructure/kubernetes?ref={ref}\n"
                 + "".join(f"  - https://raw.githubusercontent.com/stephan271/smallworlds/{ref}/infrastructure/kubernetes/apps/{a}.yaml\n" for a in apps)
                 + "\npatches:\n"
                 "  - target:\n      group: argoproj.io\n      kind: Application\n      name: keycloak\n"
                 "    patch: |-\n      - op: replace\n        path: /spec/source/targetRevision\n        value: HEAD\n"
                 "      - op: replace\n        path: /spec/source/repoURL\n        value: https://github.com/someone/my-overlay.git\n"
                 "      - op: replace\n        path: /spec/source/path\n        value: keycloak\n",
                 "keycloak/kustomization.yaml": "resources:\n"
                 f"  - https://github.com/stephan271/smallworlds.git/infrastructure/kubernetes/tenants/keycloak?ref={ref}\n"}
        os.makedirs(root)
        write(root, files)
        git(root, "init", "-q", "-b", "main")
        git(root, "add", "-A")
        git(root, "commit", "-q", "-m", "overlay")
        bare = os.path.join(self.tmp.name, "overlay.git")
        git(self.tmp.name, "clone", "-q", "--bare", root, bare)
        checkout = os.path.join(self.tmp.name, "overlay")
        git(self.tmp.name, "clone", "-q", bare, checkout)
        return checkout


# --------------------------------------------------------------------------------

class MiniYaml(unittest.TestCase):
    def test_subset(self):
        data = miniyaml.loads(textwrap.dedent("""\
            # comment
            name: a  # trailing
            list: [x, "y", 3]
            nested:
              flag: true
              text: |
                one
                two
              folded: >-
                a
                b
            items:
              - name: one
                value: 'q # not a comment'
              - plain
            """))
        self.assertEqual(data["name"], "a")
        self.assertEqual(data["list"], ["x", "y", 3])
        self.assertEqual(data["nested"], {"flag": True, "text": "one\ntwo\n", "folded": "a b"})
        self.assertEqual(data["items"], [{"name": "one", "value": "q # not a comment"}, "plain"])

    def test_rejects_duplicates(self):
        with self.assertRaises(ValueError):
            miniyaml.loads("a: 1\na: 2\n")


class DataFiles(unittest.TestCase):
    """The three files in infrastructure/kubernetes/ the Desk reads."""

    def setUp(self):
        with open(os.path.join(REPO, units.UNITS_FILE)) as handle:
            self.catalog = units.Catalog.parse(handle.read())

    def test_units_name_what_exists_and_claim_it_once(self):
        tenants = set(os.listdir(os.path.join(REPO, K8S, "tenants")))
        applications = {n[:-5] for n in os.listdir(os.path.join(REPO, K8S, "apps")) if n.endswith(".yaml")}
        claimed_tenants, claimed_apps = [], []
        for unit in self.catalog.units.values():
            claimed_tenants += unit.tenants
            claimed_apps += unit.applications
            for tenant in unit.migrates:
                self.assertIn(tenant, unit.tenants + (["keycloak"] if unit.name == units.PLATFORM else []))
        claimed_tenants += sorted(self.catalog.ring0_tenants)
        claimed_apps += sorted(self.catalog.ring0_applications)
        self.assertEqual(len(claimed_tenants), len(set(claimed_tenants)), "a tenant in two units")
        self.assertEqual(len(claimed_apps), len(set(claimed_apps)), "an Application in two units")
        self.assertLessEqual(set(claimed_tenants), tenants, "update-units.yaml names a tenant that does not exist")
        self.assertLessEqual(set(claimed_apps), applications, "update-units.yaml names an Application that does not exist")
        for path in self.catalog.not_applied:
            self.assertTrue(os.path.exists(os.path.join(REPO, path)), path)

    def test_paths_map_to_units(self):
        c = self.catalog
        self.assertEqual(c.unit_of_path(K8S + "tenants/gallery/kustomization.yaml"), "photos")
        self.assertEqual(c.unit_of_path(K8S + "apps/euro-office.yaml"), "nextcloud")
        self.assertEqual(c.unit_of_path(K8S + "tenants/keycloak/values.yaml"), units.PLATFORM)
        self.assertEqual(c.unit_of_path(K8S + "bases/pre-update-backup/x.yaml"), units.PLATFORM)
        self.assertEqual(c.unit_of_path(K8S + "components/member-network/a.yaml"), "member-network")
        self.assertIsNone(c.unit_of_path(K8S + "tenants/canary-follower/src/main.py"))
        self.assertIsNone(c.unit_of_path(K8S + "tenants/join/tests/test_desk.py"))
        self.assertIsNone(c.unit_of_path(K8S + "upgrade-stops.yaml"))
        self.assertIsNone(c.unit_of_path("doc/auto-updates.md"))

    def test_operator_steps_name_first_parent_commits(self):
        with open(os.path.join(REPO, K8S, "operator-steps.yaml")) as handle:
            entries = miniyaml.loads(handle.read())["steps"]
        line = set(subprocess.run(["git", "-C", REPO, "rev-list", "--first-parent", "HEAD"],
                                  capture_output=True, text=True).stdout.split())
        shallow = subprocess.run(["git", "-C", REPO, "rev-parse", "--is-shallow-repository"],
                                 capture_output=True, text=True).stdout.strip() == "true"
        for entry in entries:
            self.assertRegex(entry["commit"], r"^[0-9a-f]{40}$")
            self.assertIn(entry["when"], ("before", "after", "info"))
            self.assertTrue(entry["title"] and entry["detail"])
            self.assertLessEqual(set(entry.get("applies") or {}), {"domain_not", "runs", "overlay_patches"})
            if not shallow:
                self.assertIn(entry["commit"], line, f"{entry['title']}: not on main's first-parent line")

    def test_upgrade_stops_parse(self):
        with open(os.path.join(REPO, K8S, "upgrade-stops.yaml")) as handle:
            for stop in miniyaml.loads(handle.read())["stops"]:
                self.assertTrue({"unit", "image", "version", "before"} <= set(stop))


class Versions(unittest.TestCase):
    def test_reads_every_form(self):
        found = versions.versions_in("x/kustomization.yaml", textwrap.dedent("""\
            helmCharts:
              - name: nextcloud
                repo: https://x
                version: 9.4.5
            images:
              - name: ghcr.io/immich-app/immich-server
                newName: ghcr.io/open-noodle/gallery-server
                newTag: v5.11.1
            """))
        found.update(versions.versions_in("x/values.yaml", textwrap.dedent("""\
            image:
              repository: docker.io/library/redis
              tag: 8.10-alpine
            other:
              image: quay.io/keycloak/keycloak:26.8.2@sha256:abc
            """)))
        found = versions.settle(found)
        self.assertEqual(found["chart:nextcloud"], {"9.4.5"})
        self.assertEqual(found["image:ghcr.io/open-noodle/gallery-server"], {"v5.11.1"})
        self.assertNotIn("image:ghcr.io/immich-app/immich-server", found)
        self.assertEqual(found["image:redis"], {"8.10-alpine"})
        self.assertEqual(found["image:quay.io/keycloak/keycloak"], {"26.8.2"})

    def test_postgres_minor_is_no_migration(self):
        self.assertTrue(path_module._same_postgres_major(
            {"kind": "image", "name": "ghcr.io/tensorchord/cloudnative-vectorchord", "from": "16.13-1.1.1", "to": "16.15-1.1.1"}))
        self.assertFalse(path_module._same_postgres_major(
            {"kind": "image", "name": "ghcr.io/tensorchord/cloudnative-vectorchord", "from": "16.13-1.1.1", "to": "16.15-1.1.1, 18.6-1.1.1"}))


class Pins(unittest.TestCase):
    ROOT = textwrap.dedent("""\
        resources:
          - https://github.com/stephan271/smallworlds.git/infrastructure/kubernetes?ref=v1.3.0
          - https://raw.githubusercontent.com/stephan271/smallworlds/v1.3.0/infrastructure/kubernetes/apps/jitsi.yaml

        patches:
          - target:
              group: argoproj.io
              kind: Application
              name: jitsi
            patch: |-
              - op: replace
                path: /spec/source/targetRevision
                value: HEAD
              - op: replace
                path: /spec/source/repoURL
                value: https://github.com/someone/overlay.git
              - op: replace
                path: /spec/source/path
                value: jitsi
          - target:
              group: argoproj.io
              kind: Application
              name: garage
            patch: |-
              $patch: delete
              apiVersion: argoproj.io/v1alpha1
              kind: Application
              metadata:
                name: garage
        """)
    SHA = "a" * 40

    def texts(self):
        return {"kustomization.yaml": self.ROOT,
                "jitsi/kustomization.yaml": "resources:\n  - https://github.com/stephan271/smallworlds.git/infrastructure/kubernetes/tenants/jitsi?ref=v1.3.0\n"}

    def test_read(self):
        overlay = pins.read("/x", texts=self.texts())
        self.assertEqual(overlay.ref, "v1.3.0")
        self.assertEqual(overlay.routed, {"jitsi": "jitsi"})
        self.assertEqual(overlay.deleted, {"garage"})
        self.assertEqual(overlay.raw_apps, ["jitsi"])
        self.assertEqual(overlay.folder_tenants["jitsi"], ["jitsi"])

    def test_commit_pin_writes_every_ref_and_the_block(self):
        overlay = pins.read("/x", texts=self.texts())
        changed = pins.rewrite(self.texts(), self.SHA, ["jitsi", "join", "garage"], overlay)
        root = changed["kustomization.yaml"]
        self.assertNotIn("v1.3.0", root + changed["jitsi/kustomization.yaml"])
        self.assertIn(f"?ref={self.SHA}", changed["jitsi/kustomization.yaml"])
        block = root[root.index(pins.BLOCK_START):]
        self.assertIn("name: join", block)
        self.assertNotIn("name: jitsi", block, "a routed Application follows the overlay")
        self.assertNotIn("name: garage", block, "a deleted Application is not patched")
        self.assertEqual(miniyaml.loads(root)["patches"][-1]["target"]["name"], "join")
        # Reading it back sees the same overlay, and writing again changes nothing.
        again = dict(self.texts(), **changed)
        reread = pins.read("/x", texts=again)
        self.assertEqual((reread.ref, reread.routed, reread.has_block), (self.SHA, {"jitsi": "jitsi"}, True))
        self.assertEqual(pins.rewrite(again, self.SHA, ["jitsi", "join", "garage"], reread), {})

    def test_moving_to_a_tag_drops_the_block(self):
        overlay = pins.read("/x", texts=self.texts())
        pinned = dict(self.texts(), **pins.rewrite(self.texts(), self.SHA, ["join"], overlay))
        back = pins.rewrite(pinned, "v1.4.0", ["join"], pins.read("/x", texts=pinned))
        self.assertNotIn(pins.BLOCK_START, back["kustomization.yaml"])
        self.assertEqual(back["kustomization.yaml"], self.ROOT.replace("v1.3.0", "v1.4.0"))

    def test_refuses_a_moving_ref(self):
        with self.assertRaises(pins.PinError):
            pins.rewrite(self.texts(), "main", [], pins.read("/x", texts=self.texts()))

    def test_never_head(self):
        rendered = textwrap.dedent(f"""\
            apiVersion: argoproj.io/v1alpha1
            kind: Application
            metadata:
              name: join
            spec:
              source:
                repoURL: {B}
                targetRevision: HEAD
            ---
            apiVersion: argoproj.io/v1alpha1
            kind: Application
            metadata:
              name: traefik
            spec:
              source:
                repoURL: https://helm.traefik.io/traefik
                targetRevision: 42.4.1
            """)
        self.assertEqual(pins.never_head(rendered, self.SHA), ["join reads HEAD"])
        self.assertEqual(pins.never_head(rendered.replace("HEAD", self.SHA), self.SHA), [])


class Path(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.fx = Fixture()

    @classmethod
    def tearDownClass(cls):
        cls.fx.tmp.cleanup()

    def context(self, overlay_path, operator_steps=()):
        base = basegit.BaseRepo(self.fx.base)
        base.fetch = lambda: None
        catalog = units.Catalog.parse(base.show(base.head(), units.UNITS_FILE))
        overlay = pins.read(overlay_path)
        com = community.at(base, [self.fx.start, base.head()], overlay, catalog)
        return base, path_module.Context(base=base, catalog=catalog, community=com,
                                         overlay_patched=overlay.patched,
                                         operator_steps=list(operator_steps), releases={})

    def test_steps_follow_verified_commits_that_reach_the_community(self):
        overlay = self.fx.overlay(self.fx.start)
        rule = {"commit": self.fx.keycloak, "when": "after", "title": "do it", "detail": "x",
                "applies": {"domain_not": "smallworlds.network"}}
        base, context = self.context(overlay, [rule, dict(rule, title="always", applies={})])
        self.assertEqual(context.community.units, {"jitsi", units.PLATFORM})
        result = path_module.compute(context, self.fx.start, FakeVerdicts(self.fx.states))
        steps = result["steps"]
        self.assertEqual([s["sha"] for s in steps], [self.fx.jitsi, self.fx.keycloak])
        jitsi, keycloak = steps
        self.assertEqual(list(jitsi["units"]), ["jitsi"])
        self.assertEqual(jitsi["units"]["jitsi"]["changes"][0],
                         {"kind": "chart", "name": "jitsi-meet", "from": "2.20.0", "to": "3.12.0", "helper": False})
        self.assertTrue(jitsi["simple"])
        self.assertEqual(jitsi["undo"], "revert")
        # The Stalwart commit and the rejected update with its revert ride along.
        self.assertEqual([c["sha"] for c in keycloak["commits"]],
                         [self.fx.stalwart, self.fx.rejected, self.fx.revert, self.fx.keycloak])
        self.assertEqual([(m["tenant"], m["protected"]) for m in keycloak["migrations"]], [("keycloak", False)])
        self.assertEqual(keycloak["undo"], "restore")
        self.assertEqual([m["title"] for m in keycloak["manual"]], ["always"], "domain_not keeps the other one away")
        self.assertEqual([c["sha"] for c in result["beyond"]], [self.fx.docs])
        self.assertEqual(result["batches"]["careful"],
                         [{"first": 1, "last": 1, "kind": "simple"}, {"first": 2, "last": 2, "kind": "careful"}])
        self.assertEqual(result["batches"]["fewer"],
                         [{"first": 1, "last": 1, "kind": "simple"}, {"first": 2, "last": 2, "kind": "careful"}])
        overview = {row["unit"]: row for row in result["overview"]}
        self.assertEqual(overview["jitsi"]["steps"], [1])
        self.assertEqual(overview[units.PLATFORM]["migrates"], [2])


class Flow(unittest.TestCase):
    """One step from plan to rolled out, against tests/fake_kubectl.py."""

    def setUp(self):
        self.fx = Fixture()
        self.overlay = self.fx.overlay(self.fx.start)
        bin_dir = os.path.join(self.fx.tmp.name, "bin")
        os.makedirs(bin_dir)
        shim = os.path.join(bin_dir, "kubectl")
        with open(shim, "w") as handle:
            handle.write(f"#!/bin/sh\nexec {sys.executable} {os.path.join(HERE, 'tests', 'fake_kubectl.py')} \"$@\"\n")
        os.chmod(shim, 0o755)
        overlay_sha = git(self.overlay, "rev-parse", "HEAD")
        apps = [{"name": "smallworlds-root", "repoURL": "https://github.com/someone/my-overlay.git",
                 "targetRevision": overlay_sha, "revision": overlay_sha, "namespace": "argocd"},
                {"name": "keycloak", "repoURL": "https://github.com/someone/my-overlay.git",
                 "targetRevision": "HEAD", "revision": overlay_sha},
                {"name": "jitsi", "repoURL": B, "targetRevision": self.fx.start, "revision": self.fx.start}]
        for app in apps:
            app.update(sync="Synced", health="Healthy")
        self.state = os.path.join(self.fx.tmp.name, "kube.json")
        with open(self.state, "w") as handle:
            json.dump({"applications": apps, "tags": {}, "databases": [{"namespace": "keycloak", "name": "keycloak-db"}]}, handle)
        git(self.overlay, "remote", "set-url", "origin", os.path.join(self.fx.tmp.name, "overlay.git"))
        self.env = {"PATH": bin_dir + os.pathsep + os.environ["PATH"], "FAKE_KUBE_STATE": self.state,
                    "FAKE_KUBE_OVERLAY": self.overlay}
        self.saved = {k: os.environ.get(k) for k in self.env}
        os.environ.update(self.env)
        self.desk = engine.Desk(self.fx.base, self.overlay, None, verdicts=FakeVerdicts(self.fx.states))
        self.desk.base.fetch = lambda: None
        # The overlay names github.com, which a test cannot fetch: rendering is
        # replaced by what Argo CD would see for the routed and pinned Applications.
        self.rendered = None

        def render(overlay_path, timeout=180):
            ref = pins.read(overlay_path).ref
            return (f"kind: Application\nmetadata:\n  name: jitsi\nspec:\n  source:\n"
                    f"    repoURL: {B}\n    targetRevision: {self.rendered or ref}\n")
        self._render = pins.render
        pins.render = render

    def tearDown(self):
        pins.render = self._render
        self.fx.tmp.cleanup()
        for key, value in self.saved.items():
            if value is None:
                os.environ.pop(key, None)
            else:
                os.environ[key] = value

    def stage(self):
        return self.desk.state()["progress"]["stage"]

    def test_one_step_from_plan_to_done_and_back(self):
        self.desk.refresh(fetch=False)
        self.assertEqual(self.stage(), "done")
        steps = self.desk.state()["path"]["steps"]
        self.assertEqual(len(steps), 2)
        plan = self.desk.plan(steps[0]["sha"])
        self.assertEqual(plan["undo"], "revert")
        self.assertIn(f"Base-Pin: {self.fx.jitsi}", plan["message"])
        result = self.desk.write(self.fx.jitsi)
        self.assertIn("kustomization.yaml", result["files"])
        self.assertEqual(self.stage(), "written")
        self.desk.commit()
        self.assertEqual(self.stage(), "committed")
        self.desk.push()
        self.assertEqual(self.stage(), "pushed")
        self.desk.move_pin()
        with open(self.state + ".log") as handle:
            self.assertIn("patch applications.argoproj.io smallworlds-root", handle.read())
        self.assertEqual(self.stage(), "rolling")
        self.assertEqual(self.stage(), "done")
        state = self.desk.state()
        self.assertEqual(len(state["path"]["steps"]), 1, "one step taken, one left")
        self.assertEqual(state["last_update"]["to"], self.fx.jitsi)
        # Undo: the step changed no database, so it is a revert.
        undo = self.desk.undo_plan()
        self.assertIsNone(undo["blocked"])
        self.desk.write(undo["to_ref"])
        self.assertEqual(pins.read(self.overlay).ref, self.fx.start)
        self.desk.discard()
        self.assertEqual(pins.read(self.overlay).ref, self.fx.jitsi)

    def test_a_migrating_step_cannot_be_undone_by_revert(self):
        self.desk.refresh(fetch=False)
        steps = self.desk.state()["path"]["steps"]
        plan = self.desk.plan(steps[1]["sha"])  # both steps: Keycloak migrates
        self.assertEqual(plan["undo"], "restore")
        self.assertEqual([m["tenant"] for m in plan["unprotected"]], ["keycloak"])
        self.assertEqual([d["name"] for d in plan["databases"]], ["keycloak-db"])
        backup = self.desk.backup("keycloak", "keycloak-db")
        self.assertTrue(backup["name"].startswith("desk-"))
        self.desk.write(steps[1]["sha"])
        self.desk.commit()
        undo = self.desk.undo_plan()
        self.assertIn("restore the pre-update backup", undo["blocked"])
        with self.assertRaises(engine.DeskError):
            self.desk.write(undo["to_ref"])

    def test_says_what_moving_the_root_pin_brings(self):
        # An overlay commit pushed earlier and never rolled out: renovate.json only.
        with open(os.path.join(self.overlay, "renovate.json"), "w") as handle:
            handle.write("{}\n")
        git(self.overlay, "add", "renovate.json")
        git(self.overlay, "commit", "-q", "-m", "Follow Policy")
        git(self.overlay, "push", "-q", "origin", "HEAD:main")
        self.desk.refresh(fetch=False)
        progress = self.desk.state()["progress"]
        self.assertEqual(progress["stage"], "pushed")
        self.assertEqual(progress["pending"]["files"], ["renovate.json"])
        self.assertEqual(progress["pending"]["applied"], [], "renovate.json changes nothing Argo CD renders")
        self.assertEqual(progress["running_ref"], progress["origin_ref"], "the base stays where it is")
        # A Desk step on top: now the pin move brings the base along.
        steps = self.desk.state()["path"]["steps"]
        self.desk.plan(steps[0]["sha"])
        self.desk.write(steps[0]["sha"])
        self.desk.commit()
        self.desk.push()
        progress = self.desk.state()["progress"]
        self.assertIn("kustomization.yaml", progress["pending"]["applied"])
        self.assertEqual((progress["running_ref"], progress["origin_ref"]), (self.fx.start, self.fx.jitsi))

    def test_refuses_to_write_over_other_changes(self):
        self.desk.refresh(fetch=False)
        steps = self.desk.state()["path"]["steps"]
        self.desk.plan(steps[0]["sha"])
        with open(os.path.join(self.overlay, "notes.txt"), "w") as handle:
            handle.write("mine\n")
        with self.assertRaises(engine.DeskError):
            self.desk.write(steps[0]["sha"])

    def test_a_render_that_still_reads_head_is_undone(self):
        self.desk.refresh(fetch=False)
        steps = self.desk.state()["path"]["steps"]
        self.desk.plan(steps[0]["sha"])
        self.rendered = "HEAD"
        with self.assertRaises(engine.DeskError):
            self.desk.write(steps[0]["sha"])
        self.assertEqual(pins.read(self.overlay).ref, self.fx.start)
        self.assertEqual(self.desk.overlay.status()["dirty"], [])


if __name__ == "__main__":
    unittest.main(verbosity=2)
