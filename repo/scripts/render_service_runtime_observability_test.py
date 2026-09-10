import json
import sys
import tempfile
import unittest
from pathlib import Path

import yaml

sys.path.insert(0, str(Path(__file__).resolve().parent))
import render_service_runtime_observability as renderer


ROOT = Path(__file__).resolve().parents[1]
INVENTORY = ROOT / "deploy/real-k8s-lab/service-runtime-observability-p0.yaml"
SERVICES = {
    "ani-gateway": (9200, "http"),
    "auth-service": (9201, "grpc"),
    "model-service": (9203, "grpc"),
    "task-service": (9204, "grpc"),
    "inference-service": (9204, "grpc"),
    "tenant-service": (9205, "grpc"),
    "metering-service": (9210, "health"),
}


def digest_images() -> dict[str, str]:
    return {
        name: f"registry.example.invalid/ani/{name}@sha256:{index:064x}"
        for index, name in enumerate(SERVICES, start=1)
    }


class RenderServiceRuntimeObservabilityTest(unittest.TestCase):
    def test_rejects_mutable_image_reference(self) -> None:
        images = digest_images()
        images["ani-gateway"] = "registry.example.invalid/ani/ani-gateway:latest"

        with self.assertRaisesRegex(ValueError, "immutable digest"):
            renderer.render(INVENTORY, "ani-service-observability-e2e-test", "v0.9.0", images)

    def test_renders_seven_deployments_and_services_with_fixed_contract(self) -> None:
        rendered = renderer.render(
            INVENTORY,
            "ani-service-observability-e2e-test",
            "v0.9.0",
            digest_images(),
        )
        documents = [doc for doc in yaml.safe_load_all(rendered) if doc]
        deployments = {doc["metadata"]["name"]: doc for doc in documents if doc["kind"] == "Deployment"}
        services = {doc["metadata"]["name"]: doc for doc in documents if doc["kind"] == "Service"}

        self.assertEqual(set(SERVICES), set(deployments))
        self.assertEqual(set(SERVICES), set(services))
        for name, (management_port, probe_port_name) in SERVICES.items():
            deployment = deployments[name]
            pod = deployment["spec"]["template"]
            container = pod["spec"]["containers"][0]
            labels = pod["metadata"]["labels"]
            env = {entry["name"]: entry for entry in container["env"]}
            ports = {entry["name"]: entry["containerPort"] for entry in container["ports"]}
            service_ports = {entry["name"]: entry["targetPort"] for entry in services[name]["spec"]["ports"]}

            self.assertEqual("ani-platform", labels["app.kubernetes.io/part-of"])
            self.assertEqual(name, labels["ani.dev/service-name"])
            self.assertEqual("true", labels["ani.dev/metrics-scrape"])
            self.assertEqual("test", labels["ani.dev/run-id"])
            self.assertEqual("v0.9.0", labels["app.kubernetes.io/version"])
            self.assertEqual(management_port, ports["health"])
            self.assertEqual("health", service_ports["health"])
            self.assertEqual(probe_port_name, container["readinessProbe"].get("tcpSocket", {}).get("port", probe_port_name))
            self.assertEqual(
                "metadata.labels['ani.dev/service-name']",
                env["ANI_SERVICE_NAME"]["valueFrom"]["fieldRef"]["fieldPath"],
            )
            self.assertEqual(
                "metadata.labels['app.kubernetes.io/version']",
                env["ANI_SERVICE_VERSION"]["valueFrom"]["fieldRef"]["fieldPath"],
            )
            self.assertEqual("metadata.uid", env["POD_UID"]["valueFrom"]["fieldRef"]["fieldPath"])
            self.assertIn("@sha256:", container["image"])

    def test_optional_pull_secret_is_applied_to_every_workload(self) -> None:
        rendered = renderer.render(
            INVENTORY,
            "ani-service-observability-e2e-pull-secret",
            "v0.9.0",
            digest_images(),
            image_pull_secret="ani-runtime-observability-registry",
        )
        deployments = [
            doc for doc in yaml.safe_load_all(rendered) if doc and doc["kind"] == "Deployment"
        ]

        self.assertEqual(7, len(deployments))
        for deployment in deployments:
            self.assertEqual(
                [{"name": "ani-runtime-observability-registry"}],
                deployment["spec"]["template"]["spec"]["imagePullSecrets"],
            )

    def test_images_file_must_match_canonical_service_set(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            path = Path(temp_dir) / "images.json"
            path.write_text(json.dumps({"ani-gateway": digest_images()["ani-gateway"]}), encoding="utf-8")
            with self.assertRaisesRegex(ValueError, "image set"):
                renderer.load_images(path, set(SERVICES))


if __name__ == "__main__":
    unittest.main()
