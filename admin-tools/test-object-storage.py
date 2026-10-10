#!/usr/bin/env python3
"""Tests for the object-storage choice (docs/adr/0053).

Three layers, each closing a gap the others leave open:

  1. the patch generator on its own — validation, idempotence, removal;
  2. the generated patches against the real manifests, rendered with
     `kubectl kustomize` (skipped when kubectl or helm is missing) — a JSON
     patch whose path has drifted fails here rather than in Argo CD;
  3. the init-job scripts, extracted from their manifests and run against stub
     `kubectl` and `curl` — the bucket names they create must be the ones the
     generator points the consumers at.

    python3 admin-tools/test-object-storage.py
"""
import importlib.util
import json
import os
import shutil
import stat
import subprocess
import sys
import tempfile
import textwrap
import unittest
from pathlib import Path

try:
    import yaml
except ImportError:  # the generator itself needs no PyYAML; only these tests do
    yaml = None

ROOT = Path(__file__).resolve().parent.parent
K8S = ROOT / "infrastructure" / "kubernetes"

_spec = importlib.util.spec_from_file_location("storage", ROOT / "admin-tools" / "generate_storage_patches.py")
storage = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(storage)

PRIMARY = ("https://s3.provider.example", "eu-central-1")
BACKUP = ("https://backup.other.example:8443", "fra1")
PREFIX = "sw-demo"


# ----------------------------------------------------------------- generator
class GeneratorTest(unittest.TestCase):
    def test_endpoint_is_split_for_each_kind_of_consumer(self):
        self.assertEqual(storage.parse_endpoint("https://s3.example.com/"),
                         ("https://s3.example.com", "s3.example.com", 443, True))
        self.assertEqual(storage.parse_endpoint("http://minio.lan:9000"),
                         ("http://minio.lan:9000", "minio.lan", 9000, False))

    def test_a_bucket_url_is_refused(self):
        # Pasting the bucket URL is the likely mistake, and it would otherwise
        # quietly become part of every object key.
        for bad in ("https://s3.example.com/my-bucket", "s3.example.com", "ftp://x", ""):
            with self.assertRaises(storage.StorageConfigError, msg=bad):
                storage.parse_endpoint(bad)

    def test_prefix_must_leave_room_for_the_longest_suffix(self):
        storage.validate_prefix("sw-demo")
        storage.validate_prefix("a" * 30)
        for bad in ("", "ab", "a" * 31, "Upper", "-lead", "trail-", "under_score"):
            with self.assertRaises(storage.StorageConfigError, msg=bad):
                storage.validate_prefix(bad)
        longest = max(storage.bucket_names("a" * 30, "pod-gateway").values(), key=len)
        self.assertLessEqual(len(longest), 63)

    def test_rerun_replaces_and_garage_removes(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "kustomization.yaml"
            original = "resources:\n  - somewhere\n\npatches:\n  - target:\n      kind: Ingress\n    patch: |-\n      - op: test\n"
            path.write_text(original)

            first = storage.generate_patches("keycloak", PRIMARY, BACKUP, PREFIX)
            self.assertTrue(storage.apply_to_kustomization(path, first))
            once = path.read_text()
            self.assertFalse(storage.apply_to_kustomization(path, first), "second run must be a no-op")

            other = storage.generate_patches("keycloak", PRIMARY, BACKUP, "sw-other")
            storage.apply_to_kustomization(path, other)
            self.assertEqual(path.read_text().count(storage.END), 1)
            self.assertIn("sw-other-postgres-backups-keycloak", path.read_text())
            self.assertNotIn(PREFIX, path.read_text())

            storage.apply_to_kustomization(path, "")
            self.assertEqual(path.read_text(), original)
            self.assertNotEqual(once, original)

    def test_an_app_with_nothing_literal_gets_no_block(self):
        # excalidraw, jitsi and friends hold no S3 coordinates at all.
        self.assertEqual(storage.generate_patches("jitsi", PRIMARY, BACKUP, PREFIX), "")


# -------------------------------------------------------------------- render
def _can_render():
    return yaml is not None and shutil.which("kubectl") and shutil.which("helm")


def _render(directory):
    result = subprocess.run(["kubectl", "kustomize", "--enable-helm", str(directory)],
                            capture_output=True, text=True, timeout=600)
    if result.returncode != 0:
        raise AssertionError(f"kustomize failed for {directory}:\n{result.stderr}")
    return [doc for doc in yaml.safe_load_all(result.stdout) if doc]


def _find(docs, kind, name):
    hits = [d for d in docs if d["kind"] == kind and d["metadata"]["name"] == name]
    return hits[0] if hits else None


@unittest.skipUnless(_can_render(), "needs kubectl, helm and PyYAML")
class RenderTest(unittest.TestCase):
    """The overlay a real install would produce, with the remote base swapped
    for this checkout."""

    def overlay(self, app, base):
        # Inside the repo, so the relative path to the base stays short and
        # kustomize's load restrictions are not in play.
        tmp = Path(tempfile.mkdtemp(prefix=".storage-test-", dir=ROOT))
        self.addCleanup(shutil.rmtree, tmp, True)
        path = tmp / "kustomization.yaml"
        path.write_text(f"resources:\n  - {os.path.relpath(base, tmp)}\n")
        storage.apply_to_kustomization(path, storage.generate_patches(app, PRIMARY, BACKUP, PREFIX))
        return tmp

    def test_database_backups_follow_the_backup_side(self):
        for app, store, bucket in (("keycloak", "postgres-backups-keycloak", "sw-demo-postgres-backups-keycloak"),
                                   ("immich", "postgres-backups", "sw-demo-postgres-backups")):
            with self.subTest(app=app):
                docs = _render(self.overlay(app, K8S / "tenants" / app))
                spec = _find(docs, "ObjectStore", store)["spec"]
                self.assertEqual(spec["configuration"]["destinationPath"], f"s3://{bucket}/")
                self.assertEqual(spec["configuration"]["endpointURL"], "https://backup.other.example:8443")
                self.assertEqual({e["name"]: e["value"] for e in spec["instanceSidecarConfiguration"]["env"]},
                                 {"AWS_REGION": "fra1", "AWS_DEFAULT_REGION": "fra1"})

    def test_nextcloud_talks_to_the_primary_side(self):
        docs = _render(self.overlay("nextcloud", K8S / "tenants" / "nextcloud"))
        container = _find(docs, "Deployment", "nextcloud")["spec"]["template"]["spec"]["containers"][0]
        env = {e["name"]: e.get("value") for e in container["env"]}
        self.assertEqual(env["OBJECTSTORE_S3_HOST"], "s3.provider.example")
        self.assertEqual(env["OBJECTSTORE_S3_PORT"], "443")
        self.assertEqual(env["OBJECTSTORE_S3_SSL"], "true")
        self.assertEqual(env["OBJECTSTORE_S3_REGION"], "eu-central-1")
        self.assertEqual(env["OBJECTSTORE_S3_BUCKET"], "sw-demo-nextcloud")
        # The credential reference the chart rendered must survive the merge.
        self.assertIn("OBJECTSTORE_S3_KEY", env)
        self.assertEqual(len(env), len(container["env"]), "an env name was duplicated by the patch")

    def test_pod_gateway_keeps_its_archive_on_the_backup_side(self):
        docs = _render(self.overlay("pod-gateway", K8S / "tenants" / "pod-gateway"))
        data = _find(docs, "ConfigMap", "pod-gateway-config")["data"]
        self.assertEqual((data["S3_ENDPOINT"], data["S3_REGION"], data["S3_BUCKET"]),
                         ("https://backup.other.example:8443", "fra1", "sw-demo-pod-gateway"))

    def test_root_drops_garage_and_repoints_velero(self):
        docs = _render(self.overlay("root", K8S))
        for application in storage.GARAGE_ONLY_APPLICATIONS:
            self.assertIsNone(_find(docs, "Application", application), application)
        for volume in storage.GARAGE_ONLY_VOLUMES:
            self.assertIsNone(_find(docs, "PersistentVolume", volume), volume)
        # Immich without S3 support still needs its library volume.
        self.assertIsNotNone(_find(docs, "PersistentVolume", "immich-library-pv"))

        # The patch Argo CD will apply inside the Velero source has to fit the
        # resource the chart renders there — apply it the same way to find out.
        inner = _find(docs, "Application", "velero")["spec"]["source"]["kustomize"]["patches"]
        tmp = Path(tempfile.mkdtemp(prefix=".storage-test-", dir=ROOT))
        self.addCleanup(shutil.rmtree, tmp, True)
        (tmp / "kustomization.yaml").write_text(yaml.safe_dump({
            "resources": [os.path.relpath(K8S / "tenants" / "velero", tmp)], "patches": inner}))
        location = _find(_render(tmp), "BackupStorageLocation", "default")["spec"]
        self.assertEqual(location["objectStorage"]["bucket"], "sw-demo-velero-backups")
        self.assertEqual(location["config"]["region"], "fra1")
        self.assertEqual(location["config"]["s3Url"], "https://backup.other.example:8443")

    def test_gallery_is_immich_with_other_images_and_s3(self):
        """docs/adr/0054: the Gallery variant, in both storage modes."""
        immich = _render(K8S / "tenants" / "immich")
        gallery = _render(K8S / "tenants" / "gallery")

        def container(docs, name):
            return _find(docs, "Deployment", name)["spec"]["template"]["spec"]["containers"][0]

        self.assertTrue(container(gallery, "immich-server")["image"]
                        .startswith("ghcr.io/open-noodle/gallery-server:v"))
        self.assertTrue(container(gallery, "immich-machine-learning")["image"]
                        .startswith("ghcr.io/open-noodle/gallery-ml:v"))
        self.assertTrue(container(immich, "immich-server")["image"]
                        .startswith("ghcr.io/immich-app/immich-server:"), "the Immich tenant must not move")

        env = {e["name"]: e for e in container(gallery, "immich-server")["env"]}
        self.assertEqual(env["IMMICH_STORAGE_BACKEND"]["value"], "s3")
        # Every coordinate comes from the Secret, so this manifest is the same
        # for Garage and for a provider and names neither.
        for name, key in (("IMMICH_S3_BUCKET", "bucket"), ("IMMICH_S3_REGION", "region"),
                          ("IMMICH_S3_ENDPOINT", "endpointURL"),
                          ("IMMICH_S3_ACCESS_KEY_ID", "accessKeyId"),
                          ("IMMICH_S3_SECRET_ACCESS_KEY", "secretAccessKey")):
            self.assertEqual(env[name]["valueFrom"]["secretKeyRef"],
                             {"name": "garage-secret", "key": key}, name)
        self.assertIn("DB_HOSTNAME", env, "the chart's own environment must survive the patch")
        self.assertNotIn("IMMICH_STORAGE_BACKEND",
                         {e["name"] for e in container(immich, "immich-server")["env"]})

        # The Recovery Point follows the photos from the PVC to the bucket.
        self.assertIsNone(_find(gallery, "CronJob", "pv-backup"))
        self.assertIsNone(_find(gallery, "CronJob", "immich-pod-export"))
        backup = _find(gallery, "CronJob", "immich-originals-backup")
        self.assertEqual(backup["metadata"]["namespace"], "immich")
        self.assertIsNotNone(_find(immich, "CronJob", "pv-backup"))

        # Everything else is the Immich tenant, resource for resource.
        def names(docs):
            return {(d["kind"], d["metadata"]["name"]) for d in docs
                    if d["kind"] not in ("CronJob", "ConfigMap")}
        self.assertEqual(names(gallery), names(immich))

    def test_gallery_takes_the_same_storage_patches_as_immich(self):
        docs = _render(self.overlay("immich", K8S / "tenants" / "gallery"))
        spec = _find(docs, "ObjectStore", "postgres-backups")["spec"]
        self.assertEqual(spec["configuration"]["destinationPath"], "s3://sw-demo-postgres-backups/")

    def test_base_without_patches_still_names_garage(self):
        # The default must not move: an overlay that says nothing gets Garage.
        docs = _render(K8S)
        for application in storage.GARAGE_ONLY_APPLICATIONS:
            self.assertIsNotNone(_find(docs, "Application", application), application)


# ---------------------------------------------------------------- init jobs
STUB = textwrap.dedent('''\
    #!/usr/bin/env python3
    """Stand-in for kubectl, curl and cat while an init-job script runs."""
    import base64, json, os, sys
    name, argv = os.path.basename(sys.argv[0]), sys.argv[1:]
    state_path = os.environ["STUB_STATE"]
    state = json.load(open(state_path))
    def save():
        json.dump(state, open(state_path, "w"))
    def arg_after(flag):
        return argv[argv.index(flag) + 1] if flag in argv else None

    if name == "cat":
        if argv and argv[0].endswith("serviceaccount/namespace"):
            sys.stdout.write(state["namespace"])
        else:
            for path in argv:
                sys.stdout.write(open(path).read())
            if not argv:
                sys.stdout.write(sys.stdin.read())
        sys.exit(0)

    if name == "curl":
        bucket = argv[-1].rsplit("/", 1)[1]
        endpoint = argv[-1].rsplit("/", 1)[0]
        user = arg_after("--user")
        state["requests"].append([("HEAD" if "--head" in argv else arg_after("-X")), endpoint, bucket, user,
                                  arg_after("--aws-sigv4")])
        if "--head" not in argv and arg_after("-X") == "PUT" and state["may_create"]:
            state["buckets"].append(bucket)
        save()
        sys.stdout.write("200" if bucket in state["buckets"] else "404")
        sys.exit(0)

    # kubectl
    if argv[:2] == ["get", "secret"]:
        secret, namespace = argv[2], arg_after("-n")
        stored = state["secrets"].get(f"{namespace}/{secret}")
        if stored is None:
            sys.exit(1)
        output = arg_after("-o") or ""
        if output.startswith("jsonpath={.data."):
            key = output[len("jsonpath={.data."):-1]
            sys.stdout.write(base64.b64encode(stored.get(key, "").encode()).decode())
        sys.exit(0)
    if argv[:2] == ["get", "pods"]:
        sys.stdout.write("pod/garage-0\\n" if "name" in argv else "true")
        sys.exit(0)
    if argv[0] == "exec":
        command = argv[argv.index("--") + 1:]
        state["garage"].append([arg_after("-n")] + command[1:])
        save()
        if command[1:3] == ["key", "create"]:
            print(f"Key ID: GK{command[3]}\\nSecret key: secret-of-{command[3]}")
        elif command[1:] == ["status"]:
            print("0123456789abcdef  garage-0  10.0.0.1")
        sys.exit(0)
    if argv[:3] == ["create", "secret", "generic"]:
        data = dict(a[len("--from-literal="):].split("=", 1) for a in argv if a.startswith("--from-literal="))
        for a in argv:
            if a.startswith("--from-file="):
                key, path = a[len("--from-file="):].split("=", 1)
                data[key] = open(path).read()
        ref = f"{arg_after('-n')}/{argv[3]}"
        if "--dry-run=client" in argv:
            print(json.dumps({"ref": ref, "data": data}))
        else:
            state["secrets"][ref] = data
            save()
        sys.exit(0)
    if argv[:2] == ["apply", "-f"]:
        doc = json.loads(sys.stdin.read())
        state["secrets"][doc["ref"]] = doc["data"]
        save()
        sys.exit(0)
    if argv[:2] == ["patch", "secret"]:
        ref = f"{arg_after('-n')}/{argv[2]}"
        state["secrets"].setdefault(ref, {}).update(json.loads(arg_after("-p"))["stringData"])
        save()
        sys.exit(0)
    if argv[:2] == ["delete", "secret"]:
        state["secrets"].pop(f"{arg_after('-n')}/{argv[2]}", None)
        save()
        sys.exit(0)
    sys.stderr.write(f"stub kubectl: unhandled {argv}\\n")
    sys.exit(64)
''')

EXTERNAL = {
    "endpoint": PRIMARY[0], "region": PRIMARY[1],
    "accessKeyId": "PRIMARYKEY", "secretAccessKey": "primary-secret",
    "bucketPrefix": PREFIX,
}
SEPARATE_BACKUP = {
    "backupEndpoint": BACKUP[0], "backupRegion": BACKUP[1],
    "backupAccessKeyId": "BACKUPKEY", "backupSecretAccessKey": "backup-secret",
}


@unittest.skipUnless(yaml is not None and shutil.which("bash"), "needs bash and PyYAML")
class InitJobTest(unittest.TestCase):
    def run_job(self, manifest, namespace, external=None, env=None, may_create=True, buckets=(),
                secrets=None):
        tmp = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, tmp, True)
        stub = tmp / "stub.py"
        stub.write_text(STUB)
        stub.chmod(stub.stat().st_mode | stat.S_IEXEC)
        for name in ("kubectl", "curl", "cat"):
            (tmp / name).symlink_to(stub)
        state = {"namespace": namespace, "secrets": dict(secrets or {}), "requests": [], "garage": [],
                 "buckets": list(buckets), "may_create": may_create}
        if external is not None:
            state["secrets"]["default/smallworlds-object-storage"] = external
        (tmp / "state.json").write_text(json.dumps(state))

        job = yaml.safe_load((K8S / manifest).read_text())
        container = job["spec"]["template"]["spec"]["containers"][0]
        job_env = {e["name"]: e["value"] for e in container.get("env", [])}
        job_env.update(env or {})
        result = subprocess.run(
            ["bash", "-c", container["command"][-1]], capture_output=True, text=True, timeout=60,
            env={**job_env, "PATH": f"{tmp}:{os.environ['PATH']}", "STUB_STATE": str(tmp / "state.json")})
        return result, json.loads((tmp / "state.json").read_text())

    def test_tenant_job_external_single_account(self):
        result, state = self.run_job("bases/garage-init-job/garage-init-job.yaml", "nextcloud", EXTERNAL)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(state["garage"], [], "Garage must not be touched in external mode")

        names = storage.bucket_names(PREFIX, "nextcloud")
        self.assertEqual(sorted(state["buckets"]),
                         sorted([names["tenant"], names["tenant-backup"], names["postgres"]]))
        own = state["secrets"]["nextcloud/garage-secret"]
        self.assertEqual(own["bucket"], names["tenant"])
        self.assertEqual((own["endpointURL"], own["endpointHost"], own["useSSL"], own["region"]),
                         ("https://s3.provider.example", "s3.provider.example", "true", "eu-central-1"))
        # Both spellings of the key exist, because consumers disagree on them.
        self.assertEqual((own["accessKey"], own["accessKeyId"]), ("PRIMARYKEY", "PRIMARYKEY"))
        self.assertEqual(state["secrets"]["nextcloud/garage-secret-cnpg"]["bucket"], names["postgres"])
        self.assertEqual(state["secrets"]["nextcloud/garage-secret-backup"]["bucket"], names["tenant-backup"])
        self.assertTrue(all(request[4] == "aws:amz:eu-central-1:s3" for request in state["requests"]))

    def test_tenant_job_external_separate_backup_account(self):
        result, state = self.run_job("bases/garage-init-job/garage-init-job.yaml", "forgejo",
                                     {**EXTERNAL, **SEPARATE_BACKUP})
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(state["secrets"]["forgejo/garage-secret"]["accessKeyId"], "PRIMARYKEY")
        for name in ("garage-secret-cnpg", "garage-secret-backup"):
            secret = state["secrets"][f"forgejo/{name}"]
            self.assertEqual((secret["accessKeyId"], secret["endpointURL"], secret["region"]),
                             ("BACKUPKEY", "https://backup.other.example:8443", "fra1"))
        by_bucket = {request[2]: request for request in state["requests"]}
        self.assertEqual(by_bucket["sw-demo-forgejo"][3], "PRIMARYKEY:primary-secret")
        self.assertEqual(by_bucket["sw-demo-postgres-backups"][3], "BACKUPKEY:backup-secret")

    def test_pod_archive_lands_on_the_backup_account(self):
        result, state = self.run_job("bases/garage-init-job/garage-init-job.yaml", "pod-gateway",
                                     {**EXTERNAL, **SEPARATE_BACKUP},
                                     env={"TENANT_BUCKET_NAMESPACE": "garage-backup-system"})
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        own = state["secrets"]["pod-gateway/garage-secret"]
        self.assertEqual((own["accessKeyId"], own["endpointURL"], own["bucket"]),
                         ("BACKUPKEY", "https://backup.other.example:8443", "sw-demo-pod-gateway"))

    def test_a_key_that_cannot_create_buckets_is_fine_when_they_exist(self):
        names = storage.bucket_names(PREFIX, "plane")
        result, state = self.run_job("bases/garage-init-job/garage-init-job.yaml", "plane", EXTERNAL,
                                     may_create=False,
                                     buckets=[names["tenant"], names["tenant-backup"], names["postgres"]])
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertFalse([r for r in state["requests"] if r[0] == "PUT"])

    def test_a_missing_bucket_that_cannot_be_created_fails_by_name(self):
        result, state = self.run_job("bases/garage-init-job/garage-init-job.yaml", "plane", EXTERNAL,
                                     may_create=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("sw-demo-plane", result.stdout)
        self.assertNotIn("plane/garage-secret", state["secrets"],
                         "no credential may be handed out for a bucket that is not there")

    def test_an_incomplete_external_secret_is_refused(self):
        result, _ = self.run_job("bases/garage-init-job/garage-init-job.yaml", "plane",
                                 {**EXTERNAL, "bucketPrefix": ""})
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("incomplete", result.stdout)

    def test_tenant_job_garage_describes_its_secrets(self):
        result, state = self.run_job("bases/garage-init-job/garage-init-job.yaml", "nextcloud")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(state["requests"], [], "nothing may leave the cluster in Garage mode")
        own = state["secrets"]["nextcloud/garage-secret"]
        self.assertEqual((own["endpointURL"], own["endpointHost"], own["useSSL"], own["region"], own["bucket"]),
                         ("http://garage.garage-system.svc.cluster.local:3900",
                          "garage.garage-system.svc.cluster.local:3900", "false", "garage", "nextcloud"))
        self.assertEqual(own["accessKeyId"], "GKnextcloud-key")
        backup = state["secrets"]["nextcloud/garage-secret-backup"]
        self.assertEqual((backup["endpointURL"], backup["bucket"]),
                         ("http://garage-backup.garage-backup-system.svc.cluster.local:3900", "nextcloud"))
        self.assertEqual(state["secrets"]["nextcloud/garage-secret-cnpg"]["bucket"], "postgres-backups")

    def test_garage_mode_upgrades_a_secret_that_predates_the_new_keys(self):
        # A cluster installed before the coordinates travelled with the key:
        # the next sync has to add them without rotating the key itself.
        old = {"accessKey": "OLD", "secretKey": "old", "accessKeyId": "OLD", "secretAccessKey": "old"}
        result, state = self.run_job("bases/garage-init-job/garage-init-job.yaml", "forgejo",
                                     secrets={"forgejo/garage-secret": dict(old),
                                              "forgejo/garage-secret-backup": dict(old)})
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        own = state["secrets"]["forgejo/garage-secret"]
        self.assertEqual(own["accessKeyId"], "OLD", "an existing key must not be rotated")
        self.assertEqual((own["endpointHost"], own["bucket"]),
                         ("garage.garage-system.svc.cluster.local:3900", "forgejo"))
        self.assertEqual(state["secrets"]["forgejo/garage-secret-backup"]["region"], "garage")

    def test_velero_and_keycloak_use_the_names_the_generator_patches_in(self):
        names = storage.bucket_names(PREFIX)
        cases = (("bases/velero-garage-init-job/velero-garage-init-job.yaml", "velero", names["velero"],
                  "velero/velero-s3-creds"),
                 ("tenants/keycloak/garage-init-job.yaml", "keycloak", names["postgres-keycloak"],
                  "keycloak/garage-secret"))
        for manifest, namespace, bucket, secret in cases:
            with self.subTest(namespace=namespace):
                result, state = self.run_job(manifest, namespace, {**EXTERNAL, **SEPARATE_BACKUP})
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertEqual(state["buckets"], [bucket])
                self.assertEqual(state["garage"], [])
                self.assertEqual(state["requests"][0][3], "BACKUPKEY:backup-secret")
                self.assertIn("BACKUPKEY", json.dumps(state["secrets"][secret]))

    def test_velero_and_keycloak_garage_path_is_unchanged(self):
        for manifest, namespace, secret in (
                ("bases/velero-garage-init-job/velero-garage-init-job.yaml", "velero", "velero/velero-s3-creds"),
                ("tenants/keycloak/garage-init-job.yaml", "keycloak", "keycloak/garage-secret")):
            with self.subTest(namespace=namespace):
                result, state = self.run_job(manifest, namespace)
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertEqual(state["requests"], [])
                self.assertTrue(state["garage"])
                self.assertIn(secret, state["secrets"])


if __name__ == "__main__":
    unittest.main(verbosity=2)
