#!/usr/bin/env python3
"""Mutation tests for the shared C41 Envoy AI Gateway manifest."""

from __future__ import annotations

import unittest
from pathlib import Path
from tempfile import TemporaryDirectory

import validate_inference_envoy_ai_gateway_c41_manifest as manifest


class InferenceEnvoyAIGatewayC41ManifestTests(unittest.TestCase):
    def documents(self) -> list[dict]:
        return manifest.load_documents(manifest.DEFAULT_MANIFEST)

    def resource(self, documents: list[dict], kind: str, name: str) -> dict:
        return manifest.by_kind_name(documents, kind, name)

    def assert_rejected(self, documents: list[dict]) -> None:
        with self.assertRaises(SystemExit):
            manifest.validate(documents)

    def assert_raw_manifest_rejected(self, raw: str) -> None:
        with TemporaryDirectory() as directory:
            path = Path(directory) / "manifest.yaml"
            path.write_text(raw, encoding="utf-8")
            with self.assertRaises(SystemExit):
                manifest.load_documents(path)

    def test_repository_manifest_is_valid(self) -> None:
        manifest.validate(self.documents())

    def test_rejects_security_policy_targeting_route(self) -> None:
        documents = self.documents()
        policy = self.resource(documents, "SecurityPolicy", "ani-inference-ext-auth")
        policy["spec"]["targetRefs"][0]["kind"] = "HTTPRoute"
        self.assert_rejected(documents)

    def test_rejects_fail_open_true_or_missing(self) -> None:
        for mutation in ("true", "missing"):
            with self.subTest(mutation=mutation):
                documents = self.documents()
                ext_auth = self.resource(documents, "SecurityPolicy", "ani-inference-ext-auth")["spec"]["extAuth"]
                if mutation == "true":
                    ext_auth["failOpen"] = True
                else:
                    ext_auth.pop("failOpen")
                self.assert_rejected(documents)

    def test_rejects_recompute_route_false_or_missing(self) -> None:
        for mutation in ("false", "missing"):
            with self.subTest(mutation=mutation):
                documents = self.documents()
                ext_auth = self.resource(documents, "SecurityPolicy", "ani-inference-ext-auth")["spec"]["extAuth"]
                if mutation == "false":
                    ext_auth["recomputeRoute"] = False
                else:
                    ext_auth.pop("recomputeRoute")
                self.assert_rejected(documents)

    def test_rejects_static_context_extensions_or_body_forwarding(self) -> None:
        for key, value in (
            ("contextExtensions", [{"name": "ani.inference_service_id", "value": "static"}]),
            ("bodyToExtAuth", {"maxRequestBytes": 4096}),
        ):
            with self.subTest(key=key):
                documents = self.documents()
                self.resource(documents, "SecurityPolicy", "ani-inference-ext-auth")["spec"]["extAuth"][key] = value
                self.assert_rejected(documents)

    def test_rejects_missing_model_header_or_extra_forwarded_header(self) -> None:
        for mutation in ("missing_model", "extra"):
            with self.subTest(mutation=mutation):
                documents = self.documents()
                headers = self.resource(documents, "SecurityPolicy", "ani-inference-ext-auth")["spec"]["extAuth"]["headersToExtAuth"]
                if mutation == "missing_model":
                    headers.remove("x-ai-eg-model")
                else:
                    headers.append("x-ani-tenant-id")
                self.assert_rejected(documents)

    def test_rejects_missing_inference_service_adapter_configuration(self) -> None:
        documents = self.documents()
        container = self.resource(documents, "Deployment", "envoy-authz-adapter")["spec"]["template"]["spec"]["containers"][0]
        container["env"] = [item for item in container["env"] if item["name"] != "INFERENCE_SERVICE_GRPC_ADDR"]
        self.assert_rejected(documents)

    def test_rejects_missing_inference_service_egress(self) -> None:
        documents = self.documents()
        policy = self.resource(documents, "NetworkPolicy", "envoy-authz-adapter")
        policy["spec"]["egress"] = [
            rule for rule in policy["spec"]["egress"] if rule["ports"][0]["port"] != 9104
        ]
        self.assert_rejected(documents)

    def test_rejects_non_direct_public_base_url(self) -> None:
        for source in ("configMapKeyRef", "secretKeyRef"):
            with self.subTest(source=source):
                documents = self.documents()
                container = self.resource(documents, "Deployment", "inference-gateway-publisher")["spec"]["template"]["spec"]["containers"][0]
                env = next(item for item in container["env"] if item["name"] == "INFERENCE_AI_GATEWAY_PUBLIC_BASE_URL")
                env.pop("value")
                env["valueFrom"] = {source: {"name": "gateway-config", "key": "public_base_url"}}
                self.assert_rejected(documents)

    def test_rejects_publisher_without_service_account_token(self) -> None:
        for target in ("account", "pod"):
            with self.subTest(target=target):
                documents = self.documents()
                if target == "account":
                    self.resource(documents, "ServiceAccount", "inference-gateway-publisher")["automountServiceAccountToken"] = False
                else:
                    self.resource(documents, "Deployment", "inference-gateway-publisher")["spec"]["template"]["spec"]["automountServiceAccountToken"] = False
                self.assert_rejected(documents)

    def test_rejects_publisher_secret_or_workload_rbac(self) -> None:
        for resource in ("secrets", "pods", "deployments", "services", "namespaces", "customresourcedefinitions"):
            with self.subTest(resource=resource):
                documents = self.documents()
                role = self.resource(documents, "Role", "inference-gateway-publisher")
                role["rules"].append({"apiGroups": [""], "resources": [resource], "verbs": ["get"]})
                self.assert_rejected(documents)

    def test_rejects_publisher_status_update_permission(self) -> None:
        documents = self.documents()
        role = self.resource(documents, "Role", "inference-gateway-publisher")
        role["rules"][0]["resources"].append("backends/status")
        role["rules"][0]["verbs"].append("update")
        self.assert_rejected(documents)

    def test_rejects_cluster_scoped_publisher_rbac(self) -> None:
        documents = self.documents()
        role = self.resource(documents, "Role", "inference-gateway-publisher")
        role["kind"] = "ClusterRole"
        role["metadata"].pop("namespace")
        self.assert_rejected(documents)

    def test_rejects_static_per_service_gateway_resources(self) -> None:
        for api_version, kind in (
            ("gateway.envoyproxy.io/v1alpha1", "Backend"),
            ("aigateway.envoyproxy.io/v1beta1", "AIServiceBackend"),
            ("aigateway.envoyproxy.io/v1beta1", "AIGatewayRoute"),
        ):
            with self.subTest(kind=kind):
                documents = self.documents()
                documents.append(
                    {
                        "apiVersion": api_version,
                        "kind": kind,
                        "metadata": {"name": "must-be-dynamic", "namespace": "ani-aigw"},
                        "spec": {},
                    }
                )
                self.assert_rejected(documents)

    def test_rejects_redefinition_of_inference_service(self) -> None:
        documents = self.documents()
        documents.append(
            {
                "apiVersion": "apps/v1",
                "kind": "Deployment",
                "metadata": {"name": "inference-service", "namespace": "ani-system"},
                "spec": {},
            }
        )
        self.assert_rejected(documents)

    def test_rejects_root_or_privilege_escalation(self) -> None:
        for name in ("envoy-authz-adapter", "inference-gateway-publisher"):
            for mutation in ("root", "escalation", "writable", "capability"):
                with self.subTest(name=name, mutation=mutation):
                    documents = self.documents()
                    deployment = self.resource(documents, "Deployment", name)
                    container = deployment["spec"]["template"]["spec"]["containers"][0]
                    if mutation == "root":
                        container["securityContext"]["runAsUser"] = 0
                    elif mutation == "escalation":
                        container["securityContext"]["allowPrivilegeEscalation"] = True
                    elif mutation == "writable":
                        container["securityContext"]["readOnlyRootFilesystem"] = False
                    else:
                        container["securityContext"]["capabilities"]["drop"] = []
                    self.assert_rejected(documents)

    def test_rejects_plaintext_credential_literals(self) -> None:
        for literal in (
            "Bearer should-never-appear",
            "ani_live_should-never-appear",
            "postgres://ani:plaintext@postgres:5432/ani",
            "password=plaintext",
            "AK=plaintext",
            "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJ1c2VyIn0.signature",
        ):
            with self.subTest(literal=literal.split("=", 1)[0]):
                documents = self.documents()
                deployment = self.resource(documents, "Deployment", "inference-gateway-publisher")
                deployment["metadata"]["annotations"] = {"unsafe.example/value": literal}
                with self.assertRaises(SystemExit):
                    manifest.validate_no_plaintext_credentials(documents)

    def test_rejects_plaintext_credential_in_raw_yaml_comment(self) -> None:
        raw = manifest.DEFAULT_MANIFEST.read_text(encoding="utf-8")
        self.assert_raw_manifest_rejected(raw + "\n# Bearer hidden-review-repro\n")

    def test_rejects_plaintext_credential_in_discarded_yaml_anchor_name(self) -> None:
        raw = manifest.DEFAULT_MANIFEST.read_text(encoding="utf-8")
        anchored = raw.replace("metadata:\n", "metadata: &ani_live_hidden_anchor\n", 1)
        self.assertNotEqual(anchored, raw)
        self.assert_raw_manifest_rejected(anchored)

    def test_rejects_adapter_token_mount(self) -> None:
        documents = self.documents()
        deployment = self.resource(documents, "Deployment", "envoy-authz-adapter")
        deployment["spec"]["template"]["spec"]["automountServiceAccountToken"] = True
        self.assert_rejected(documents)

    def test_rejects_publisher_egress_widening(self) -> None:
        documents = self.documents()
        policy = self.resource(documents, "NetworkPolicy", "inference-gateway-publisher")
        policy["spec"]["egress"].append(
            {"to": [{"ipBlock": {"cidr": "0.0.0.0/0"}}], "ports": [{"protocol": "TCP", "port": 80}]}
        )
        self.assert_rejected(documents)

    def test_rejects_wrong_shared_rate_limit(self) -> None:
        documents = self.documents()
        policy = self.resource(documents, "BackendTrafficPolicy", "ani-inference-ratelimit")
        policy["spec"]["rateLimit"]["global"]["rules"][0]["limit"]["requests"] = 601
        self.assert_rejected(documents)

    def test_rejects_wrong_images_or_publisher_database_reference(self) -> None:
        for mutation in ("adapter_image", "publisher_image", "database_secret"):
            with self.subTest(mutation=mutation):
                documents = self.documents()
                if mutation == "adapter_image":
                    container = self.resource(documents, "Deployment", "envoy-authz-adapter")["spec"]["template"]["spec"]["containers"][0]
                    container["image"] = "docker.changqingyun.cn/ani/envoy-authz-adapter:latest"
                else:
                    container = self.resource(documents, "Deployment", "inference-gateway-publisher")["spec"]["template"]["spec"]["containers"][0]
                    if mutation == "publisher_image":
                        container["image"] = "docker.changqingyun.cn/ani/inference-gateway-publisher:latest"
                    else:
                        container["env"][0]["valueFrom"]["secretKeyRef"]["name"] = "other"
                self.assert_rejected(documents)

    def test_rejects_non_object_nested_shape(self) -> None:
        documents = self.documents()
        self.resource(documents, "Deployment", "inference-gateway-publisher")["spec"]["template"] = []
        self.assert_rejected(documents)

    def test_rejects_non_object_document(self) -> None:
        documents = self.documents()
        documents.append([])
        self.assert_rejected(documents)


if __name__ == "__main__":
    unittest.main()
