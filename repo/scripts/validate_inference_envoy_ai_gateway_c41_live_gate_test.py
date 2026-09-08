#!/usr/bin/env python3
"""Safety tests for the C41 multi-tenant AI Gateway live-gate contract.

Every external boundary is mocked.  This test module must stay local-only.
"""

from __future__ import annotations

import json
import os
import runpy
import stat
import sys
import tempfile
import unittest
from contextlib import ExitStack
from pathlib import Path
from unittest import mock

import validate_inference_envoy_ai_gateway_c41_live_gate as gate
import run_inference_envoy_ai_gateway_c41_live as runner


class C41LiveGateSafetyTests(unittest.TestCase):
    @staticmethod
    def _jwt_for_tenant(tenant_id: str) -> str:
        import base64

        encode = lambda value: base64.urlsafe_b64encode(json.dumps(value).encode()).decode().rstrip("=")
        return encode({"alg": "none"}) + "." + encode({"tid": tenant_id}) + ".signature"

    def test_contract_is_local_only_and_declares_exact_environment(self) -> None:
        document = gate.load_gate(gate.DEFAULT_GATE)
        gate.validate_contract(document)
        self.assertEqual("contract", document["status"])
        self.assertEqual(gate.REQUIRED_ENV, document["required_env"])

    def test_importing_runner_has_no_network_or_kubernetes_side_effects(self) -> None:
        self.assertEqual(runner.lifecycle_plan()[0], "authorize-live-run")

    def test_external_url_validation_rejects_internal_hosts_and_requires_http(self) -> None:
        for value in (
            "https://inference-service.ani-system.svc.cluster.local",
            "https://gateway.ani-aigw.svc",
            "https://10.96.0.1",
            "ftp://public.example",
        ):
            with self.subTest(value=value), self.assertRaises(SystemExit):
                runner.validate_public_url(value, "test URL")
        self.assertEqual("https://public.example/base", runner.validate_public_url("https://public.example/base/", "test URL"))
        self.assertEqual("http://127.0.0.1:18080", runner.validate_public_url("http://127.0.0.1:18080", "test URL"))

    def test_control_plane_url_must_be_external_api_v1(self) -> None:
        with self.assertRaises(SystemExit):
            runner.validate_control_plane_url("https://control.ani-system.svc.cluster.local/api/v1")
        self.assertEqual(
            "https://control.example/api/v1",
            runner.validate_control_plane_url("https://control.example/api/v1/"),
        )

    def test_login_jwt_tenant_identity_is_a_uuid_and_tenants_must_differ(self) -> None:
        tenant = "11111111-1111-1111-1111-111111111111"
        self.assertEqual(tenant, runner.tenant_id_from_login_jwt(self._jwt_for_tenant(tenant)))
        with self.assertRaises(SystemExit):
            runner.tenant_id_from_login_jwt(self._jwt_for_tenant("not-a-uuid"))

    def test_api_key_cleanup_registration_precedes_one_time_value_validation(self) -> None:
        cleanup: list[tuple[str, str]] = []
        with mock.patch.object(runner, "control_request", return_value=(201, {"key_id": "ak-1"})), mock.patch.object(
            runner, "revoke_api_key", return_value=None
        ):
            with self.assertRaises(SystemExit):
                runner.create_registered_api_key("tenant-token", cleanup, name="c41", rpm=1)
        self.assertEqual([("tenant-token", "ak-1")], cleanup)

    def test_evidence_is_atomic_private_and_redacted(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            target = Path(directory) / "evidence.json"
            runner.write_evidence_atomically(target, {"profile": runner.PROFILE, "status": "passed", "checks": []})
            self.assertEqual(0o600, stat.S_IMODE(target.stat().st_mode))
            self.assertEqual("passed", json.loads(target.read_text(encoding="utf-8"))["status"])
            self.assertFalse(list(target.parent.glob(target.name + ".*")))
            with self.assertRaises(SystemExit):
                runner.write_evidence_atomically(target, {"prompt": "private prompt"})

    def test_evidence_never_accepts_credentials_or_model_content(self) -> None:
        for forbidden in (
            {"authorization": "Bearer secret"},
            {"api_key": "ani_dev_secret"},
            {"prompt": "do not persist"},
            {"completion": "do not persist"},
            {"vector": [0.1, 0.2]},
        ):
            with self.subTest(forbidden=forbidden), self.assertRaises(SystemExit):
                runner.assert_redacted_evidence(forbidden)
        with self.assertRaises(SystemExit):
            runner.assert_redacted_evidence({"check": "a prompt must never be written"})

    def test_kubectl_errors_discard_output_and_secret_data_is_forbidden(self) -> None:
        completed = mock.Mock(returncode=1, stdout="Bearer leaked", stderr="ani_dev_leaked")
        with mock.patch.object(runner.subprocess, "run", return_value=completed):
            with self.assertRaises(SystemExit) as raised:
                runner.kubectl(["get", "pods"])
        self.assertNotIn("leaked", str(raised.exception))
        for command in (
            ["get", "secret", "x", "-o", "json"],
            ["get", "secrets", "-o", "json"],
            ["get", "secret", "x", "-o", "jsonpath={.data}"],
        ):
            with self.subTest(command=command), self.assertRaises(SystemExit):
                runner.kubectl(command)

    def test_cleanup_targets_are_runner_owned_only_and_failures_fail_gate(self) -> None:
        state = runner.CleanupState()
        state.register_service("tenant-a-token", "service-a")
        state.register_policy("tenant-a-token", "service-a")
        state.register_api_key("tenant-a-token", "ak-a")
        self.assertEqual([("tenant-a-token", "service-a")], state.services)
        with mock.patch.object(runner, "delete_policy", side_effect=SystemExit("no")), mock.patch.object(
            runner, "delete_inference_service", return_value=None
        ), mock.patch.object(runner, "revoke_api_key", return_value=None):
            with self.assertRaises(SystemExit):
                runner.cleanup_or_fail(state)

    def test_publisher_snapshot_accepts_only_empty_success_as_absent(self) -> None:
        with mock.patch.object(runner.subprocess, "run", return_value=mock.Mock(returncode=0, stdout="")):
            snapshot = runner.snapshot_publisher()
        self.assertEqual(11, len(snapshot.resources))
        self.assertTrue(all(not resource.existed and resource.prior is None for resource in snapshot.resources))
        with mock.patch.object(runner.subprocess, "run", return_value=mock.Mock(returncode=1, stdout="")):
            with self.assertRaises(SystemExit):
                runner.snapshot_publisher()

    def test_local_contract_validator_and_runner_do_not_run_live(self) -> None:
        with mock.patch.object(runner.urllib.request, "urlopen") as urlopen, mock.patch.object(
            runner.subprocess, "run"
        ) as subprocess_run:
            gate.main()
            self.assertFalse(urlopen.called)
            self.assertFalse(subprocess_run.called)

    def test_fresh_runner_import_cannot_call_network_or_subprocess(self) -> None:
        path = str(runner.__file__)
        forbidden_opener = mock.Mock()
        forbidden_opener.open.side_effect = AssertionError("network opener on import")
        with mock.patch("urllib.request.build_opener", return_value=forbidden_opener) as build_opener, mock.patch(
            "subprocess.run", side_effect=AssertionError("subprocess on import")
        ):
            runpy.run_path(path, run_name="c41_runner_import_probe")
        build_opener.assert_not_called()
        forbidden_opener.open.assert_not_called()

    def test_api_key_and_service_requests_follow_openapi_security_contract(self) -> None:
        cleanup: list[tuple[str, str]] = []
        with mock.patch.object(runner, "control_request", return_value=(201, {"key_id": "id", "key_value": "key"})) as request:
            runner.create_registered_api_key("token", cleanup, name="ak", rpm=1)
        self.assertEqual(["scope:inference:invoke"], request.call_args.args[3]["scopes"])
        state = runner.CleanupState()
        digest = "registry.example/model@sha256:" + "a" * 64
        with mock.patch.object(runner, "control_request", return_value=(202, {"id": "svc"})) as request:
            runner.create_inference_service("token", state, name="ani-c41-shared", model_version_id="model", image_ref=digest, mode="generate")
        body = request.call_args.args[3]
        self.assertNotIn("engine", body)
        self.assertEqual(digest, body["image_ref"])
        with self.assertRaises(SystemExit):
            runner.create_inference_service("token", state, name="x", model_version_id="model", image_ref="registry.example/model:tag", mode="generate")

    def test_gateway_http_error_retains_only_headers_and_requires_no_redirect(self) -> None:
        class Body:
            def read(self) -> bytes:
                return b"ani_dev_leaked"
            def close(self) -> None:
                return None
        error = runner.urllib.error.HTTPError("https://public.example/v1/x", 429, "rate", {"Retry-After": "3"}, Body())
        with mock.patch.dict(os.environ, {"ANI_C41_GATEWAY_URL": "https://public.example"}, clear=False), mock.patch.object(
            runner, "_urlopen_no_redirect", side_effect=error
        ):
            status, body, headers = runner.gateway_response("/v1/x")
        self.assertEqual((429, "", {"retry-after": "3"}), (status, body, headers))

    def test_secret_commands_are_allowlist_only(self) -> None:
        allowed = ["-n", "ani-aigw", "get", "secrets", "-l", runner.MANAGED_BY_LABEL, "-o", "name"]
        with mock.patch.object(runner.subprocess, "run", return_value=mock.Mock(returncode=0, stdout="", stderr="")):
            self.assertEqual("", runner.kubectl(allowed))
        for disallowed in (
            ["get", "secret", "x"], ["get", "secret", "x", "-o", "yaml"],
            ["get", "secret", "x", "-o", "jsonpath={.metadata.name}"],
            ["get", "secrets", "-o", "custom-columns=NAME:.metadata.name"],
        ):
            with self.subTest(disallowed=disallowed), self.assertRaises(SystemExit):
                runner.kubectl(disallowed)

    def test_embedding_validation_rejects_bool_values(self) -> None:
        with self.assertRaises(SystemExit):
            runner._embedding_evidence('{"data":[{"embedding":[true]}]}')

    def test_validator_rejects_duplicate_checks_and_wrong_forbidden_list(self) -> None:
        document = gate.load_gate(gate.DEFAULT_GATE)
        document["live_checks"].append(document["live_checks"][0])
        with self.assertRaises(SystemExit):
            gate.validate_contract(document)
        document = gate.load_gate(gate.DEFAULT_GATE)
        document["evidence_policy"]["forbidden_content"] = ["Bearer"]
        with self.assertRaises(SystemExit):
            gate.validate_contract(document)

    def test_cleanup_waits_for_runner_owned_publications_and_policy_delete_is_idempotent(self) -> None:
        state = runner.CleanupState(services=[("token", "svc")], policies=[("token", "policy")])
        with mock.patch.object(runner, "delete_policy", return_value=None) as delete_policy, mock.patch.object(
            runner, "delete_inference_service", return_value=None
        ), mock.patch.object(runner, "wait_for_publications_absent", return_value=None) as wait:
            runner.cleanup_or_fail(state)
        self.assertTrue(wait.called)
        self.assertEqual("policy", delete_policy.call_args.args[1])

    def test_policy_create_uses_exact_contract_and_registers_before_validation(self) -> None:
        cleanup = runner.CleanupState()
        with mock.patch.object(runner, "control_request", return_value=(201, {"id": "policy-1"})) as request:
            runner.create_policy("tenant", cleanup, name="c41", priority=2000, scope={"type": "inference_service_api_key", "inference_service_ids": ["svc"], "api_key_ids": ["ak"]}, access={"allow_all_tenant_keys": False, "allow_api_key_ids": ["ak"], "deny_api_key_ids": []})
        body = request.call_args.args[3]
        self.assertEqual(set(body), {"idempotency_key", "name", "status", "priority", "scope", "access", "rate_limits", "concurrency"})
        self.assertEqual([("tenant", "policy-1")], cleanup.policies)

    def test_fault_injection_scales_each_known_deployment_and_restores_on_probe_error(self) -> None:
        snapshots = {key: {"metadata": {"resourceVersion": "1"}, "spec": {"replicas": 1}} for key in runner.FAULT_DEPLOYMENTS}
        with mock.patch.object(runner, "snapshot_deployment", side_effect=lambda key: snapshots[key]), mock.patch.object(
            runner, "scale_fault_to_zero", return_value=None
        ) as scale, mock.patch.object(runner, "assert_fault_503_no_target_counter", side_effect=SystemExit("503")), mock.patch.object(
            runner, "restore_deployment", return_value=None
        ) as restore:
            with self.assertRaises(SystemExit):
                runner.run_fault_injections("key", "service-id", "tenant-id")
        self.assertEqual(1, scale.call_count)
        self.assertEqual(1, restore.call_count)

    def test_selected_vllm_identity_rejects_wrong_tenant_namespace_and_multiplicity(self) -> None:
        service_id = "11111111-1111-1111-1111-111111111111"
        tenant_id = "22222222-2222-2222-2222-222222222222"
        service = {"metadata": {"name": "pw-" + service_id, "namespace": "ani-tenant-" + tenant_id, "labels": {"services.ani.io/inference-service-id": service_id, "ani.kubercloud.io/owner-ref": service_id, "ani.kubercloud.io/tenant-id": tenant_id}}}
        slices = {"items": [{"ports": [{"port": 8000}], "endpoints": [{"conditions": {"ready": True}, "targetRef": {"kind": "Pod", "name": "vllm"}}]}]}
        pod = {"metadata": {"namespace": "ani-tenant-" + tenant_id}, "spec": {"containers": [{"name": "vllm", "image": "vllm"}]}}
        with mock.patch.object(runner, "kubectl_json", side_effect=[{"items": [service]}, slices, pod]), mock.patch.object(runner, "kubectl", return_value="access /v1/chat/completions"):
            identity = runner.selected_vllm_identity(service_id, tenant_id)
        self.assertEqual(tenant_id, identity["tenant_id"])
        bad = dict(service); bad["metadata"] = dict(service["metadata"]); bad["metadata"]["namespace"] = "ani-tenant-other"
        with mock.patch.object(runner, "kubectl_json", return_value={"items": [bad]}):
            with self.assertRaises(SystemExit): runner.selected_vllm_identity(service_id, tenant_id)

    def test_selected_vllm_counter_and_logs_are_bound_to_verified_tenant_identity(self) -> None:
        identity = {
            "service_id": "service-a", "tenant_id": "tenant-a", "namespace": "ani-tenant-tenant-a",
            "pod": "vllm-pod", "container": "vllm",
        }
        with mock.patch.object(runner, "selected_vllm_identity", return_value=identity) as selected, mock.patch.object(
            runner, "kubectl", return_value="POST /v1/chat/completions\n"
        ) as kubectl:
            self.assertEqual(1, runner.selected_vllm_counter("service-a", "tenant-a"))
        selected.assert_called_once_with("service-a", "tenant-a")
        self.assertEqual("ani-tenant-tenant-a", kubectl.call_args.args[0][1])

    def test_invalid_credentials_include_real_tenant_login_jwt(self) -> None:
        with mock.patch.object(runner, "_chat", return_value=(401, "")) as chat:
            runner.assert_invalid_credentials_401("real-login-jwt", "revoked-ak")
        self.assertEqual([None, "random", "real-login-jwt", "revoked-ak"], [call.args[0] for call in chat.call_args_list])

    def test_spoof_probe_uses_all_exact_untrusted_identity_headers(self) -> None:
        with mock.patch.object(runner, "assert_selected_route", return_value=None) as selected:
            runner.assert_spoof_ignored("ak", "model", "service-a", "tenant-a", "service-b", "tenant-b")
        self.assertEqual(
            {"x-ani-inference-service-id": "spoof", "x-ani-tenant-id": "spoof", "x-ani-user-id": "spoof"},
            selected.call_args.args[-1],
        )

    def test_secret_guard_allows_only_the_runner_metadata_command(self) -> None:
        allowed = ["-n", "ani-aigw", "get", "secrets", "-l", runner.MANAGED_BY_LABEL, "-o", "name"]
        runner._forbid_secret_data_command(allowed)
        for command in (
            ["get", "secrets", "-l", runner.MANAGED_BY_LABEL, "-o", "name"],
            ["-n", "ani-aigw", "delete", "secrets", "-l", runner.MANAGED_BY_LABEL, "-o", "name"],
            ["-n", "ani-aigw", "get", "secrets", "-l", "x=y", "-o", "name"],
            ["-n", "ani-aigw", "get", "secret/name", "-o", "name"],
            ["-n", "ani-aigw", "get", "secrets/name", "-o", "name"],
            ["-n", "ani-aigw", "get", "secret.v1", "-o", "name"],
        ):
            with self.subTest(command=command), self.assertRaises(SystemExit): runner._forbid_secret_data_command(command)

    def test_publisher_snapshot_covers_every_task8_object_before_apply(self) -> None:
        documents = runner._task8_manifest_documents()

        def probe(command: list[str], **_kwargs: object) -> mock.Mock:
            self.assertIn("--ignore-not-found", command)
            return mock.Mock(returncode=0, stdout="")

        with mock.patch.object(runner.subprocess, "run", side_effect=probe):
            snapshot = runner.snapshot_publisher()
        self.assertEqual(11, len(snapshot.resources))
        self.assertEqual(
            {(item["kind"], item["metadata"]["namespace"], item["metadata"]["name"]) for item in documents},
            {(item.kind, item.namespace, item.name) for item in snapshot.resources},
        )

    def test_publisher_rollback_cas_restores_existing_and_uid_deletes_created(self) -> None:
        existing_object = {
            "apiVersion": "apps/v1", "kind": "Deployment",
            "metadata": {"name": "publisher", "namespace": "ani-system", "uid": "old-uid", "resourceVersion": "old-rv"},
            "spec": {"replicas": 1}, "status": {"readyReplicas": 1},
        }
        existing = runner.PublisherResourceSnapshot(
            api_version="apps/v1", kind="Deployment", namespace="ani-system", name="publisher",
            resource="deployment", existed=True, prior=existing_object, prior_uid="old-uid", mutation_attempted=True,
        )
        created = runner.PublisherResourceSnapshot(
            api_version="v1", kind="Service", namespace="ani-aigw", name="created",
            resource="service", existed=False, prior=None, prior_uid=None, created_uid="created-uid", mutation_attempted=True,
        )
        snapshot = runner.PublisherSnapshot([existing, created])
        current = {
            ("Deployment", "publisher"): {"metadata": {"name": "publisher", "namespace": "ani-system", "uid": "old-uid", "resourceVersion": "current-rv"}},
            ("Service", "created"): {"metadata": {"name": "created", "namespace": "ani-aigw", "uid": "created-uid", "resourceVersion": "created-rv"}},
        }

        def probe(item: object) -> dict[str, object] | None:
            return current[(item.kind, item.name)]

        with mock.patch.object(runner, "_probe_publisher_resource", side_effect=probe), mock.patch.object(
            runner.subprocess, "run", return_value=mock.Mock(returncode=0)
        ) as replace, mock.patch.object(runner, "kubectl", return_value="") as kubectl, mock.patch.object(
            runner, "_wait_publisher_resource_absent", return_value=None
        ) as wait_absent:
            runner.restore_publisher(snapshot)
        restored = json.loads(replace.call_args.kwargs["input"])
        self.assertNotIn("status", restored)
        self.assertEqual("current-rv", restored["metadata"]["resourceVersion"])
        self.assertEqual(existing_object["spec"], restored["spec"])
        self.assertIn("created", kubectl.call_args_list[0].args[0])
        wait_absent.assert_called_once_with(created)

    def test_failure_categories_aggregate_without_exception_text(self) -> None:
        error = runner.aggregate_failures(SystemExit("primary secret"), SystemExit("cleanup bearer"), SystemExit("restore key"))
        self.assertEqual("live gate failed: primary,cleanup,publisher-restore", str(error))

    def test_publisher_prepare_dry_run_precedes_apply_and_absent_is_only_empty_success(self) -> None:
        snapshot = runner.PublisherSnapshot([])
        order: list[str] = []
        with mock.patch.object(runner, "validate_task8_server_dry_run", side_effect=lambda: order.append("dry-run")), mock.patch.object(
            runner, "snapshot_publisher", side_effect=lambda: (order.append("snapshot"), snapshot)[1]
        ), mock.patch.object(runner, "apply_publisher", side_effect=lambda _url, _snapshot: order.append("apply")):
            snapshot = runner.prepare_publisher("https://public.example")
        self.assertEqual(["dry-run", "snapshot", "apply"], order)

    def test_task8_server_dry_run_checks_all_11_objects(self) -> None:
        with mock.patch.object(runner.subprocess, "run", return_value=mock.Mock(returncode=0, stdout="")) as run:
            runner.validate_task8_server_dry_run()
        self.assertEqual(11, run.call_count)
        self.assertTrue(all("--dry-run=server" in call.args[0] and "--server-side" in call.args[0] for call in run.call_args_list))

    def test_publisher_dry_run_failure_causes_zero_actual_mutation(self) -> None:
        with mock.patch.object(runner, "validate_task8_server_dry_run", side_effect=SystemExit("dry-run")), mock.patch.object(
            runner, "snapshot_publisher"
        ) as snapshot, mock.patch.object(runner, "apply_publisher") as apply:
            with self.assertRaises(SystemExit):
                runner.prepare_publisher("https://public.example")
        snapshot.assert_not_called()
        apply.assert_not_called()

    def test_publisher_apply_uses_11_individual_ssa_objects_and_records_created_uids(self) -> None:
        resources = [runner._publisher_resource_from_document(document) for document in runner._task8_manifest_documents()]
        snapshot = runner.PublisherSnapshot(resources)

        def current(resource: object) -> dict[str, object]:
            return {
                "apiVersion": resource.api_version, "kind": resource.kind,
                "metadata": {"namespace": resource.namespace, "name": resource.name, "uid": "uid-" + resource.name, "resourceVersion": "1"},
            }

        with mock.patch.object(runner.subprocess, "run", return_value=mock.Mock(returncode=0)) as run, mock.patch.object(
            runner, "_probe_publisher_resource", side_effect=current
        ), mock.patch.object(runner, "set_publisher_public_base_url") as configure:
            runner.apply_publisher("https://public.example", snapshot)
        self.assertEqual(11, run.call_count)
        self.assertTrue(snapshot.mutation_started)
        self.assertTrue(all(resource.created_uid == "uid-" + resource.name for resource in resources))
        self.assertTrue(all(call.args[0] == ["kubectl", "apply", "--server-side", "--force-conflicts", "-f", "-"] for call in run.call_args_list))
        configure.assert_called_once_with("https://public.example")

    def test_publisher_prepare_partial_apply_failure_aggregates_restore_failure(self) -> None:
        snapshot = runner.PublisherSnapshot([], mutation_started=True)
        with mock.patch.object(runner, "validate_task8_server_dry_run"), mock.patch.object(
            runner, "snapshot_publisher", return_value=snapshot
        ), mock.patch.object(runner, "apply_publisher", side_effect=SystemExit("apply secret")), mock.patch.object(
            runner, "restore_publisher", side_effect=SystemExit("restore bearer")
        ) as restore:
            with self.assertRaises(SystemExit) as raised:
                runner.prepare_publisher("https://public.example")
        self.assertEqual("live gate failed: primary,publisher-restore", str(raised.exception))
        restore.assert_called_once_with(snapshot)

    def test_publisher_restore_attempts_every_mutated_resource_before_failing(self) -> None:
        resources = [
            runner.PublisherResourceSnapshot("v1", "Service", "ani-aigw", name, "service", True, {}, "uid", mutation_attempted=True)
            for name in ("one", "two")
        ]
        with mock.patch.object(runner, "_restore_publisher_resource", side_effect=SystemExit("sensitive")) as restore:
            with self.assertRaises(SystemExit) as raised:
                runner.restore_publisher(runner.PublisherSnapshot(resources, mutation_started=True))
        self.assertEqual(2, restore.call_count)
        self.assertEqual("C41 AI Gateway live gate failed: publisher manifest rollback failed: restore-existing,restore-existing", str(raised.exception))

    def test_fault_sequence_restores_each_snapshot_before_next_target(self) -> None:
        snapshots = {key: {"metadata": {"resourceVersion": "old", "generation": 1}, "spec": {"replicas": 1}, "status": {"observedGeneration": 1, "availableReplicas": 1}} for key in runner.FAULT_DEPLOYMENTS}
        with mock.patch.object(runner, "snapshot_deployment", side_effect=lambda key: snapshots[key]), mock.patch.object(runner, "scale_fault_to_zero", return_value=None) as down, mock.patch.object(runner, "assert_fault_503_no_target_counter", return_value=None), mock.patch.object(runner, "restore_deployment", return_value=None) as restore, mock.patch.object(runner, "assert_gateway_counter_recovers", return_value=None):
            runner.run_fault_injections("key", "service", "tenant")
        self.assertEqual(4, down.call_count)
        self.assertEqual(4, restore.call_count)

    def test_fault_scale_waits_for_the_matching_service_endpointslice_to_drain(self) -> None:
        snapshot = {"metadata": {"generation": 1}, "spec": {"replicas": 1}}
        ready = {"items": [{"endpoints": [{"conditions": {"ready": True}}]}]}
        drained = {"items": [{"endpoints": [{"conditions": {"ready": False}}]}]}
        with mock.patch.object(runner, "kubectl") as kubectl, mock.patch.object(
            runner, "kubectl_json", side_effect=[ready, drained]
        ) as kubectl_json, mock.patch.object(runner.time, "monotonic", side_effect=[0, 1, 2]), mock.patch.object(
            runner.time, "sleep", return_value=None
        ):
            runner.scale_fault_to_zero("adapter", snapshot)
        self.assertEqual(["-n", "ani-aigw", "scale", "deployment/envoy-authz-adapter", "--replicas=0"], kubectl.call_args.args[0])
        self.assertEqual(2, kubectl_json.call_count)
        self.assertTrue(all("kubernetes.io/service-name=envoy-authz-adapter" in call.args[0] for call in kubectl_json.call_args_list))

    def test_fault_recovery_requires_generation_ready_endpoints_then_gateway_counter(self) -> None:
        deployment = {"metadata": {"generation": 7}, "status": {"observedGeneration": 7, "readyReplicas": 2}}
        with mock.patch.object(runner, "kubectl_json", return_value=deployment), mock.patch.object(
            runner, "_ready_endpoint_count", return_value=2
        ) as endpoints:
            runner.wait_fault_target_recovered("adapter", 2, timeout=1)
        endpoints.assert_called_once_with("ani-aigw", "envoy-authz-adapter")
        with mock.patch.object(runner, "selected_vllm_counter", side_effect=[8, 9]), mock.patch.object(
            runner, "_chat", return_value=(200, "")
        ) as chat:
            runner.assert_gateway_counter_recovers("ak", "service", "tenant")
        chat.assert_called_once_with("ak", "ani-c41-shared")

    def test_fault_restore_uses_current_resource_version_and_exact_snapshot(self) -> None:
        snapshot = {
            "apiVersion": "apps/v1", "kind": "Deployment",
            "metadata": {"name": "envoy-authz-adapter", "namespace": "ani-aigw", "resourceVersion": "old", "generation": 4, "labels": {"keep": "exact"}},
            "spec": {"replicas": 2, "template": {"metadata": {"labels": {"keep": "exact"}}, "spec": {"containers": [{"name": "adapter", "image": "exact"}]}}},
            "status": {"observedGeneration": 4, "availableReplicas": 2},
        }
        current = {"metadata": {"resourceVersion": "current"}}
        with mock.patch.object(runner, "kubectl_json", return_value=current), mock.patch.object(
            runner.subprocess, "run", return_value=mock.Mock(returncode=0)
        ) as run, mock.patch.object(runner, "wait_fault_target_recovered", create=True) as wait:
            runner.restore_deployment("adapter", snapshot)
        restored = json.loads(run.call_args.kwargs["input"])
        expected = dict(snapshot)
        expected.pop("status")
        expected["metadata"] = dict(snapshot["metadata"], resourceVersion="current")
        self.assertEqual(expected, restored)
        wait.assert_called_once_with("adapter", 2)

    def test_fault_primary_and_restore_errors_are_both_retained_as_categories(self) -> None:
        snapshot = {"metadata": {"generation": 1}, "spec": {"replicas": 1}, "status": {}}
        with mock.patch.object(runner, "snapshot_deployment", return_value=snapshot), mock.patch.object(
            runner, "scale_fault_to_zero", side_effect=SystemExit("primary secret")
        ), mock.patch.object(runner, "restore_deployment", side_effect=SystemExit("restore bearer")):
            with self.assertRaises(SystemExit) as raised:
                runner.run_fault_injections("key", "service", "tenant")
        self.assertEqual("fault injection failed: primary,restore", str(raised.exception))

    def test_stop_requires_unpublished_nonstopped_state_before_stopped(self) -> None:
        with mock.patch.object(runner, "control_request", side_effect=[(200, {"status": "stopped", "invocation_url": ""})]), mock.patch.object(runner, "gateway_request", return_value=(404, "")):
            with self.assertRaises(SystemExit): runner.wait_for_unpublished_then_stopped("token", "svc", timeout=1)

    def test_stop_allows_initial_200_then_requires_ak_404_while_runtime_ready(self) -> None:
        states = [
            (200, {"status": "stopping", "invocation_url": "https://public.example/v1/chat/completions"}),
            (200, {"status": "stopping", "invocation_url": ""}),
            (200, {"status": "stopped", "invocation_url": ""}),
        ]
        with mock.patch.object(runner, "control_request", side_effect=states), mock.patch.object(
            runner, "_chat", side_effect=[(200, ""), (404, "")]
        ) as chat, mock.patch.object(runner, "selected_vllm_identity", return_value={"tenant_id": "tenant", "pod": "ready", "container": "vllm"}) as selected, mock.patch.object(
            runner.time, "sleep", return_value=None
        ):
            runner.wait_for_unpublished_then_stopped("token", "svc", "ak", "tenant", timeout=5)
        self.assertEqual(["ak", "ak"], [call.args[0] for call in chat.call_args_list])
        selected.assert_called_once_with("svc", "tenant")

    def test_start_requires_running_public_200_and_selected_counter_increase(self) -> None:
        with mock.patch.object(runner, "wait_for_running", return_value={"status": "running"}), mock.patch.object(
            runner, "selected_vllm_counter", side_effect=[4, 5]
        ), mock.patch.object(runner, "_chat", return_value=(200, "")):
            runner.wait_for_started("token", "svc", "https://public.example/v1/chat/completions", "ak", "tenant")

    def test_publisher_restart_validates_current_service_identity_and_generation(self) -> None:
        service_id = "11111111-1111-1111-1111-111111111111"
        tenant_id = "22222222-2222-2222-2222-222222222222"
        name = "ani-inf-" + service_id
        labels = {
            "app.kubernetes.io/managed-by": "ani-inference-gateway-publisher",
            "ani.kubercloud.io/inference-service-id": service_id,
            "ani.kubercloud.io/tenant-id": tenant_id,
            "ani.kubercloud.io/publication-generation": "7",
        }
        items = [{"kind": kind, "metadata": {"name": name, "namespace": "ani-aigw", "labels": labels}} for kind in ("Backend", "AIServiceBackend", "AIGatewayRoute")]
        with mock.patch.object(runner, "control_request", return_value=(200, {"id": service_id, "generation": 7})), mock.patch.object(
            runner, "kubectl_json", return_value={"items": items}
        ):
            runner.assert_current_publication("token", service_id, tenant_id)
        items[0]["metadata"]["labels"] = dict(labels, **{"ani.kubercloud.io/publication-generation": "6"})
        with mock.patch.object(runner, "control_request", return_value=(200, {"id": service_id, "generation": 7})), mock.patch.object(
            runner, "kubectl_json", return_value={"items": items}
        ):
            with self.assertRaises(SystemExit):
                runner.assert_current_publication("token", service_id, tenant_id)

    def test_delete_accepts_async_or_sync_success_then_waits_service_and_three_cr_absence(self) -> None:
        for status in (200, 202, 204):
            with self.subTest(status=status), mock.patch.object(runner, "control_request", return_value=(status, {})), mock.patch.object(
                runner, "wait_for_service_deleted"
            ) as service_gone, mock.patch.object(runner, "wait_for_publications_absent") as cr_gone:
                runner.delete_inference_service_for_reuse("token", "svc")
            service_gone.assert_called_once_with("token", "svc")
            cr_gone.assert_called_once_with("svc")

    def test_run_live_aggregates_primary_cleanup_and_publisher_restore_failures(self) -> None:
        environment = {
            "KUBECONFIG": "/approved", "ANI_C41_CONTROL_PLANE_URL": "https://control.example/api/v1",
            "ANI_C41_GATEWAY_URL": "https://public.example", "ANI_C41_TENANT_A_ACCESS_TOKEN": "tenant-a.jwt",
            "ANI_C41_TENANT_B_ACCESS_TOKEN": "tenant-b.jwt", "ANI_C41_CHAT_MODEL_VERSION_ID": "chat",
            "ANI_C41_EMBED_MODEL_VERSION_ID": "embed", "ANI_C41_CHAT_IMAGE_REF": "repo/chat@sha256:" + "a" * 64,
            "ANI_C41_EMBED_IMAGE_REF": "repo/embed@sha256:" + "b" * 64,
        }
        snapshot = runner.PublisherSnapshot([], mutation_started=True)
        with mock.patch.dict(os.environ, environment, clear=True), mock.patch.object(
            runner, "tenant_id_from_login_jwt", side_effect=["11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"]
        ), mock.patch.object(runner, "snapshot_publisher", return_value=snapshot), mock.patch.object(
            runner, "validate_task8_server_dry_run", return_value=None
        ), mock.patch.object(runner, "apply_publisher", side_effect=SystemExit("primary secret")), mock.patch.object(
            runner, "create_registered_api_key"
        ), mock.patch.object(runner, "cleanup_or_fail", side_effect=SystemExit("cleanup bearer")), mock.patch.object(
            runner, "restore_publisher", side_effect=SystemExit("restore ani_key")
        ), mock.patch.object(runner, "write_evidence_atomically") as write:
            with self.assertRaises(SystemExit) as raised:
                runner.run_live()
        self.assertEqual("live gate failed: primary,cleanup,publisher-restore", str(raised.exception))
        write.assert_not_called()

    def test_run_live_wires_tenant_identity_fault_publisher_and_finalization_before_evidence(self) -> None:
        tenant_a_id = "11111111-1111-1111-1111-111111111111"
        tenant_b_id = "22222222-2222-2222-2222-222222222222"
        environment = {
            "KUBECONFIG": "/approved", "ANI_C41_CONTROL_PLANE_URL": "https://control.example/api/v1",
            "ANI_C41_GATEWAY_URL": "https://public.example",
            "ANI_C41_TENANT_A_ACCESS_TOKEN": self._jwt_for_tenant(tenant_a_id),
            "ANI_C41_TENANT_B_ACCESS_TOKEN": self._jwt_for_tenant(tenant_b_id),
            "ANI_C41_CHAT_MODEL_VERSION_ID": "chat", "ANI_C41_EMBED_MODEL_VERSION_ID": "embed",
            "ANI_C41_CHAT_IMAGE_REF": "repo/chat@sha256:" + "a" * 64,
            "ANI_C41_EMBED_IMAGE_REF": "repo/embed@sha256:" + "b" * 64,
        }
        snapshot = runner.PublisherSnapshot([], mutation_started=True)
        created = [{"id": value} for value in ("svc-a", "svc-b", "svc-embed", "svc-a-only", "svc-a-only-reused")]
        key_results = iter((("ak-a-id", "ak-a"), ("ak-b-id", "ak-b"), ("rpm-id", "rpm"), ("revoked-id", "revoked")))
        service_results = iter(created)
        order: list[str] = []
        captured: dict[str, object] = {}

        def create_key(token: str, cleanup: list[tuple[str, str]], **_kwargs: object) -> tuple[str, str]:
            key_id, key = next(key_results)
            cleanup.append((token, key_id))
            return key_id, key

        def create_service(token: str, cleanup: runner.CleanupState, **_kwargs: object) -> dict[str, str]:
            result = next(service_results)
            cleanup.register_service(token, result["id"])
            return result

        def capture_evidence(_target: Path, evidence: dict[str, object]) -> None:
            order.append("write")
            captured.update(evidence)

        with ExitStack() as stack:
            stack.enter_context(mock.patch.dict(os.environ, environment, clear=True))
            stack.enter_context(mock.patch.object(runner, "snapshot_publisher", return_value=snapshot))
            stack.enter_context(mock.patch.object(runner, "validate_task8_server_dry_run"))
            stack.enter_context(mock.patch.object(runner, "apply_publisher", side_effect=lambda _url, _snapshot: order.append("apply")))
            stack.enter_context(mock.patch.object(runner, "create_registered_api_key", side_effect=create_key))
            stack.enter_context(mock.patch.object(runner, "revoke_api_key"))
            stack.enter_context(mock.patch.object(runner, "create_inference_service", side_effect=create_service))
            stack.enter_context(mock.patch.object(runner, "wait_for_running"))
            chat = stack.enter_context(mock.patch.object(runner, "_chat", side_effect=[
                (200, '{"choices":[{}]}'), (200, "data: {}\ndata: [DONE]\n"),
                (404, ""), (404, ""), (401, ""), (401, ""), (401, ""), (401, ""), (200, ""),
            ]))
            embeddings = stack.enter_context(mock.patch.object(
                runner, "_embeddings", side_effect=[(200, '{"data":[{"embedding":[1.0]}]}'), (404, "")],
            ))
            selected_route = stack.enter_context(mock.patch.object(runner, "assert_selected_route"))
            models = stack.enter_context(mock.patch.object(runner, "gateway_request", return_value=(404, "")))
            rpm_second = stack.enter_context(mock.patch.object(runner, "gateway_response", return_value=(429, "", {"retry-after": "1"})))
            policy_precedence = stack.enter_context(mock.patch.object(
                runner, "probe_policy_precedence", return_value={"specific_allow_200": True, "lower_tenant_deny_403": True},
            ))
            lifecycle = stack.enter_context(mock.patch.object(runner, "apply_lifecycle"))
            stopped = stack.enter_context(mock.patch.object(runner, "wait_for_unpublished_then_stopped"))
            started = stack.enter_context(mock.patch.object(runner, "wait_for_started"))
            faults = stack.enter_context(mock.patch.object(runner, "run_fault_injections"))
            stack.enter_context(mock.patch.object(runner, "delete_inference_service_for_reuse"))
            reconcile = stack.enter_context(mock.patch.object(runner, "verify_publisher_reconcile"))
            stack.enter_context(mock.patch.object(runner, "assert_no_managed_ak_secret"))
            logs = stack.enter_context(mock.patch.object(runner, "assert_logs_redacted"))
            stack.enter_context(mock.patch.object(runner, "cleanup_or_fail", side_effect=lambda _state: order.append("cleanup")))
            stack.enter_context(mock.patch.object(runner, "restore_publisher", side_effect=lambda _snapshot: order.append("restore")))
            stack.enter_context(mock.patch.object(runner, "write_evidence_atomically", side_effect=capture_evidence))
            evidence = runner.run_live()

        self.assertEqual(9, chat.call_count)
        self.assertEqual(environment["ANI_C41_TENANT_A_ACCESS_TOKEN"], chat.call_args_list[6].args[0])
        self.assertEqual(2, embeddings.call_count)
        self.assertEqual(4, selected_route.call_count)
        models.assert_called_once_with("/v1/models")
        rpm_second.assert_called_once()
        policy_precedence.assert_called_once()
        self.assertEqual(["stop", "start"], [call.args[2] for call in lifecycle.call_args_list])
        stopped.assert_called_once()
        started.assert_called_once()
        faults.assert_called_once_with("ak-a", "svc-a", tenant_a_id)
        expected_publications = [
            (environment["ANI_C41_TENANT_A_ACCESS_TOKEN"], "svc-a", tenant_a_id),
            (environment["ANI_C41_TENANT_B_ACCESS_TOKEN"], "svc-b", tenant_b_id),
            (environment["ANI_C41_TENANT_A_ACCESS_TOKEN"], "svc-embed", tenant_a_id),
            (environment["ANI_C41_TENANT_A_ACCESS_TOKEN"], "svc-a-only-reused", tenant_a_id),
        ]
        reconcile.assert_called_once_with(expected_publications)
        self.assertEqual([("svc-a", tenant_a_id), ("svc-b", tenant_b_id), ("svc-embed", tenant_a_id), ("svc-a-only-reused", tenant_a_id)], logs.call_args.args[1])
        self.assertLess(order.index("cleanup"), order.index("restore"))
        self.assertLess(order.index("restore"), order.index("write"))
        cleanup = next(check for check in evidence["checks"] if check["id"] == "cleanup-complete")
        self.assertEqual({"runner_owned_resources_removed": True, "publisher_restored": True}, {key: value for key, value in cleanup.items() if key != "id"})
        self.assertEqual("passed", captured["status"])

    def test_run_live_dry_run_failure_never_applies_or_restores_unmutated_publisher(self) -> None:
        tenant_a_id = "11111111-1111-1111-1111-111111111111"
        tenant_b_id = "22222222-2222-2222-2222-222222222222"
        environment = {
            "KUBECONFIG": "/approved", "ANI_C41_CONTROL_PLANE_URL": "https://control.example/api/v1",
            "ANI_C41_GATEWAY_URL": "https://public.example",
            "ANI_C41_TENANT_A_ACCESS_TOKEN": self._jwt_for_tenant(tenant_a_id),
            "ANI_C41_TENANT_B_ACCESS_TOKEN": self._jwt_for_tenant(tenant_b_id),
            "ANI_C41_CHAT_MODEL_VERSION_ID": "chat", "ANI_C41_EMBED_MODEL_VERSION_ID": "embed",
            "ANI_C41_CHAT_IMAGE_REF": "repo/chat@sha256:" + "a" * 64,
            "ANI_C41_EMBED_IMAGE_REF": "repo/embed@sha256:" + "b" * 64,
        }
        with mock.patch.dict(os.environ, environment, clear=True), mock.patch.object(
            runner, "snapshot_publisher", return_value=runner.PublisherSnapshot([])
        ), mock.patch.object(runner, "validate_task8_server_dry_run", side_effect=SystemExit("dry-run")), mock.patch.object(
            runner, "apply_publisher"
        ) as apply, mock.patch.object(runner, "cleanup_or_fail"), mock.patch.object(runner, "restore_publisher") as restore:
            with self.assertRaises(SystemExit) as raised:
                runner.run_live()
        self.assertEqual("live gate failed: primary", str(raised.exception))
        apply.assert_not_called()
        restore.assert_not_called()

    def test_policy_precedence_evidence_keeps_both_actual_facts(self) -> None:
        facts = runner.policy_precedence_evidence(True, True)
        self.assertEqual({"specific_allow_200": True, "lower_tenant_deny_403": True}, facts)

    def test_policy_precedence_proves_specific_allow_then_lower_deny_with_both_counters(self) -> None:
        cleanup = runner.CleanupState()
        policy_ids = iter(("specific", "lower"))

        def create_policy(token: str, state: runner.CleanupState, **_kwargs: object) -> str:
            policy_id = next(policy_ids)
            state.register_policy(token, policy_id)
            return policy_id

        with mock.patch.object(runner, "create_policy", side_effect=create_policy), mock.patch.object(
            runner, "selected_vllm_counter", side_effect=[10, 20, 11, 20, 11, 20, 11, 20]
        ), mock.patch.object(runner, "_chat", side_effect=[(200, ""), (403, "")]), mock.patch.object(
            runner, "delete_policy"
        ) as delete:
            facts = runner.probe_policy_precedence(
                "login-jwt", cleanup, "ak", "ak-id", "service-a", "tenant-a", "service-b", "tenant-b"
            )
        self.assertEqual({"specific_allow_200": True, "lower_tenant_deny_403": True}, facts)
        self.assertEqual([mock.call("login-jwt", "specific"), mock.call("login-jwt", "lower")], delete.call_args_list)
        self.assertEqual([], cleanup.policies)


if __name__ == "__main__":
    unittest.main()
