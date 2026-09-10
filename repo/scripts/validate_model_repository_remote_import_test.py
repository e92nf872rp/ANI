#!/usr/bin/env python3
from __future__ import annotations

import copy
import pathlib
import shutil
import tempfile
import unittest

import yaml

import validate_model_repository_remote_import as validator


class ModelRepositoryRemoteImportContractTest(unittest.TestCase):
    def _copy_migrations(self, temporary: str | pathlib.Path) -> pathlib.Path:
        root = pathlib.Path(__file__).resolve().parents[1]
        destination = pathlib.Path(temporary) / "deploy" / "migrations"
        destination.parent.mkdir(parents=True, exist_ok=True)
        shutil.copytree(root / "deploy/migrations", destination)
        return destination

    def test_current_profile_contains_worker_and_fail_closed_image_contract(self) -> None:
        validator.validate_workspace(pathlib.Path(__file__).resolve().parents[1])

    def test_manifest_rejects_worker_image_drift_from_configmap(self) -> None:
        root = pathlib.Path(__file__).resolve().parents[1]
        manifest = root / "deploy/real-k8s-lab/inference-incluster-e2e.yaml"
        docs = list(yaml.safe_load_all(manifest.read_text(encoding="utf-8")))
        broken = copy.deepcopy(docs)
        worker = next(
            item
            for item in broken
            if item and item.get("kind") == "Deployment" and item.get("metadata", {}).get("name") == "model-import-worker"
        )
        worker["spec"]["template"]["spec"]["containers"][0]["image"] = (
            "docker.changqingyun.cn/ani/model-import-worker@sha256:"
            "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
        )
        with tempfile.TemporaryDirectory() as temporary:
            path = pathlib.Path(temporary) / "profile.yaml"
            path.write_text(yaml.safe_dump_all(broken), encoding="utf-8")
            with self.assertRaisesRegex(AssertionError, "must match ConfigMap"):
                validator.validate_manifest(path)

    def test_atlas_migration_checksum_matches_all_sql_files(self) -> None:
        validator.validate_migration_checksum(pathlib.Path(__file__).resolve().parents[1])

    def test_atlas_migration_checksum_rejects_root_mutation(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            migrations = self._copy_migrations(temporary)
            checksum = migrations / "atlas.sum"
            lines = checksum.read_text(encoding="utf-8").splitlines()
            lines[0] = "h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
            checksum.write_text("\n".join(lines) + "\n", encoding="utf-8")
            with self.assertRaisesRegex(AssertionError, "root mismatch"):
                validator.validate_migration_checksum(pathlib.Path(temporary))

    def test_atlas_migration_checksum_rejects_migration_mutation(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            migrations = self._copy_migrations(temporary)
            migration = migrations / "20260904000100_model_import_resolved_revision.sql"
            migration.write_text(migration.read_text(encoding="utf-8") + "\n-- mutation\n", encoding="utf-8")
            with self.assertRaisesRegex(AssertionError, "checksum mismatch"):
                validator.validate_migration_checksum(pathlib.Path(temporary))

    def test_fetcher_image_build_target_and_help_are_registered(self) -> None:
        root = pathlib.Path(__file__).resolve().parents[1]
        makefile = (root / "Makefile").read_text(encoding="utf-8")
        self.assertIn("image-model-fetcher", makefile)
        self.assertIn("make image-model-fetcher", makefile)
        self.assertIn("-f services/model-fetcher/Dockerfile", makefile)

    def test_fetcher_dockerfile_pins_every_base_image(self) -> None:
        root = pathlib.Path(__file__).resolve().parents[1]
        dockerfile = (root / "services/model-fetcher/Dockerfile").read_text(encoding="utf-8")
        from_lines = [line.strip() for line in dockerfile.splitlines() if line.strip().startswith("FROM ")]
        self.assertGreaterEqual(len(from_lines), 2)
        for line in from_lines:
            self.assertRegex(line, r"^FROM\s+\S+@sha256:[0-9a-f]{64}(?:\s+AS\s+\S+)?$")

    def test_model_service_dockerfile_pins_every_base_image(self) -> None:
        root = pathlib.Path(__file__).resolve().parents[1]
        dockerfile = (root / "services/model-service/Dockerfile").read_text(encoding="utf-8")
        from_lines = [line.strip() for line in dockerfile.splitlines() if line.strip().startswith("FROM ")]
        self.assertGreaterEqual(len(from_lines), 3)
        for line in from_lines:
            self.assertRegex(line, r"^FROM\s+\S+@sha256:[0-9a-f]{64}(?:\s+AS\s+\S+)?$")

    def test_model_service_build_contract_rejects_unpinned_base_image(self) -> None:
        root = pathlib.Path(__file__).resolve().parents[1]
        dockerfile = (root / "services/model-service/Dockerfile").read_text(encoding="utf-8")
        mutable = dockerfile.replace(
            "alpine:3.20@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc",
            "alpine:3.20",
        )
        with tempfile.TemporaryDirectory() as temporary:
            temp_root = pathlib.Path(temporary)
            (temp_root / "services/model-service").mkdir(parents=True)
            (temp_root / "services/model-service/Dockerfile").write_text(mutable, encoding="utf-8")
            with self.assertRaisesRegex(AssertionError, "model-service Dockerfile FROM image must be digest pinned"):
                validator.validate_model_service_build_contract(temp_root)

    def test_fetcher_build_contract_rejects_unpinned_base_image(self) -> None:
        root = pathlib.Path(__file__).resolve().parents[1]
        makefile = (root / "Makefile").read_text(encoding="utf-8")
        dockerfile = (root / "services/model-fetcher/Dockerfile").read_text(encoding="utf-8")
        mutable = dockerfile.replace(
            "alpine:3.20@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc",
            "alpine:3.20",
        )
        with tempfile.TemporaryDirectory() as temporary:
            temp_root = pathlib.Path(temporary)
            (temp_root / "services/model-fetcher").mkdir(parents=True)
            (temp_root / "Makefile").write_text(makefile, encoding="utf-8")
            (temp_root / "services/model-fetcher/Dockerfile").write_text(mutable, encoding="utf-8")
            with self.assertRaisesRegex(AssertionError, "FROM image must be digest pinned"):
                validator.validate_fetcher_build_contract(temp_root)

    def test_fetcher_build_contract_rejects_recipe_without_fetcher_context(self) -> None:
        root = pathlib.Path(__file__).resolve().parents[1]
        makefile = (root / "Makefile").read_text(encoding="utf-8").replace(
            "-f services/model-fetcher/Dockerfile",
            "-f services/model-service/Dockerfile",
        )
        dockerfile = (root / "services/model-fetcher/Dockerfile").read_text(encoding="utf-8")
        with tempfile.TemporaryDirectory() as temporary:
            temp_root = pathlib.Path(temporary)
            (temp_root / "services/model-fetcher").mkdir(parents=True)
            (temp_root / "Makefile").write_text(makefile, encoding="utf-8")
            (temp_root / "services/model-fetcher/Dockerfile").write_text(dockerfile, encoding="utf-8")
            with self.assertRaisesRegex(AssertionError, "model-fetcher/Dockerfile"):
                validator.validate_fetcher_build_contract(temp_root)

    def test_fetcher_build_contract_rejects_non_root_build_context(self) -> None:
        root = pathlib.Path(__file__).resolve().parents[1]
        makefile = (root / "Makefile").read_text(encoding="utf-8").replace(
            "-t $(REGISTRY)/model-fetcher:$(VERSION) .",
            "-t $(REGISTRY)/model-fetcher:$(VERSION) services/model-fetcher",
        )
        dockerfile = (root / "services/model-fetcher/Dockerfile").read_text(encoding="utf-8")
        with tempfile.TemporaryDirectory() as temporary:
            temp_root = pathlib.Path(temporary)
            (temp_root / "services/model-fetcher").mkdir(parents=True)
            (temp_root / "Makefile").write_text(makefile, encoding="utf-8")
            (temp_root / "services/model-fetcher/Dockerfile").write_text(dockerfile, encoding="utf-8")
            with self.assertRaisesRegex(AssertionError, "repository root"):
                validator.validate_fetcher_build_contract(temp_root)

    def test_fetcher_build_contract_accepts_standard_multiline_recipe(self) -> None:
        root = pathlib.Path(__file__).resolve().parents[1]
        makefile = (root / "Makefile").read_text(encoding="utf-8")
        multiline_recipe = "\n".join(
            [
                "docker build \\",
                "\t\t-f services/model-fetcher/Dockerfile \\",
                "\t\t-t $(REGISTRY)/model-fetcher:$(VERSION) \\",
                "\t\t.",
            ]
        )
        makefile = makefile.replace(
            "docker build -f services/model-fetcher/Dockerfile -t $(REGISTRY)/model-fetcher:$(VERSION) .",
            multiline_recipe,
        )
        dockerfile = (root / "services/model-fetcher/Dockerfile").read_text(encoding="utf-8")
        with tempfile.TemporaryDirectory() as temporary:
            temp_root = pathlib.Path(temporary)
            (temp_root / "services/model-fetcher").mkdir(parents=True)
            (temp_root / "Makefile").write_text(makefile, encoding="utf-8")
            (temp_root / "services/model-fetcher/Dockerfile").write_text(dockerfile, encoding="utf-8")
            validator.validate_fetcher_build_contract(temp_root)

    def test_manifest_rejects_plaintext_source_credentials(self) -> None:
        root = pathlib.Path(__file__).resolve().parents[1]
        manifest = root / "deploy/real-k8s-lab/inference-incluster-e2e.yaml"
        docs = list(yaml.safe_load_all(manifest.read_text(encoding="utf-8")))
        config = next(item for item in docs if item and item.get("metadata", {}).get("name") == "ani-inference-materialization")
        worker = next(item for item in docs if item and item.get("metadata", {}).get("name") == "model-import-worker")
        worker = copy.deepcopy(worker)
        worker["spec"]["template"]["spec"]["containers"][0]["env"] = [
            {"name": "HF_TOKEN", "value": "secret-token"},
        ]
        with tempfile.TemporaryDirectory() as temporary:
            path = pathlib.Path(temporary) / "profile.yaml"
            path.write_text(yaml.safe_dump_all([config, worker]), encoding="utf-8")
            with self.assertRaises(AssertionError):
                validator.validate_manifest(path)

    def test_manifest_rejects_invalid_fetcher_http_opt_in(self) -> None:
        root = pathlib.Path(__file__).resolve().parents[1]
        manifest = root / "deploy/real-k8s-lab/inference-incluster-e2e.yaml"
        docs = list(yaml.safe_load_all(manifest.read_text(encoding="utf-8")))
        config = copy.deepcopy(next(item for item in docs if item and item.get("metadata", {}).get("name") == "ani-inference-materialization"))
        worker = next(item for item in docs if item and item.get("metadata", {}).get("name") == "model-import-worker")
        config["data"]["model_fetcher_allow_insecure_http"] = "sometimes"
        with tempfile.TemporaryDirectory() as temporary:
            path = pathlib.Path(temporary) / "profile.yaml"
            path.write_text(yaml.safe_dump_all([config, worker]), encoding="utf-8")
            with self.assertRaisesRegex(AssertionError, "must be a boolean"):
                validator.validate_manifest(path)

    def test_archive_marker_and_source_policy_are_explicit(self) -> None:
        root = pathlib.Path(__file__).resolve().parents[1]
        source = (root / "services/model-fetcher/archive_extract.go").read_text(encoding="utf-8")
        self.assertIn("model.tar.gz", source)
        self.assertIn(".ani-model-extraction-complete", source)
        self.assertIn("os.Rename", source)
        self.assertIn("TypeSymlink", source) if "TypeSymlink" in source else self.assertIn("non-regular", source)

    def test_mtls_fixture_uses_tenant_fetcher_service_account_identity(self) -> None:
        root = pathlib.Path(__file__).resolve().parents[1]
        docs = list(yaml.safe_load_all((root / "deploy/real-k8s-lab/model-repository-mtls-dev.yaml").read_text(encoding="utf-8")))
        client = next(item for item in docs if item and item.get("kind") == "Certificate" and item.get("metadata", {}).get("name") == "model-fetcher-client")
        uris = client.get("spec", {}).get("uris", [])
        self.assertIn(
            "spiffe://ani.dev/ns/ani-tenant-00000000-0000-0000-0000-000000000001/sa/ani-inference-fetcher",
            uris,
        )

    def test_mtls_fixture_requires_fetcher_client_auth_usage(self) -> None:
        root = pathlib.Path(__file__).resolve().parents[1]
        docs = list(yaml.safe_load_all((root / "deploy/real-k8s-lab/model-repository-mtls-dev.yaml").read_text(encoding="utf-8")))
        client = next(item for item in docs if item and item.get("kind") == "Certificate" and item.get("metadata", {}).get("name") == "model-fetcher-client")
        broken = copy.deepcopy(docs)
        broken_client = next(item for item in broken if item and item.get("kind") == "Certificate" and item.get("metadata", {}).get("name") == "model-fetcher-client")
        broken_client["spec"].pop("usages", None)
        with tempfile.TemporaryDirectory() as temporary:
            path = pathlib.Path(temporary) / "model-repository-mtls-dev.yaml"
            path.write_text(yaml.safe_dump_all(broken), encoding="utf-8")
            with self.assertRaises(AssertionError):
                validator.validate_mtls_dev(path)
        self.assertEqual(client["spec"].get("usages"), ["client auth"])

    def test_tenant_rbac_template_binds_gateway_without_clusterrole_expansion(self) -> None:
        root = pathlib.Path(__file__).resolve().parents[1]
        docs = list(yaml.safe_load_all((root / "deploy/real-k8s-lab/model-repository-mtls-dev.yaml").read_text(encoding="utf-8")))
        role = next(item for item in docs if item and item.get("kind") == "Role" and item.get("metadata", {}).get("name") == "ani-model-fetcher-provisioner")
        binding = next(item for item in docs if item and item.get("kind") == "RoleBinding" and item.get("metadata", {}).get("name") == "ani-model-fetcher-provisioner")
        self.assertEqual(role["metadata"].get("namespace"), "ani-tenant-00000000-0000-0000-0000-000000000001")
        self.assertEqual(binding["metadata"].get("namespace"), role["metadata"].get("namespace"))
        self.assertEqual(binding["roleRef"], {"apiGroup": "rbac.authorization.k8s.io", "kind": "Role", "name": "ani-model-fetcher-provisioner"})
        self.assertEqual(binding["subjects"], [{"kind": "ServiceAccount", "name": "ani-gateway", "namespace": "ani-system"}])
        self.assertEqual(
            role["rules"],
            [
                {"apiGroups": ["cert-manager.io"], "resources": ["certificates"], "verbs": ["get", "list", "watch", "create", "update", "patch", "delete"]},
                {"apiGroups": [""], "resources": ["serviceaccounts"], "verbs": ["get", "list", "watch", "create", "update", "patch", "delete"]},
            ],
        )

    def test_tenant_rbac_gate_rejects_missing_preprovisioned_role(self) -> None:
        root = pathlib.Path(__file__).resolve().parents[1]
        manifest = root / "deploy/real-k8s-lab/model-repository-mtls-dev.yaml"
        docs = list(yaml.safe_load_all(manifest.read_text(encoding="utf-8")))
        broken = [
            item
            for item in docs
            if not (item and item.get("kind") == "Role" and item.get("metadata", {}).get("name") == "ani-model-fetcher-provisioner")
        ]
        with tempfile.TemporaryDirectory() as temporary:
            path = pathlib.Path(temporary) / "model-repository-mtls-dev.yaml"
            path.write_text(yaml.safe_dump_all(broken), encoding="utf-8")
            with self.assertRaises(AssertionError):
                validator.validate_tenant_rbac_manifest(path)

    def test_gateway_requires_model_fetcher_9105_config_and_strict_refs(self) -> None:
        root = pathlib.Path(__file__).resolve().parents[1]
        gateway = root / "deploy/real-k8s-lab/sprint13-production-shaped-gateway-deployment.yaml"
        docs = list(yaml.safe_load_all(gateway.read_text(encoding="utf-8")))
        deployment = next(item for item in docs if item and item.get("kind") == "Deployment")
        env = deployment["spec"]["template"]["spec"]["containers"][0]["env"]
        fetcher = next(item for item in env if item.get("name") == "MODEL_FETCHER_GRPC_ADDR")
        broken = copy.deepcopy(docs)
        broken_deployment = next(item for item in broken if item and item.get("kind") == "Deployment")
        broken_env = broken_deployment["spec"]["template"]["spec"]["containers"][0]["env"]
        broken_fetcher = next(item for item in broken_env if item.get("name") == "MODEL_FETCHER_GRPC_ADDR")
        broken_fetcher["valueFrom"]["configMapKeyRef"]["optional"] = True
        with tempfile.TemporaryDirectory() as temporary:
            path = pathlib.Path(temporary) / "gateway.yaml"
            path.write_text(yaml.safe_dump_all(broken), encoding="utf-8")
            with self.assertRaises(AssertionError):
                validator.validate_gateway_manifest(path)
        self.assertEqual(fetcher["valueFrom"]["configMapKeyRef"].get("key"), "model_fetcher_grpc_addr")

    def test_gateway_clusterrole_rejects_cert_manager_certificate_access(self) -> None:
        root = pathlib.Path(__file__).resolve().parents[1]
        manifest = root / "deploy/real-k8s-lab/sprint13-production-shaped-gateway-rbac.yaml"
        docs = list(yaml.safe_load_all(manifest.read_text(encoding="utf-8")))
        broken = copy.deepcopy(docs)
        role = next(item for item in broken if item and item.get("kind") == "ClusterRole")
        role.setdefault("rules", []).append({"apiGroups": ["cert-manager.io"], "resources": ["certificates"], "verbs": ["*"]})
        with tempfile.TemporaryDirectory() as temporary:
            path = pathlib.Path(temporary) / "gateway-rbac.yaml"
            path.write_text(yaml.safe_dump_all(broken), encoding="utf-8")
            with self.assertRaises(AssertionError):
                validator.validate_gateway_rbac_manifest(path)

    def test_model_service_requires_mtls_fetcher_listener(self) -> None:
        root = pathlib.Path(__file__).resolve().parents[1]
        profile = root / "deploy/real-k8s-lab/inference-incluster-e2e.yaml"
        docs = list(yaml.safe_load_all(profile.read_text(encoding="utf-8")))
        broken = copy.deepcopy(docs)
        model_service = next(item for item in broken if item and item.get("kind") == "Deployment" and item.get("metadata", {}).get("name") == "model-service")
        env = model_service["spec"]["template"]["spec"]["containers"][0]["env"]
        next(item for item in env if item.get("name") == "MODEL_FETCHER_GRPC_PORT")["value"] = "9103"
        with tempfile.TemporaryDirectory() as temporary:
            path = pathlib.Path(temporary) / "profile.yaml"
            path.write_text(yaml.safe_dump_all(broken), encoding="utf-8")
            with self.assertRaises(AssertionError):
                validator.validate_fetcher_mtls_manifest(path)

    def test_model_service_network_policy_allows_only_control_plane_9103(self) -> None:
        root = pathlib.Path(__file__).resolve().parents[1]
        manifest = root / "deploy/real-k8s-lab/model-repository-mtls-dev.yaml"
        docs = list(yaml.safe_load_all(manifest.read_text(encoding="utf-8")))
        policy = next(item for item in docs if item and item.get("kind") == "NetworkPolicy" and item.get("metadata", {}).get("name") == "model-service-fetcher-ingress")
        ingress = policy.get("spec", {}).get("ingress", [])
        control_plane_peers = []
        for rule in ingress:
            ports = rule.get("ports", [])
            if any(port.get("protocol") == "TCP" and port.get("port") == 9103 for port in ports):
                control_plane_peers = rule.get("from", [])
                break
        expected_namespace = {"matchLabels": {"kubernetes.io/metadata.name": "ani-system"}}
        expected_peers = [
            {"namespaceSelector": expected_namespace, "podSelector": {"matchLabels": {"app.kubernetes.io/name": "ani-gateway"}}},
            {"namespaceSelector": expected_namespace, "podSelector": {"matchLabels": {"app.kubernetes.io/name": "inference-service"}}},
        ]
        self.assertEqual(control_plane_peers, expected_peers)

        broken = copy.deepcopy(docs)
        broken_policy = next(item for item in broken if item and item.get("kind") == "NetworkPolicy" and item.get("metadata", {}).get("name") == "model-service-fetcher-ingress")
        broken_policy["spec"]["ingress"] = [rule for rule in broken_policy["spec"].get("ingress", []) if not any(port.get("port") == 9103 for port in rule.get("ports", []))]
        with tempfile.TemporaryDirectory() as temporary:
            path = pathlib.Path(temporary) / "model-repository-mtls-dev.yaml"
            path.write_text(yaml.safe_dump_all(broken), encoding="utf-8")
            with self.assertRaises(AssertionError):
                validator.validate_model_service_network_policy(path)

    def test_worker_requires_a_bounded_writable_archive_workspace(self) -> None:
        root = pathlib.Path(__file__).resolve().parents[1]
        manifest = root / "deploy/real-k8s-lab/inference-incluster-e2e.yaml"
        docs = list(yaml.safe_load_all(manifest.read_text(encoding="utf-8")))
        config = next(item for item in docs if item and item.get("metadata", {}).get("name") == "ani-inference-materialization")
        worker = next(item for item in docs if item and item.get("metadata", {}).get("name") == "model-import-worker")
        worker = copy.deepcopy(worker)
        worker["spec"]["template"]["spec"].pop("volumes", None)
        worker["spec"]["template"]["spec"]["containers"][0].pop("volumeMounts", None)
        with tempfile.TemporaryDirectory() as temporary:
            path = pathlib.Path(temporary) / "profile.yaml"
            path.write_text(yaml.safe_dump_all([config, worker]), encoding="utf-8")
            with self.assertRaises(AssertionError):
                validator.validate_manifest(path)

    def test_worker_memory_limit_covers_streaming_archive_page_cache(self) -> None:
        root = pathlib.Path(__file__).resolve().parents[1]
        manifest = root / "deploy/real-k8s-lab/inference-incluster-e2e.yaml"
        docs = list(yaml.safe_load_all(manifest.read_text(encoding="utf-8")))
        config = next(item for item in docs if item and item.get("metadata", {}).get("name") == "ani-inference-materialization")
        worker = next(item for item in docs if item and item.get("metadata", {}).get("name") == "model-import-worker")
        worker = copy.deepcopy(worker)
        worker["spec"]["template"]["spec"]["containers"][0]["resources"]["limits"]["memory"] = "512Mi"
        with tempfile.TemporaryDirectory() as temporary:
            path = pathlib.Path(temporary) / "profile.yaml"
            path.write_text(yaml.safe_dump_all([config, worker]), encoding="utf-8")
            with self.assertRaises(AssertionError):
                validator.validate_manifest(path)

    def test_worker_rejects_archive_limit_mismatch(self) -> None:
        root = pathlib.Path(__file__).resolve().parents[1]
        manifest = root / "deploy/real-k8s-lab/inference-incluster-e2e.yaml"
        docs = list(yaml.safe_load_all(manifest.read_text(encoding="utf-8")))
        config = next(item for item in docs if item and item.get("metadata", {}).get("name") == "ani-inference-materialization")
        worker = copy.deepcopy(next(item for item in docs if item and item.get("metadata", {}).get("name") == "model-import-worker"))
        env = worker["spec"]["template"]["spec"]["containers"][0]["env"]
        env.append({"name": "MODEL_IMPORT_MAX_OUTPUT_BYTES", "value": "4294967296"})
        with tempfile.TemporaryDirectory() as temporary:
            path = pathlib.Path(temporary) / "profile.yaml"
            path.write_text(yaml.safe_dump_all([config, worker]), encoding="utf-8")
            with self.assertRaises(AssertionError):
                validator.validate_manifest(path)


if __name__ == "__main__":
    unittest.main()
