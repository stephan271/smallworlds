#!/usr/bin/env python3
"""Tests for release-notes.py on synthetic snapshots (no git, no rendering)."""
import importlib.util
import pathlib
import sys
import unittest

spec = importlib.util.spec_from_file_location(
    "release_notes", pathlib.Path(__file__).resolve().parent / "release-notes.py")
rn = importlib.util.module_from_spec(spec)
spec.loader.exec_module(rn)


def snap(tenants=None, apps=None, pins=None):
    return {"tenants": {k: {"images": set(v[0]), "charts": set(v[1])} for k, v in (tenants or {}).items()},
            "apps": apps or {}, "pins": pins or {}}


def no_commits(old, new, path):
    return []


class Images(unittest.TestCase):
    def test_images_are_read_from_rendered_yaml(self):
        rendered = """
        containers:
          - name: server
            image: ghcr.io/open-noodle/gallery-server:v5.10.0@sha256:abc
          - image: "redis:7.4"
        imageName: ghcr.io/cloudnative-pg/postgresql:16.4
        """
        self.assertEqual({"ghcr.io/open-noodle/gallery-server:v5.10.0@sha256:abc", "redis:7.4",
                          "ghcr.io/cloudnative-pg/postgresql:16.4"}, rn.images_in(rendered))

    def test_a_moved_tag_is_one_line_and_a_new_digest_is_none(self):
        old = {"ghcr.io/o/server:v5.8.0@sha256:1", "redis:7.4@sha256:a"}
        new = {"ghcr.io/o/server:v5.10.0@sha256:2", "redis:7.4@sha256:b"}
        self.assertEqual(["`ghcr.io/o/server` v5.8.0 → v5.10.0"], rn.changes(old, new))

    def test_added_and_removed_images(self):
        lines = rn.changes({"a:1"}, {"b:2"})
        self.assertEqual(["`a` removed (was 1)", "`b` new: 2"], lines)

    def test_a_registry_port_is_not_a_tag(self):
        self.assertEqual(("registry:5000/app", ""), rn._split("registry:5000/app"))

    def test_charts_are_read_from_a_kustomization(self):
        d = pathlib.Path(self.tmp())
        (d / "kustomization.yaml").write_text(
            "helmCharts:\n  - name: jitsi-meet\n    repo: https://x\n    releaseName: jitsi\n"
            "    version: 2.20.0\n    valuesFile: values.yaml\n")
        self.assertEqual({"jitsi-meet@2.20.0"}, rn.charts_in_tenant(d))

    def tmp(self):
        import tempfile
        d = tempfile.mkdtemp()
        self.addCleanup(__import__("shutil").rmtree, d)
        return d


class Compose(unittest.TestCase):
    GALLERY_OLD = (["ghcr.io/open-noodle/gallery-server:v5.8.0", "ghcr.io/open-noodle/gallery-ml:v5.8.0"], [])
    GALLERY_NEW = (["ghcr.io/open-noodle/gallery-server:v5.10.0", "ghcr.io/open-noodle/gallery-ml:v5.10.0"], [])

    def notes(self, old, new, message="Release v1.3.0 (minor, security)\n\nCut from abc.\n"
              "Ring 0: verified 2026-11-13T09:00:00Z, 75 h before this release", commits=no_commits):
        return rn.compose("v1.2.46", "v1.3.0", old, new, message, commits=commits)

    def test_kind_and_security_flag_come_from_the_tag(self):
        text = self.notes(snap(), snap())
        self.assertIn("**Kind:** minor · **Security**", text)
        self.assertIn("Ring 0: verified 2026-11-13T09:00:00Z, 75 h", text)

    def test_an_application_gets_its_own_section_with_todos(self):
        text = self.notes(snap({"gallery": self.GALLERY_OLD}), snap({"gallery": self.GALLERY_NEW}))
        self.assertIn("### Photos (Gallery)", text)
        self.assertIn("`ghcr.io/open-noodle/gallery-server` v5.8.0 → v5.10.0", text)
        self.assertIn("**Database migration:** TODO", text)
        self.assertIn("**Rollback:** TODO", text)
        self.assertIn("Soaked on Ring 0: Photos (Gallery)", text)

    def test_a_variant_ring0_does_not_run_is_marked_fresh_install_only(self):
        text = self.notes(snap({"immich": self.GALLERY_OLD}), snap({"immich": self.GALLERY_NEW}))
        self.assertIn("Fresh install tested only, not soaked", text)
        self.assertIn("Photos (Immich)", text)

    def test_platform_collects_charts_pins_and_other_tenants(self):
        text = self.notes(
            snap({"keycloak": (["quay.io/keycloak/keycloak:26.4"], [])},
                 {"traefik": "traefik@37.1.0"}, {"K3S_VERSION": "v1.36.4+k3s1"}),
            snap({"keycloak": (["quay.io/keycloak/keycloak:26.5"], [])},
                 {"traefik": "traefik@37.2.0"}, {"K3S_VERSION": "v1.36.5+k3s1"}))
        platform = text.split("## Platform", 1)[1].split("## Evidence", 1)[0]
        self.assertIn("**keycloak**: `quay.io/keycloak/keycloak` 26.4 → 26.5", platform)
        self.assertIn("**traefik** chart: traefik@37.1.0 → traefik@37.2.0", platform)
        self.assertIn("`K3S_VERSION`: v1.36.4+k3s1 → v1.36.5+k3s1 (reaches new and rebuilt nodes only)", platform)
        self.assertNotIn("### ", text.split("## Platform")[0].split("## Applications")[1])

    def test_ring0_only_tenants_are_left_out(self):
        text = self.notes(snap({"canary-follower": (["python:3.14"], [])}),
                          snap({"canary-follower": (["python:3.15"], [])}))
        self.assertNotIn("canary-follower", text)

    def test_unchanged_applications_are_left_out(self):
        text = self.notes(snap({"jitsi": (["jitsi/web:1"], [])}), snap({"jitsi": (["jitsi/web:1"], [])}))
        self.assertIn("No application changed.", text)

    def test_commits_touching_an_app_are_listed_even_without_a_version_change(self):
        def commits(old, new, path):
            return ["1087aec Keep Jitsi's XMPP passwords stable"] if path.endswith("/jitsi") else []
        text = self.notes(snap({"jitsi": (["j:1"], [])}), snap({"jitsi": (["j:1"], [])}), commits=commits)
        self.assertIn("### Meet (Jitsi)", text)
        self.assertIn("1087aec Keep Jitsi's XMPP passwords stable", text)

    def test_an_ungated_release_says_so(self):
        text = self.notes(snap(), snap(), "Release v1.3.0 (patch)\n\n"
                          "Released WITHOUT the Ring 0 gate: emergency")
        self.assertIn("Released WITHOUT the Ring 0 gate: emergency", text)


if __name__ == "__main__":
    unittest.main(verbosity=1)
