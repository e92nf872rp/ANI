import sys
import unittest
from pathlib import Path

import yaml

sys.path.insert(0, str(Path(__file__).resolve().parent))
import render_service_runtime_observability_l3 as renderer


ROOT = Path(__file__).resolve().parents[1]
NAMESPACE = "ani-service-observability-e2e-0cedae8-l3"
VERSION = "p0-0cedae8"
SERVICES = {
    "ani-gateway",
    "auth-service",
    "model-service",
    "task-service",
    "inference-service",
    "tenant-service",
    "metering-service",
}


def images() -> dict[str, str]:
    return {
        name: f"harbor.ani.internal/ani/{name}@sha256:{index:064x}"
        for index, name in enumerate(sorted(SERVICES), start=1)
    }


class RenderServiceRuntimeObservabilityL3Test(unittest.TestCase):
    def render_documents(self) -> list[dict]:
        rendered = renderer.render_l3_fixture(
            namespace=NAMESPACE,
            version=VERSION,
            images=images(),
            image_pull_secret="ani-runtime-observability-registry",
        )
        return [document for document in yaml.safe_load_all(rendered) if document]

    def test_fixture_has_exact_namespace_scoped_object_matrix(self) -> None:
        documents = self.render_documents()
        kinds = [(document["kind"], document["metadata"]["name"]) for document in documents]

        self.assertIn(("Namespace", NAMESPACE), kinds)
        self.assertIn(("ConfigMap", "ani-service-observability-postgres-init"), kinds)
        self.assertIn(("ServiceAccount", "ani-service-observability-prometheus"), kinds)
        self.assertIn(("Role", "ani-service-observability-prometheus"), kinds)
        self.assertIn(("RoleBinding", "ani-service-observability-prometheus"), kinds)
        self.assertIn(("ConfigMap", "ani-service-observability-prometheus"), kinds)
        self.assertIn(("Deployment", "ani-service-observability-prometheus"), kinds)
        self.assertIn(("Service", "ani-service-observability-prometheus"), kinds)
        self.assertIn(("NetworkPolicy", "ani-service-observability-ingress"), kinds)
        for dependency in ("postgres", "nats", "redis"):
            self.assertIn(("Deployment", f"ani-service-observability-{dependency}"), kinds)
            self.assertIn(("Service", f"ani-service-observability-{dependency}"), kinds)
        for service in SERVICES:
            self.assertIn(("Deployment", service), kinds)
            self.assertIn(("Service", service), kinds)

        self.assertFalse(any(kind == "Secret" for kind, _ in kinds))
        self.assertEqual(29, len(documents))
        for document in documents:
            if document["kind"] != "Namespace":
                self.assertEqual(NAMESPACE, document["metadata"]["namespace"])

    def test_prometheus_uses_fixed_digest_and_exact_discovery_contract(self) -> None:
        documents = self.render_documents()
        prometheus = next(
            document
            for document in documents
            if document["kind"] == "Deployment"
            and document["metadata"]["name"] == "ani-service-observability-prometheus"
        )
        container = prometheus["spec"]["template"]["spec"]["containers"][0]
        self.assertEqual(renderer.PROMETHEUS_IMAGE, container["image"])

        config = next(
            document
            for document in documents
            if document["kind"] == "ConfigMap"
            and document["metadata"]["name"] == "ani-service-observability-prometheus"
        )
        payload = yaml.safe_load(config["data"]["prometheus.yml"])
        self.assertEqual("15s", payload["global"]["scrape_interval"])
        self.assertEqual(1, len(payload["scrape_configs"]))
        job = payload["scrape_configs"][0]
        self.assertEqual("ani-components", job["job_name"])
        self.assertEqual("15s", job["scrape_interval"])
        self.assertEqual("5s", job["scrape_timeout"])
        self.assertEqual(
            [NAMESPACE],
            job["kubernetes_sd_configs"][0]["namespaces"]["names"],
        )
        self.assertIn(
            "ani-gateway|auth-service|model-service|task-service|inference-service|tenant-service|metering-service",
            {item.get("regex") for item in job["relabel_configs"]},
        )

    def test_dependency_images_and_migrations_are_immutable_and_complete(self) -> None:
        documents = self.render_documents()
        deployments = {
            document["metadata"]["name"]: document
            for document in documents
            if document["kind"] == "Deployment"
        }
        expected = {
            "ani-service-observability-postgres": renderer.POSTGRES_IMAGE,
            "ani-service-observability-nats": renderer.NATS_IMAGE,
            "ani-service-observability-redis": renderer.REDIS_IMAGE,
        }
        for name, image in expected.items():
            self.assertEqual(
                image,
                deployments[name]["spec"]["template"]["spec"]["containers"][0]["image"],
            )
            self.assertIn("@sha256:", image)

        migrations = next(
            document
            for document in documents
            if document["kind"] == "ConfigMap"
            and document["metadata"]["name"] == "ani-service-observability-postgres-init"
        )
        expected_files = {path.name for path in (ROOT / "deploy/migrations").glob("*.sql")}
        self.assertEqual(expected_files, set(migrations["data"]))
        self.assertGreaterEqual(len(expected_files), 38)

    def test_stateful_dependency_entrypoints_receive_only_required_capabilities(self) -> None:
        documents = self.render_documents()
        deployments = {
            document["metadata"]["name"]: document
            for document in documents
            if document["kind"] == "Deployment"
        }
        expected_additions = {
            "ani-service-observability-postgres": [
                "CHOWN",
                "DAC_OVERRIDE",
                "FOWNER",
                "SETGID",
                "SETUID",
            ],
            "ani-service-observability-redis": ["CHOWN", "SETGID", "SETUID"],
        }

        for name, additions in expected_additions.items():
            security_context = deployments[name]["spec"]["template"]["spec"]["containers"][0][
                "securityContext"
            ]
            self.assertFalse(security_context["allowPrivilegeEscalation"])
            self.assertEqual(["ALL"], security_context["capabilities"]["drop"])
            self.assertEqual(additions, security_context["capabilities"]["add"])

    def test_workloads_have_run_id_pull_secret_and_no_external_exposure(self) -> None:
        documents = self.render_documents()
        for document in documents:
            self.assertNotEqual("Ingress", document["kind"])
            if document["kind"] == "Service":
                self.assertEqual("ClusterIP", document["spec"].get("type", "ClusterIP"))
        for deployment in (
            document
            for document in documents
            if document["kind"] == "Deployment" and document["metadata"]["name"] in SERVICES
        ):
            labels = deployment["spec"]["template"]["metadata"]["labels"]
            pod_spec = deployment["spec"]["template"]["spec"]
            self.assertEqual("0cedae8-l3", labels["ani.dev/run-id"])
            self.assertEqual(
                [{"name": "ani-runtime-observability-registry"}],
                pod_spec["imagePullSecrets"],
            )


if __name__ == "__main__":
    unittest.main()
