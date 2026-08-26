#!/usr/bin/env python3
"""Regression tests for the C40 Envoy AI Gateway live-gate contract."""

from __future__ import annotations

import copy
import json
import os
import tempfile
import unittest
from pathlib import Path
from unittest import mock

import validate_inference_envoy_ai_gateway_live_gate as gate
import run_inference_envoy_ai_gateway_live as runner


def evidence(**overrides: object) -> dict:
    payload = {
        "profile": gate.PROFILE,
        "status": "passed",
        "envoy_invocation_ready": True,
        "dynamic_publication_ready": False,
        "runtime_ready": False,
        "authorization_not_forwarded": True,
        "clusterip_only_backend": True,
        "checks": [{"id": check_id, "status": "passed"} for check_id in sorted(gate.REQUIRED_CHECKS)],
    }
    payload.update(overrides)
    return payload


class InferenceEnvoyAIGatewayLiveGateTests(unittest.TestCase):
    def write_evidence(self, payload: dict) -> Path:
        directory = Path(tempfile.mkdtemp(prefix="ani-c40-evidence-"))
        self.addCleanup(lambda: __import__("shutil").rmtree(directory, ignore_errors=True))
        target = directory / "evidence.json"
        target.write_text(json.dumps(payload), encoding="utf-8")
        return target

    def test_repo_contract_is_valid_and_is_not_a_live_claim(self) -> None:
        document = gate.load_gate(gate.DEFAULT_GATE)
        gate.validate_contract(document)
        self.assertEqual(document["status"], "contract")
        self.assertEqual(document["readiness_claims"], gate.READINESS_CLAIMS)

    def test_gate_validator_and_runner_share_the_20260825_evidence_target(self) -> None:
        document = gate.load_gate(gate.DEFAULT_GATE)
        expected = gate.ROOT / "development-records/live-evidence/inference-envoy-ai-gateway-live-20260825.json"
        self.assertEqual(gate.DEFAULT_EVIDENCE, expected)
        self.assertEqual(runner.EVIDENCE, expected)
        self.assertEqual(gate.ROOT / document["evidence"], expected)

    def test_required_checks_include_confirmed_c40_boundaries(self) -> None:
        self.assertTrue({
            "credential-location-boundary",
            "public-path-boundary",
            "valid-ak-embedding",
            "workload-probes-ready",
        } <= gate.REQUIRED_CHECKS)

    def test_rejects_live_status_without_evidence(self) -> None:
        document = gate.load_gate(gate.DEFAULT_GATE)
        document["status"] = "live"
        document["evidence"] = "development-records/live-evidence/missing-c40.json"
        with self.assertRaises(SystemExit):
            gate.validate_contract(document)

    def test_accepts_complete_redacted_live_evidence(self) -> None:
        document = gate.load_gate(gate.DEFAULT_GATE)
        document["status"] = "live"
        target = self.write_evidence(evidence())
        document["evidence"] = str(target)
        gate.validate_contract(document)

    def test_rejects_readiness_claims_other_than_the_c40_boundary(self) -> None:
        target = self.write_evidence(evidence(runtime_ready=True))
        with self.assertRaises(SystemExit):
            gate.validate_evidence(target)

    def test_rejects_evidence_with_secret_bearing_content(self) -> None:
        forbidden = (
            '"Authorization": "Bearer value"',
            "authorization-header-value",
            "Bearer value",
            "ani_dev_example",
            "ANI_PROD_example",
            "password=not-safe",
            "postgresql://user:password@example/database",
            '{"kind":"Secret","data":{"token":"not-safe"}}',
        )
        for value in forbidden:
            with self.subTest(value=value):
                target = self.write_evidence(evidence(note=value))
                with self.assertRaises(SystemExit):
                    gate.validate_evidence(target)

    def test_runner_lifecycle_plan_keeps_the_required_order(self) -> None:
        self.assertEqual(
            runner.lifecycle_plan(),
            [
                "apply-and-wait",
                "owner-nonstream",
                "owner-embedding",
                "owner-sse",
                "missing-and-malformed",
                "credential-location-boundary",
                "public-path-boundary",
                "workload-probes-ready",
                "expired-key",
                "revoked-key",
                "rpm-limit",
                "foreign-tenant",
                "adapter-fail-closed",
                "auth-service-unreachable",
                "credential-forwarding",
                "control-plane-regressions",
                "redaction-scan",
            ],
        )

    def test_runner_lifecycle_includes_confirmed_c40_boundaries(self) -> None:
        plan = runner.lifecycle_plan()
        self.assertLess(plan.index("missing-and-malformed"), plan.index("credential-location-boundary"))
        self.assertLess(plan.index("credential-location-boundary"), plan.index("public-path-boundary"))
        self.assertLess(plan.index("public-path-boundary"), plan.index("expired-key"))

    def test_runner_redacts_and_rejects_sensitive_values(self) -> None:
        rendered = runner.redact_text("Authorization: Bearer ani_prod_sensitive password=unsafe")
        self.assertNotIn("ani_prod_sensitive", rendered)
        self.assertNotIn("Bearer ani_prod", rendered)
        with self.assertRaises(SystemExit):
            runner.assert_redacted_evidence({"note": "ani_dev_sensitive"})

    def test_runner_requires_effective_http_route_credential_removals(self) -> None:
        route = {
            "spec": {
                "rules": [
                    {
                        "filters": [
                            {
                                "type": "RequestHeaderModifier",
                                "requestHeaderModifier": {
                                    "remove": [
                                        "Authorization",
                                        "x-api-key",
                                        "x-ani-tenant-id",
                                        "x-ani-user-id",
                                    ]
                                },
                            }
                        ]
                    }
                ]
            }
        }
        self.assertTrue(runner.has_required_header_removals(route))
        route["spec"]["rules"][0]["filters"][0]["requestHeaderModifier"]["remove"].pop()
        self.assertFalse(runner.has_required_header_removals(route))

    def test_control_plane_url_requires_api_v1_before_any_mutation(self) -> None:
        with self.assertRaises(SystemExit):
            runner.validate_control_plane_url("https://control.example")
        self.assertEqual(
            runner.validate_control_plane_url("https://control.example/api/v1/"),
            "https://control.example/api/v1",
        )

    def test_control_request_builds_api_v1_request_and_discards_error_body(self) -> None:
        class Response:
            status = 201

            def read(self) -> bytes:
                return b'{"key_id":"id","key_value":"secret"}'

            def __enter__(self):
                return self

            def __exit__(self, *args: object) -> None:
                return None

        with mock.patch.dict(os.environ, {"ANI_C40_CONTROL_PLANE_URL": "https://control.example/api/v1"}, clear=False):
            with mock.patch.object(runner.urllib.request, "urlopen", return_value=Response()) as urlopen:
                status, body = runner.control_request("POST", "/auth/api-keys", "access-token", {"name": "c40"})
        self.assertEqual(status, 201)
        self.assertEqual(body["key_id"], "id")
        request = urlopen.call_args.args[0]
        self.assertEqual(request.full_url, "https://control.example/api/v1/auth/api-keys")

    def test_control_request_rejects_malformed_success_json_without_echoing_it(self) -> None:
        class Response:
            status = 200

            def read(self) -> bytes:
                return b"not-json-ani_prod_sensitive"

            def __enter__(self):
                return self

            def __exit__(self, *args: object) -> None:
                return None

        with mock.patch.dict(os.environ, {"ANI_C40_CONTROL_PLANE_URL": "https://control.example/api/v1"}, clear=False):
            with mock.patch.object(runner.urllib.request, "urlopen", return_value=Response()):
                with self.assertRaises(SystemExit) as raised:
                    runner.control_request("GET", "/auth/api-keys", "access-token")
        self.assertNotIn("ani_prod_sensitive", str(raised.exception))

    def test_gateway_request_never_promotes_alternate_credentials(self) -> None:
        class Unauthorized:
            code = 401

            def read(self) -> bytes:
                return b"discarded"

            def close(self) -> None:
                return None

        with mock.patch.dict(os.environ, {"ANI_C40_GATEWAY_URL": "https://invoke.example"}, clear=False):
            with mock.patch.object(
                runner.urllib.request,
                "urlopen",
                side_effect=runner.urllib.error.HTTPError(
                    "https://invoke.example/v1/chat/completions",
                    401,
                    "unauthorized",
                    {},
                    Unauthorized(),
                ),
            ) as urlopen:
                status, _ = runner.gateway_request(
                    "/v1/chat/completions",
                    method="POST",
                    body={"model": runner.PUBLIC_MODEL_ID},
                    headers={"x-api-key": "ani_dev_not_a_real_key"},
                )
        self.assertEqual(status, 401)
        request = urlopen.call_args.args[0]
        self.assertNotIn("Authorization", request.headers)

    def test_credential_location_boundary_uses_owner_key_only_in_alternate_headers(self) -> None:
        owner_key = "in-memory-owner-key"
        with mock.patch.object(
            runner,
            "gateway_request",
            side_effect=[(401, {}), (401, {}), (401, {})],
        ) as gateway_request:
            runner.verify_credential_location_boundary(owner_key)
        first_headers = gateway_request.call_args_list[0].kwargs["headers"]
        second_headers = gateway_request.call_args_list[1].kwargs["headers"]
        query = gateway_request.call_args_list[2].kwargs["query"]
        self.assertEqual(first_headers["x-api-key"], owner_key)
        self.assertEqual(second_headers["Cookie"], "api_key=" + owner_key)
        self.assertEqual(query, {"api_key": "not-a-credential"})
        self.assertNotIn(owner_key, repr(query))

    def test_public_path_boundary_posts_to_only_the_confirmed_unregistered_paths(self) -> None:
        markers = ["marker-health", "marker-ready", "marker-unregistered"]
        with mock.patch.object(runner, "temporary_name", side_effect=markers), mock.patch.object(
            runner,
            "gateway_request",
            side_effect=[(404, {}), (404, {}), (404, {})],
        ) as gateway_request:
            self.assertEqual(runner.verify_public_path_boundary(), markers)
        self.assertEqual(
            [call.args[0] for call in gateway_request.call_args_list],
            ["/healthz", "/readyz", "/v1/not-registered"],
        )
        self.assertTrue(all(call.kwargs["method"] == "POST" for call in gateway_request.call_args_list))
        self.assertEqual(
            [call.kwargs["body"] for call in gateway_request.call_args_list],
            [{"marker": marker} for marker in markers],
        )

    def test_workload_probe_predicate_requires_both_probes_on_every_named_container(self) -> None:
        deployment = {"spec": {"template": {"spec": {"containers": [
            {"name": "envoy", "readinessProbe": {"httpGet": {}}, "livenessProbe": {"httpGet": {}}},
            {"name": "shutdown-manager", "readinessProbe": {"httpGet": {}}, "livenessProbe": {"httpGet": {}}},
        ]}}}}
        self.assertTrue(runner.has_container_probes(deployment, {"envoy", "shutdown-manager"}))
        del deployment["spec"]["template"]["spec"]["containers"][0]["livenessProbe"]
        self.assertFalse(runner.has_container_probes(deployment, {"envoy", "shutdown-manager"}))

    def test_workload_probe_check_reads_exact_deployments_without_mutating_them(self) -> None:
        adapter = {"spec": {"template": {"spec": {"containers": [
            {"name": "envoy-authz-adapter", "readinessProbe": {"httpGet": {}}, "livenessProbe": {"httpGet": {}}},
        ]}}}}
        envoy = {"spec": {"template": {"spec": {"containers": [
            {"name": "envoy", "readinessProbe": {"httpGet": {}}, "livenessProbe": {"httpGet": {}}},
            {"name": "shutdown-manager", "readinessProbe": {"httpGet": {}}, "livenessProbe": {"httpGet": {}}},
        ]}}}}
        original_adapter = copy.deepcopy(adapter)
        original_envoy = copy.deepcopy(envoy)
        with mock.patch.object(
            runner,
            "kubectl_json",
            side_effect=[adapter, {"items": [envoy]}],
        ) as kubectl_json:
            runner.verify_workload_probes()
        self.assertEqual(adapter, original_adapter)
        self.assertEqual(envoy, original_envoy)
        self.assertEqual(
            kubectl_json.call_args_list,
            [
                mock.call(["-n", "ani-aigw", "get", "deployment", "envoy-authz-adapter"]),
                mock.call([
                    "-n", "envoy-gateway-system", "get", "deployments",
                    "-l", "gateway.envoyproxy.io/owning-gateway-name=ani-aigw",
                ]),
            ],
        )

    def test_snapshots_and_restores_exact_adapter_state_even_when_env_restore_fails(self) -> None:
        state = runner.adapter_state_from_deployment(
            {"spec": {"replicas": 3, "template": {"spec": {"containers": [{"name": "envoy-authz-adapter", "env": []}]}}}}
        )
        self.assertEqual(state.replicas, 3)
        self.assertIsNone(state.auth_service_grpc_addr)
        calls: list[list[str]] = []

        def fake_kubectl(args: list[str], timeout: int = 120) -> str:
            calls.append(args)
            if args[2:4] == ["set", "env"]:
                raise SystemExit("simulated env failure")
            return ""

        with mock.patch.object(runner, "kubectl", side_effect=fake_kubectl), mock.patch.object(
            runner, "wait_adapter_restored", return_value=[]
        ):
            failures = runner.restore_adapter_state(state)
        self.assertEqual(failures, ["env"])
        self.assertIn(["-n", runner.ADAPTER_NAMESPACE, "set", "env", "deployment/envoy-authz-adapter", "AUTH_SERVICE_GRPC_ADDR-"], calls)
        self.assertIn(["-n", runner.ADAPTER_NAMESPACE, "scale", "deployment/envoy-authz-adapter", "--replicas=3"], calls)

    def test_applies_and_waits_before_snapshotting_manifest_owned_adapter_baseline(self) -> None:
        calls: list[str] = []
        state = runner.AdapterState(1, runner.AUTH_SERVICE_ADDR)
        with mock.patch.object(runner, "kubectl", side_effect=lambda *args, **kwargs: calls.append("apply")), mock.patch.object(
            runner, "snapshot_adapter_state", side_effect=lambda: calls.append("snapshot") or state
        ), mock.patch.object(runner, "wait_adapter_restored", side_effect=lambda *args, **kwargs: calls.append("wait-generation") or []):
            self.assertEqual(runner.apply_and_snapshot_adapter(), state)
        self.assertEqual(calls, ["apply", "snapshot", "wait-generation", "snapshot"])

    def test_apply_baseline_rejects_stale_observed_generation(self) -> None:
        state = runner.AdapterState(1, runner.AUTH_SERVICE_ADDR)
        with mock.patch.object(runner, "kubectl"), mock.patch.object(runner, "snapshot_adapter_state", return_value=state), mock.patch.object(
            runner, "wait_adapter_restored", return_value=["readiness"]
        ):
            with self.assertRaises(SystemExit):
                runner.apply_and_snapshot_adapter()

    def test_waits_for_zero_ready_adapter_endpoints_before_probe(self) -> None:
        responses = [
            {"items": [{"endpoints": [{"conditions": {"ready": True}}]}]},
            {"subsets": [{"addresses": [{"ip": "10.0.0.1"}]}]},
            {"items": [{"endpoints": [{"conditions": {"ready": False}}]}]},
            {"subsets": []},
        ]
        with mock.patch.object(runner, "kubectl_json", side_effect=responses) as kubectl_json, mock.patch.object(runner.time, "sleep"):
            runner.wait_adapter_endpoints_zero(timeout=2)
        self.assertEqual(kubectl_json.call_count, 4)

    def test_resolves_only_ready_endpoint_slice_pods_and_vllm_containers(self) -> None:
        endpoint_slices = {
            "items": [
                {"ports": [{"port": 8000}], "endpoints": [
                    {"conditions": {"ready": True}, "targetRef": {"kind": "Pod", "name": "vllm-a"}},
                    {"conditions": {"ready": False}, "targetRef": {"kind": "Pod", "name": "ignored"}},
                ]}
            ]
        }
        pods = {
            "vllm-a": {"metadata": {"name": "vllm-a"}, "spec": {"containers": [{"name": "sidecar"}, {"name": "vllm"}]}},
            "ignored": {"metadata": {"name": "ignored"}, "spec": {"containers": [{"name": "vllm"}]}},
        }
        self.assertEqual(runner.resolve_vllm_log_targets(endpoint_slices, pods), [("vllm-a", "vllm")])

    def test_sensitive_kubectl_failure_does_not_expose_stdout_or_stderr(self) -> None:
        result = mock.Mock(returncode=1, stdout="ani_prod_stdout", stderr="ani_prod_stderr")
        with mock.patch.object(runner.subprocess, "run", return_value=result):
            with self.assertRaises(SystemExit) as raised:
                runner.kubectl(["-n", runner.ADAPTER_NAMESPACE, "get", "secrets", "-o", "json"])
        self.assertNotIn("ani_prod_stdout", str(raised.exception))
        self.assertNotIn("ani_prod_stderr", str(raised.exception))

    def test_generated_route_must_bind_current_c40_model_backend_and_removals(self) -> None:
        route = {
            "metadata": {"name": "ani-c40", "generation": 7},
            "status": {"parents": [{"controllerName": "gateway.envoyproxy.io/gatewayclass-controller", "parentRef": {"group": "gateway.networking.k8s.io", "kind": "Gateway", "namespace": "ani-aigw", "name": "ani-aigw"}, "conditions": [{"type": "Accepted", "status": "True", "observedGeneration": 7}, {"type": "ResolvedRefs", "status": "True", "observedGeneration": 7}]}]},
            "spec": {"rules": [
                {"matches": [{"headers": [{"name": "x-ai-eg-model", "type": "Exact", "value": runner.CHAT_MODEL_ID}]}], "backendRefs": [{"name": "ani-c40-chat-vllm"}], "filters": [{"type": "RequestHeaderModifier", "requestHeaderModifier": {"remove": ["Authorization", "x-api-key", "x-ani-tenant-id", "x-ani-user-id"]}}]},
                {"matches": [{"headers": [{"name": "x-ai-eg-model", "type": "Exact", "value": runner.EMBED_MODEL_ID}]}], "backendRefs": [{"name": "ani-c40-embed-vllm"}], "filters": [{"type": "RequestHeaderModifier", "requestHeaderModifier": {"remove": ["Authorization", "x-api-key", "x-ani-tenant-id", "x-ani-user-id"]}}]},
            ]},
        }
        self.assertTrue(runner.is_current_c40_http_route(route))
        route["status"]["parents"][0]["conditions"][1]["observedGeneration"] = 6
        self.assertFalse(runner.is_current_c40_http_route(route))

    def test_route_rejects_non_exact_model_match_or_non_modifier_filter(self) -> None:
        route = {
            "metadata": {"name": "ani-c40", "generation": 2},
            "status": {"parents": [{"controllerName": "controller", "parentRef": {"group": "gateway.networking.k8s.io", "kind": "Gateway", "name": "ani-aigw"}, "conditions": [{"type": "Accepted", "status": "True", "observedGeneration": 2}, {"type": "ResolvedRefs", "status": "True", "observedGeneration": 2}]}]},
            "spec": {"rules": [{"matches": [{"headers": [{"name": "x-ai-eg-model", "type": "RegularExpression", "value": runner.PUBLIC_MODEL_ID}]}], "backendRefs": [{"name": "ani-c40-chat-vllm"}], "filters": [{"type": "Other", "requestHeaderModifier": {"remove": ["Authorization", "x-api-key", "x-ani-tenant-id", "x-ani-user-id"]}}]}]},
        }
        self.assertFalse(runner.is_current_c40_http_route(route))

    def test_route_rejects_one_stale_parent_condition(self) -> None:
        route = {
            "metadata": {"name": "ani-c40", "generation": 3},
            "status": {"parents": [{"controllerName": "gateway.envoyproxy.io/gatewayclass-controller", "parentRef": {"group": "gateway.networking.k8s.io", "kind": "Gateway", "name": "ani-aigw"}, "conditions": [{"type": "Accepted", "status": "True", "observedGeneration": 3}, {"type": "ResolvedRefs", "status": "True", "observedGeneration": 2}]}]},
            "spec": {"rules": [{"matches": [{"headers": [{"name": "x-ai-eg-model", "type": "Exact", "value": runner.PUBLIC_MODEL_ID}]}], "backendRefs": [{"name": "ani-c40-chat-vllm"}], "filters": [{"type": "RequestHeaderModifier", "requestHeaderModifier": {"remove": ["Authorization", "x-api-key", "x-ani-tenant-id", "x-ani-user-id"]}}]}]},
        }
        self.assertFalse(runner.is_current_c40_http_route(route))

    def test_malformed_key_value_is_revoked_immediately_after_key_id_registration(self) -> None:
        cleanup: list[tuple[str, str]] = []
        with mock.patch.object(runner, "control_request", return_value=(201, {"key_id": "temporary-id", "key_value": ""})), mock.patch.object(
            runner, "revoke_api_key"
        ) as revoke:
            with self.assertRaises(SystemExit):
                runner.create_registered_api_key("owner-token", cleanup, name="c40", scopes=["scope:models:read"], rpm=1)
        revoke.assert_called_once_with("owner-token", "temporary-id")
        self.assertEqual(cleanup, [])

    def test_clusterip_contract_rejects_empty_external_or_wrong_port_services(self) -> None:
        service = {"spec": {"type": "ClusterIP", "clusterIP": "10.96.1.2", "ports": [{"protocol": "TCP", "port": 8000, "targetPort": 8000}]}}
        self.assertTrue(runner.is_retained_clusterip_service(service))
        service["spec"]["clusterIP"] = ""
        self.assertFalse(runner.is_retained_clusterip_service(service))
        service["spec"]["clusterIP"] = "10.96.1.2"
        service["spec"]["externalIPs"] = ["203.0.113.1"]
        self.assertFalse(runner.is_retained_clusterip_service(service))

    def test_backend_requires_exact_single_retained_fqdn_endpoint(self) -> None:
        backend = {
            "spec": {
                "type": "Endpoints",
                "endpoints": [{"fqdn": {"hostname": f"{runner.CHAT_VLLM_SERVICE}.{runner.VLLM_NAMESPACE}.svc.cluster.local", "port": 8000}}],
            }
        }
        self.assertTrue(runner.is_exact_c40_backend(backend, runner.CHAT_VLLM_SERVICE))
        backend["spec"]["endpoints"].append({"fqdn": {"hostname": "other.svc.cluster.local", "port": 8000}})
        self.assertFalse(runner.is_exact_c40_backend(backend, runner.CHAT_VLLM_SERVICE))

    def test_cleanup_attempts_every_key_and_reports_aggregate_failure(self) -> None:
        attempts: list[str] = []

        def revoke(access: str, key_id: str) -> None:
            attempts.append(key_id)
            if key_id == "first":
                raise SystemExit("failed")

        with mock.patch.object(runner, "revoke_api_key", side_effect=revoke):
            self.assertEqual(runner.cleanup_api_keys([("token", "first"), ("token", "second")]), ["first"])
        self.assertEqual(attempts, ["second", "first"])

    def test_log_and_secret_scans_use_full_temporary_keys_and_prefix_material(self) -> None:
        material = runner.temporary_key_search_material(["ani_prod_0123456789abcdef"])
        self.assertIn("ani_prod_0123456789abcdef", material)
        self.assertIn("ani_prod_0123456789abcdef"[:20], material)

    def test_atomic_evidence_write_validates_temp_before_replace_and_cleans_up(self) -> None:
        target = Path(tempfile.mkdtemp(prefix="ani-c40-atomic-")) / "evidence.json"
        self.addCleanup(lambda: __import__("shutil").rmtree(target.parent, ignore_errors=True))
        with mock.patch.object(runner, "validate_evidence") as validate:
            runner.write_evidence_atomically(target, evidence())
        self.assertTrue(target.exists())
        self.assertEqual(json.loads(target.read_text(encoding="utf-8"))["status"], "passed")
        self.assertEqual(validate.call_count, 1)
        self.assertFalse(list(target.parent.glob(".inference-envoy-ai-gateway-*.tmp")))

    def test_atomic_evidence_write_preserves_existing_target_when_temp_validation_fails(self) -> None:
        target = Path(tempfile.mkdtemp(prefix="ani-c40-atomic-fail-")) / "evidence.json"
        self.addCleanup(lambda: __import__("shutil").rmtree(target.parent, ignore_errors=True))
        target.write_text('{"previous":true}\n', encoding="utf-8")
        with mock.patch.object(runner, "validate_evidence", side_effect=SystemExit("invalid")):
            with self.assertRaises(SystemExit):
                runner.write_evidence_atomically(target, evidence())
        self.assertEqual(target.read_text(encoding="utf-8"), '{"previous":true}\n')
        self.assertFalse(list(target.parent.glob(".inference-envoy-ai-gateway-*.tmp")))


if __name__ == "__main__":
    unittest.main()
