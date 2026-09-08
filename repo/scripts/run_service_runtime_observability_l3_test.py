import base64
import contextlib
import http.server
import json
import os
import subprocess
import sys
import tempfile
import threading
import time
import unittest
from pathlib import Path
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parent))
import run_service_runtime_observability_l3 as gate


NAMESPACE = "ani-service-observability-e2e-0cedae8-0903a"
IMAGES = {
    name: f"harbor.ani.internal/ani/{name}@sha256:{index:064x}"
    for index, name in enumerate(sorted(gate.SERVICES), start=1)
}
FIXTURE_IMAGES = {
    "nats": (
        "docker.changqingyun.cn/ani/nats@sha256:"
        "b83efabe3e7def1e0a4a31ec6e078999bb17c80363f881df35edc70fcb6bb927"
    ),
    "postgres": (
        "docker.changqingyun.cn/ani/postgres@sha256:"
        "5660c2cbfea50c7a9127d17dc4e48543eedd3d7a41a595a2dfa572471e37e64c"
    ),
    "prometheus": (
        "docker.changqingyun.cn/ani/prometheus@sha256:"
        "2659f4c2ebb718e7695cb9b25ffa7d6be64db013daba13e05c875451cf51b0d3"
    ),
    "redis": (
        "docker.changqingyun.cn/ani/redis@sha256:"
        "ff02b58f971e7d7d156a1267e283fcbbeee91773b6aa36c49dac28ecfe28eadf"
    ),
}


class RunServiceRuntimeObservabilityL3Test(unittest.TestCase):
    def test_approval_contract_keeps_auth_available_during_missing_state(self) -> None:
        config = gate.LiveConfig(
            kubectl_binary="/usr/bin/kubectl",
            kubeconfig=Path("/home/kubercloud/.kube/config"),
            context="kubernetes-admin@kubernetes",
            expected_server="https://10.10.1.66:6443",
            namespace=NAMESPACE,
            version="p0-0cedae8",
        )
        approval = gate.approval_plan(config, IMAGES)
        temporary = approval["object_plan"]["temporary"]

        self.assertIn(f"Deployment/{NAMESPACE}/model-service replicas 1->0->1", temporary)
        self.assertNotIn(f"Deployment/{NAMESPACE}/auth-service replicas 1->0->1", temporary)
        self.assertEqual(
            "ani.dev/run-id=0cedae8-0903a,ani.dev/service-name=auth-service",
            approval["object_plan"]["pod_delete_target"]["label_selector"],
        )

        with mock.patch.object(gate, "MISSING_SERVICE", "tenant-service", create=True):
            changed = gate.approval_plan(config, IMAGES)
        self.assertNotEqual(
            approval["required_confirmation"],
            changed["required_confirmation"],
        )

    def test_scrape_fault_policy_is_applied_before_exact_pod_replacement(self) -> None:
        calls: list[tuple[str, object]] = []

        class FakeKubectl:
            def apply(self, document: dict[str, object]) -> None:
                calls.append(("apply", document))

            def run(self, args: list[str]) -> str:
                calls.append(("run", args))
                return ""

        with (
            mock.patch.object(
                gate,
                "_initial_pod",
                return_value=("auth-old", "uid-old", "ani.dev/run-id=exact"),
            ),
            mock.patch.object(
                gate,
                "_wait_new_pod",
                return_value=("auth-new", "uid-new"),
            ),
        ):
            replacement = gate._activate_scrape_fault(FakeKubectl(), NAMESPACE)

        self.assertEqual("apply", calls[0][0])
        self.assertEqual("NetworkPolicy", calls[0][1]["kind"])
        self.assertEqual(
            (
                "run",
                ["-n", NAMESPACE, "delete", "pod", "auth-old", "--wait=true"],
            ),
            calls[1],
        )
        self.assertEqual(
            {
                "selector": "ani.dev/run-id=exact",
                "old_pod": "auth-old",
                "old_uid": "uid-old",
                "new_pod": "auth-new",
                "new_uid": "uid-new",
            },
            replacement,
        )

    def test_port_forward_waits_for_remote_kubectl_readiness_not_local_ssh_listener(self) -> None:
        command = [
            sys.executable,
            "-c",
            (
                "import sys,time; time.sleep(0.2); "
                "print('Forwarding from 127.0.0.1:43123 -> 9200', "
                "file=sys.stdout, flush=True); time.sleep(2)"
            ),
        ]
        with (
            mock.patch.object(gate, "_free_port", return_value=43123),
            mock.patch.object(gate, "service_port_forward_command", return_value=command),
            mock.patch.object(
                gate.socket,
                "create_connection",
                return_value=contextlib.nullcontext(),
            ),
        ):
            started = time.monotonic()
            with gate._service_port_forward(mock.Mock(), NAMESPACE, "auth-service", "health"):
                elapsed = time.monotonic() - started

        self.assertGreaterEqual(elapsed, 0.15)

    def test_loopback_http_get_bypasses_environment_proxy(self) -> None:
        class TargetHandler(http.server.BaseHTTPRequestHandler):
            def do_GET(self) -> None:
                self.send_response(200)
                self.end_headers()
                if self.path == "/healthz":
                    self.wfile.write(b"target-ok")
                else:
                    self.wfile.write(b'{"status":"ok"}')

            def log_message(self, format: str, *args: object) -> None:
                del format, args

        class RejectingProxyHandler(http.server.BaseHTTPRequestHandler):
            def do_GET(self) -> None:
                self.send_response(502)
                self.end_headers()

            def log_message(self, format: str, *args: object) -> None:
                del format, args

        target = http.server.ThreadingHTTPServer(("127.0.0.1", 0), TargetHandler)
        proxy = http.server.ThreadingHTTPServer(("127.0.0.1", 0), RejectingProxyHandler)
        threads = [
            threading.Thread(target=server.serve_forever, daemon=True)
            for server in (target, proxy)
        ]
        for thread in threads:
            thread.start()
        self.addCleanup(target.server_close)
        self.addCleanup(target.shutdown)
        self.addCleanup(proxy.server_close)
        self.addCleanup(proxy.shutdown)

        target_url = f"http://127.0.0.1:{target.server_port}"
        proxy_url = f"http://127.0.0.1:{proxy.server_port}"
        with mock.patch.dict(
            os.environ,
            {
                "HTTP_PROXY": proxy_url,
                "http_proxy": proxy_url,
                "ALL_PROXY": proxy_url,
                "all_proxy": proxy_url,
                "NO_PROXY": "",
                "no_proxy": "",
            },
        ):
            self.assertEqual("target-ok", gate._http_get(target_url, "/healthz"))
            self.assertEqual((200, {"status": "ok"}), gate._platform_health(target_url, None))

    def test_management_probes_use_port_forward_when_service_proxy_is_network_blocked(self) -> None:
        forwarded: list[tuple[str, str]] = []

        class NetworkPolicyBlockedKubectl:
            def run(self, args: list[str]) -> str:
                raise RuntimeError(
                    "kubectl get --raw services/proxy failed: "
                    "dial tcp 10.16.6.198:9200: i/o timeout"
                )

        def fake_port_forward(
            kubectl: object,
            namespace: str,
            service: str,
            remote_port: str,
        ) -> contextlib.AbstractContextManager[str]:
            del kubectl
            self.assertEqual(NAMESPACE, namespace)
            forwarded.append((service, remote_port))
            return contextlib.nullcontext(f"http://{service}.test")

        def fake_http_get(base_url: str, path: str) -> str:
            service = base_url.removeprefix("http://").removesuffix(".test")
            if path == "/metrics":
                return f'target_info{{service_name="{service}"}} 1\n'
            return '{"status":"ok"}'

        with (
            mock.patch.object(
                gate,
                "_service_port_forward",
                side_effect=fake_port_forward,
                create=True,
            ),
            mock.patch.object(gate, "_http_get", side_effect=fake_http_get, create=True),
        ):
            gate._verify_management_endpoints(NetworkPolicyBlockedKubectl(), NAMESPACE)

        self.assertEqual(
            [(service, "health") for service in sorted(gate.SERVICES)],
            forwarded,
        )

    def test_prometheus_queries_use_port_forward_when_service_proxy_is_network_blocked(self) -> None:
        class NetworkPolicyBlockedKubectl:
            def run(self, args: list[str]) -> str:
                raise RuntimeError(
                    "kubectl get --raw services/proxy failed: "
                    "dial tcp 10.16.6.197:9090: i/o timeout"
                )

        with (
            mock.patch.object(
                gate,
                "_service_port_forward",
                return_value=contextlib.nullcontext("http://prometheus.test"),
                create=True,
            ) as port_forward,
            mock.patch.object(
                gate,
                "_http_get",
                return_value='{"status":"success","data":{"result":[]}}',
                create=True,
            ) as http_get,
        ):
            payload = gate._prometheus_query(
                NetworkPolicyBlockedKubectl(),
                NAMESPACE,
                'up{job="ani-components"}',
            )

        self.assertEqual("success", payload["status"])
        port_forward.assert_called_once_with(
            mock.ANY,
            NAMESPACE,
            gate.fixture_renderer.PROMETHEUS_NAME,
            "http",
        )
        http_get.assert_called_once_with(
            "http://prometheus.test",
            "/api/v1/query?query=up%7Bjob%3D%22ani-components%22%7D",
        )

    def test_confirmation_is_bound_to_exact_context_server_and_namespace(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            images_file = Path(directory) / "images.json"
            images_file.write_text(json.dumps(IMAGES), encoding="utf-8")
            expected = gate.confirmation_value(
                "ani-lab",
                "https://10.0.0.1:6443",
                NAMESPACE,
                "p0-0cedae8",
                IMAGES,
                "registry-system/harbor-pull",
            )
            config = gate.LiveConfig(
                kubectl_binary="kubectl",
                kubeconfig=Path("/tmp/kubeconfig"),
                context="ani-lab",
                expected_server="https://10.0.0.1:6443",
                namespace=NAMESPACE,
                version="p0-0cedae8",
                images_file=images_file,
                confirmation=expected,
                image_pull_secret_source="registry-system/harbor-pull",
            )

            self.assertEqual([], gate.validate_config_values(config, check_files=False))
            changed = dict(IMAGES)
            changed["ani-gateway"] = "harbor.ani.internal/ani/ani-gateway@sha256:" + "f" * 64
            images_file.write_text(json.dumps(changed), encoding="utf-8")
            self.assertTrue(
                any(
                    "confirmation" in error
                    for error in gate.validate_config_values(config, check_files=False)
                )
            )

            with mock.patch.object(
                gate.fixture_renderer,
                "PROMETHEUS_IMAGE",
                "docker.changqingyun.cn/ani/prometheus@sha256:" + "e" * 64,
            ):
                fixture_changed = gate.confirmation_value(
                    config.context,
                    config.expected_server,
                    config.namespace,
                    config.version,
                    IMAGES,
                    config.image_pull_secret_source,
                )
            self.assertNotEqual(expected, fixture_changed)

            with mock.patch.object(
                gate.fixture_renderer,
                "render_l3_fixture",
                return_value="changed rendered fixture\n",
            ):
                manifest_changed = gate.confirmation_value(
                    config.context,
                    config.expected_server,
                    config.namespace,
                    config.version,
                    IMAGES,
                    config.image_pull_secret_source,
                )
            self.assertNotEqual(expected, manifest_changed)

    def test_ephemeral_secret_contains_platform_and_tenant_test_credentials(self) -> None:
        secret, token, tenant_token = gate.build_runtime_secret(NAMESPACE, now=1_788_431_000)
        keys = set(secret["stringData"])

        self.assertEqual("Secret", secret["kind"])
        self.assertEqual(gate.RUNTIME_SECRET, secret["metadata"]["name"])
        self.assertTrue(gate.REQUIRED_RUNTIME_SECRET_KEYS <= keys)
        self.assertNotIn(token, json.dumps(gate.secret_safe_summary(secret)))

        parts = token.split(".")
        self.assertEqual(3, len(parts))
        payload = json.loads(base64.urlsafe_b64decode(parts[1] + "=="))
        self.assertEqual("service", payload["principal_kind"])
        self.assertEqual("platform", payload["credential_domain"])
        self.assertEqual(["scope:observability:read"], payload["permissions"])
        self.assertEqual("ani-core", payload["aud"])
        self.assertGreater(payload["exp"], payload["iat"])

        tenant_parts = tenant_token.split(".")
        self.assertEqual(3, len(tenant_parts))
        tenant_payload = json.loads(base64.urlsafe_b64decode(tenant_parts[1] + "=="))
        self.assertEqual("user", tenant_payload["principal_kind"])
        self.assertEqual("tenant", tenant_payload["credential_domain"])
        self.assertEqual("tenant", tenant_payload["scope"])
        self.assertNotEqual("", tenant_payload["tid"])
        self.assertGreater(tenant_payload["exp"], tenant_payload["iat"])

    def test_scrape_fault_policy_targets_exact_run_and_auth_service(self) -> None:
        document = gate.scrape_fault_network_policy(NAMESPACE)
        selector = document["spec"]["podSelector"]["matchLabels"]

        self.assertEqual(NAMESPACE, document["metadata"]["namespace"])
        self.assertEqual("0cedae8-0903a", selector["ani.dev/run-id"])
        self.assertEqual("auth-service", selector["ani.dev/service-name"])
        self.assertEqual([{"protocol": "TCP", "port": 9101}], document["spec"]["ingress"][0]["ports"])

    def test_prometheus_vector_parser_requires_one_sample_per_service(self) -> None:
        payload = {
            "status": "success",
            "data": {
                "resultType": "vector",
                "result": [
                    {"metric": {"ani_service_name": name}, "value": [1_788_431_000, "1"]}
                    for name in sorted(gate.SERVICES)
                ],
            },
        }

        parsed = gate.parse_up_vector(payload)
        self.assertEqual(gate.SERVICES, set(parsed))
        self.assertTrue(all(value == 1 for value in parsed.values()))

        payload["data"]["result"].append(payload["data"]["result"][0])
        with self.assertRaisesRegex(ValueError, "duplicate"):
            gate.parse_up_vector(payload)

    def test_object_plan_is_exact_and_cleanup_deletes_only_namespace(self) -> None:
        plan = gate.object_plan(NAMESPACE, include_pull_secret=True)

        self.assertEqual(31, len(plan["create"]))
        self.assertEqual([], plan["modify_existing"])
        self.assertEqual([f"Namespace/{NAMESPACE}"], plan["cleanup"])
        self.assertIn(f"Secret/{NAMESPACE}/{gate.RUNTIME_SECRET}", plan["create"])
        self.assertIn(
            f"Secret/{NAMESPACE}/{gate.IMAGE_PULL_SECRET}",
            plan["create"],
        )
        self.assertIn(
            f"NetworkPolicy/{NAMESPACE}/{gate.SCRAPE_FAULT_POLICY}",
            plan["temporary"],
        )
        self.assertEqual(
            {
                "namespace": NAMESPACE,
                "label_selector": "ani.dev/run-id=0cedae8-0903a,ani.dev/service-name=auth-service",
                "cardinality": "exactly_one",
            },
            plan["pod_delete_target"],
        )

        config = gate.LiveConfig(
            kubectl_binary="/opt/kubernetes/bin/kubectl",
            kubeconfig=Path("/secure/configs/ani-lab.yaml"),
            context="ani-lab",
            expected_server="https://10.0.0.1:6443",
            namespace=NAMESPACE,
            version="p0-0cedae8",
            image_pull_secret_source="registry-system/harbor-pull",
        )
        self.assertEqual(
            "/opt/kubernetes/bin/kubectl --kubeconfig /secure/configs/ani-lab.yaml "
            f"--context ani-lab delete namespace {NAMESPACE} --wait=true --timeout=300s",
            gate.cleanup_command(config),
        )
        approval = gate.approval_plan(config, IMAGES)
        self.assertEqual(IMAGES, approval["images"])
        self.assertEqual(FIXTURE_IMAGES, approval["fixture_images"])
        self.assertRegex(approval["fixture_manifest_sha256"], r"^sha256:[0-9a-f]{64}$")
        self.assertEqual("registry-system/harbor-pull", approval["image_pull_secret_source"])
        self.assertEqual(gate.SOURCE_COMMIT, approval["source_commit"])
        self.assertEqual(gate.cleanup_command(config), approval["cleanup_command"])
        self.assertEqual(
            gate.confirmation_value(
                config.context,
                config.expected_server,
                config.namespace,
                config.version,
                IMAGES,
                config.image_pull_secret_source,
            ),
            approval["required_confirmation"],
        )

    def test_cluster_permission_preflight_is_complete_and_fails_closed(self) -> None:
        class FakeKubectl:
            def __init__(self, denied: tuple[str, str, str] | None = None) -> None:
                self.denied = denied
                self.calls: list[list[str]] = []

            def result(self, args: list[str], *, input_text: str | None = None) -> subprocess.CompletedProcess[str]:
                del input_text
                self.calls.append(args)
                namespace = ""
                if "--namespace" in args:
                    namespace = args[args.index("--namespace") + 1]
                key = (args[2], args[3], namespace)
                allowed = key != self.denied
                return subprocess.CompletedProcess(args, 0 if allowed else 1, "yes\n" if allowed else "no\n", "")

        allowed = FakeKubectl()
        summary = gate.verify_cluster_permissions(
            allowed,
            namespace=NAMESPACE,
            image_pull_secret_source="registry-system/harbor-pull",
        )

        checked = {
            (args[2], args[3], args[args.index("--namespace") + 1] if "--namespace" in args else "")
            for args in allowed.calls
        }
        self.assertIn(("create", "namespaces", ""), checked)
        self.assertIn(("delete", "namespaces", ""), checked)
        self.assertIn(("create", "deployments.apps", NAMESPACE), checked)
        self.assertIn(("patch", "deployments.apps", NAMESPACE), checked)
        self.assertIn(("delete", "pods", NAMESPACE), checked)
        self.assertIn(("create", "pods/portforward", NAMESPACE), checked)
        self.assertNotIn(("get", "services/proxy", NAMESPACE), checked)
        self.assertIn(("patch", "networkpolicies.networking.k8s.io", NAMESPACE), checked)
        self.assertIn(("get", "secrets", "registry-system"), checked)
        self.assertEqual(len(checked), summary["checks"])

        denied = FakeKubectl(("delete", "pods", NAMESPACE))
        with self.assertRaisesRegex(RuntimeError, "delete pods"):
            gate.verify_cluster_permissions(
                denied,
                namespace=NAMESPACE,
                image_pull_secret_source="",
            )

    def test_ssh_transport_keeps_kubeconfig_remote_and_builds_exact_command(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            images_file = Path(directory) / "images.json"
            images_file.write_text(json.dumps(IMAGES), encoding="utf-8")
            ssh_config = Path(directory) / "ssh-config"
            ssh_config.write_text("Host ani\n  HostName 10.10.1.66\n", encoding="utf-8")
            config = gate.LiveConfig(
                kubectl_binary="/usr/bin/kubectl",
                kubeconfig=Path("/home/kubercloud/.kube/config"),
                context="kubernetes-admin@kubernetes",
                expected_server="https://10.10.1.66:6443",
                namespace=NAMESPACE,
                version="p0-0cedae8",
                images_file=images_file,
                confirmation=gate.confirmation_value(
                    "kubernetes-admin@kubernetes",
                    "https://10.10.1.66:6443",
                    NAMESPACE,
                    "p0-0cedae8",
                    IMAGES,
                    "",
                ),
                ssh_host="ani",
                ssh_config=ssh_config,
            )

            self.assertEqual([], gate.validate_config_values(config, check_files=False))
            self.assertEqual(
                [
                    "ssh",
                    "-F",
                    str(ssh_config),
                    "-o",
                    "BatchMode=yes",
                    "-o",
                    "ConnectTimeout=10",
                    "--",
                    "ani",
                    "/usr/bin/kubectl --kubeconfig /home/kubercloud/.kube/config "
                    "--context kubernetes-admin@kubernetes get nodes",
                ],
                gate.Kubectl(config).command(["get", "nodes"]),
            )
            cleanup = gate.cleanup_command(config)
            self.assertIn("ssh -F", cleanup)
            self.assertIn("/usr/bin/kubectl --kubeconfig /home/kubercloud/.kube/config", cleanup)
            self.assertNotIn(str(ssh_config) + " --context", cleanup)

            port_forward = gate.gateway_port_forward_command(
                gate.Kubectl(config),
                NAMESPACE,
                43123,
            )
            self.assertIn("-L", port_forward)
            self.assertIn("127.0.0.1:43123:127.0.0.1:43123", port_forward)
            self.assertIn("--address=127.0.0.1", port_forward[-1])
            self.assertIn("43123:8080", port_forward[-1])

    def test_ssh_transport_rejects_unsafe_or_relative_remote_values(self) -> None:
        config = gate.LiveConfig(
            kubectl_binary="kubectl",
            kubeconfig=Path("relative-kubeconfig"),
            context="ani-lab",
            expected_server="https://10.0.0.1:6443",
            namespace=NAMESPACE,
            version="p0-0cedae8",
            ssh_host="ani;touch-bad",
            ssh_config=Path("/tmp/ssh-config"),
        )
        errors = gate.validate_config_values(
            config,
            check_files=False,
            require_confirmation=False,
        )
        self.assertTrue(any("SSH host" in error for error in errors), errors)
        self.assertTrue(any("remote kubectl" in error for error in errors), errors)
        self.assertTrue(any("remote kubeconfig" in error for error in errors), errors)


if __name__ == "__main__":
    unittest.main()
