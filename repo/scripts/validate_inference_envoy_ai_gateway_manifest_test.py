#!/usr/bin/env python3
"""Regression tests for the static C40 Envoy AI Gateway manifest."""

from __future__ import annotations

import copy
import unittest

import validate_inference_envoy_ai_gateway_manifest as manifest


class InferenceEnvoyAIGatewayManifestTests(unittest.TestCase):
    def documents(self) -> list[dict]:
        return manifest.load_documents(manifest.DEFAULT_MANIFEST)

    def assert_rejected(self, documents: list[dict]) -> None:
        with self.assertRaises(SystemExit):
            manifest.validate(documents)

    def test_repo_manifest_is_valid(self) -> None:
        manifest.validate(self.documents())

    def test_repo_manifest_targets_current_owner_chat_and_embedding_inference(self) -> None:
        self.assertEqual(manifest.TENANT_ID, "00000000-0000-0000-0000-000000000001")
        self.assertEqual(manifest.INFERENCE_SERVICE_ID, "182df9a4-4a6a-4eed-9d50-51a458a15f6a")
        self.assertEqual(
            manifest.MODEL_BINDINGS,
            {
                "chat": {
                    "model": "ani-c40-chat",
                    "backend": "ani-c40-chat-vllm",
                    "service": "pw-182df9a4-4a6a-4eed-9d50-51a458a15f6a",
                },
                "embed": {
                    "model": "ani-c40-embed",
                    "backend": "ani-c40-embed-vllm",
                    "service": "pw-6ae6f951-415d-454f-8459-cd38d32dc58f",
                },
            },
        )

    def test_rejects_wrong_crd_api_version(self) -> None:
        documents = self.documents()
        manifest.by_kind_name(documents, "AIGatewayRoute", "ani-c40")["apiVersion"] = (
            "aigateway.envoyproxy.io/v1alpha1"
        )
        self.assert_rejected(documents)

    def test_rejects_api_key_auth_and_secret_credentials(self) -> None:
        for mutation in ("api_key_auth", "secret_ref"):
            with self.subTest(mutation=mutation):
                documents = self.documents()
                policy = manifest.by_kind_name(documents, "SecurityPolicy", "ani-c40-ext-auth")
                if mutation == "api_key_auth":
                    policy["spec"]["apiKeyAuth"] = {}
                else:
                    policy["spec"]["extAuth"]["grpc"]["backendRefs"][0]["credentialRef"] = {
                        "kind": "Secret",
                        "name": "must-not-exist",
                    }
                self.assert_rejected(documents)

    def test_rejects_fail_open_or_wrong_error_status(self) -> None:
        for key, value in (("failOpen", True), ("statusOnError", 500)):
            with self.subTest(key=key):
                documents = self.documents()
                policy = manifest.by_kind_name(documents, "SecurityPolicy", "ani-c40-ext-auth")
                policy["spec"]["extAuth"][key] = value
                self.assert_rejected(documents)

    def test_rejects_non_route_specific_policy_target(self) -> None:
        documents = self.documents()
        policy = manifest.by_kind_name(documents, "SecurityPolicy", "ani-c40-ext-auth")
        policy["spec"]["targetRefs"][0]["kind"] = "Gateway"
        self.assert_rejected(documents)

    def test_rejects_public_health_route_or_path_match(self) -> None:
        documents = self.documents()
        documents.append(
            {
                "apiVersion": "gateway.networking.k8s.io/v1",
                "kind": "HTTPRoute",
                "metadata": {"name": "public-health", "namespace": manifest.NAMESPACE},
                "spec": {"parentRefs": [{"name": "ani-aigw"}], "rules": []},
            }
        )
        self.assert_rejected(documents)

        documents = self.documents()
        route = manifest.by_kind_name(documents, "AIGatewayRoute", "ani-c40")
        route["spec"]["rules"][0]["matches"][0]["path"] = {
            "type": "PathPrefix",
            "value": "/healthz",
        }
        self.assert_rejected(documents)

    def test_rejects_gateway_wide_security_policy(self) -> None:
        documents = self.documents()
        policy = manifest.by_kind_name(documents, "SecurityPolicy", "ani-c40-ext-auth")
        policy["spec"]["targetRefs"] = [
            {"group": "gateway.networking.k8s.io", "kind": "Gateway", "name": "ani-aigw"}
        ]
        self.assert_rejected(documents)

    def test_rejects_incomplete_authz_policy_boundary(self) -> None:
        for mutation in ("missing_authorization_header", "wrong_adapter_port"):
            with self.subTest(mutation=mutation):
                documents = self.documents()
                ext_auth = manifest.by_kind_name(documents, "SecurityPolicy", "ani-c40-ext-auth")["spec"]["extAuth"]
                if mutation == "missing_authorization_header":
                    ext_auth["headersToExtAuth"] = []
                else:
                    ext_auth["grpc"]["backendRefs"][0]["port"] = 9001
                self.assert_rejected(documents)

    def test_rejects_missing_or_wrong_trusted_context(self) -> None:
        for mutation in ("missing", "wrong_tenant"):
            with self.subTest(mutation=mutation):
                documents = self.documents()
                extensions = manifest.by_kind_name(
                    documents, "SecurityPolicy", "ani-c40-ext-auth"
                )["spec"]["extAuth"]["contextExtensions"]
                if mutation == "missing":
                    extensions.pop()
                else:
                    extensions[0]["value"] = "22222222-2222-2222-2222-222222222222"
                self.assert_rejected(documents)

    def test_rejects_backend_that_can_receive_authorization(self) -> None:
        for header in ("Authorization", "x-api-key", "x-ani-tenant-id", "x-ani-user-id"):
            with self.subTest(header=header):
                documents = self.documents()
                for rule in manifest.by_kind_name(documents, "AIGatewayRoute", "ani-c40")["spec"]["rules"]:
                    rule["backendRefs"][0]["headerMutation"]["remove"].remove(header)
                self.assert_rejected(documents)

    def test_rejects_unretained_vllm_dns_or_port(self) -> None:
        for backend_name in ("ani-c40-chat-vllm", "ani-c40-embed-vllm"):
            for key, value in (("hostname", "wrong.ani-system.svc.cluster.local"), ("port", 8001)):
                with self.subTest(backend_name=backend_name, key=key):
                    documents = self.documents()
                    fqdn = manifest.by_kind_name(documents, "Backend", backend_name)["spec"]["endpoints"][0]["fqdn"]
                    fqdn[key] = value
                    self.assert_rejected(documents)

    def test_rejects_missing_embedding_route_or_backend(self) -> None:
        for mutation in ("missing_embed_rule", "wrong_embed_model", "wrong_embed_backend"):
            with self.subTest(mutation=mutation):
                documents = self.documents()
                route = manifest.by_kind_name(documents, "AIGatewayRoute", "ani-c40")
                if mutation == "missing_embed_rule":
                    route["spec"]["rules"].pop()
                elif mutation == "wrong_embed_model":
                    route["spec"]["rules"][1]["matches"][0]["headers"][0]["value"] = "ani-c40-chat"
                else:
                    route["spec"]["rules"][1]["backendRefs"][0]["name"] = "ani-c40-chat-vllm"
                self.assert_rejected(documents)

    def test_rejects_adapter_secret_or_data_store_environment(self) -> None:
        forbidden_names = ("DATABASE_URL", "REDIS_URL", "NATS_URL", "AK", "JWT_SECRET", "DB_PASSWORD")
        for name in forbidden_names:
            with self.subTest(name=name):
                documents = self.documents()
                container = manifest.by_kind_name(documents, "Deployment", "envoy-authz-adapter")["spec"][
                    "template"
                ]["spec"]["containers"][0]
                container["env"].append({"name": name, "value": "must-not-be-present"})
                self.assert_rejected(documents)

    def test_rejects_adapter_privilege_or_token_mount(self) -> None:
        for mutation in ("service_account_token", "not_read_only", "privilege_escalation", "missing_drop_all"):
            with self.subTest(mutation=mutation):
                documents = self.documents()
                deployment = manifest.by_kind_name(documents, "Deployment", "envoy-authz-adapter")
                if mutation == "service_account_token":
                    deployment["spec"]["template"]["spec"]["automountServiceAccountToken"] = True
                else:
                    security = deployment["spec"]["template"]["spec"]["containers"][0]["securityContext"]
                    if mutation == "not_read_only":
                        security["readOnlyRootFilesystem"] = False
                    elif mutation == "privilege_escalation":
                        security["allowPrivilegeEscalation"] = True
                    else:
                        security["capabilities"]["drop"] = []
                self.assert_rejected(documents)

    def test_rejects_network_policy_wider_than_envoy_dns_and_auth(self) -> None:
        for mutation in ("open_ingress", "missing_gateway_selector", "open_egress", "wrong_auth_port"):
            with self.subTest(mutation=mutation):
                documents = self.documents()
                policy = manifest.by_kind_name(documents, "NetworkPolicy", "envoy-authz-adapter")
                if mutation == "open_ingress":
                    policy["spec"]["ingress"][0]["from"].append({"podSelector": {}})
                elif mutation == "missing_gateway_selector":
                    del policy["spec"]["ingress"][0]["from"][0]["podSelector"]["matchLabels"][
                        "gateway.envoyproxy.io/owning-gateway-name"
                    ]
                elif mutation == "open_egress":
                    policy["spec"]["egress"].append(
                        {"to": [{"ipBlock": {"cidr": "0.0.0.0/0"}}], "ports": [{"protocol": "TCP", "port": 443}]}
                    )
                else:
                    policy["spec"]["egress"][2]["ports"][0]["port"] = 9102
                self.assert_rejected(documents)

    def test_mutations_do_not_change_the_loaded_manifest(self) -> None:
        documents = self.documents()
        mutated = copy.deepcopy(documents)
        manifest.by_kind_name(mutated, "Backend", "ani-c40-embed-vllm")["spec"]["endpoints"][0]["fqdn"]["port"] = 1
        self.assert_rejected(mutated)
        manifest.validate(documents)


if __name__ == "__main__":
    unittest.main()
