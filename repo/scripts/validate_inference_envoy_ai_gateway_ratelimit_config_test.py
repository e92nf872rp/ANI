#!/usr/bin/env python3
import copy
import unittest

import validate_inference_envoy_ai_gateway_ratelimit_config as config


class RateLimitConfigTests(unittest.TestCase):
    def document(self):
        return config.yaml.safe_load(config.DEFAULT_CONFIG.read_text(encoding="utf-8"))

    def test_repo_fragment_is_valid_and_secret_backed(self):
        document = self.document()
        config.validate(document)
        self.assertNotIn("redis://", str(document).lower())

    def test_rejects_plaintext_or_wrong_secret_reference(self):
        for mutation in ("url", "secret", "namespace"):
            with self.subTest(mutation=mutation):
                document = self.document()
                if mutation == "url":
                    embedded = config.yaml.safe_load(document["data"]["envoy-gateway.yaml"])
                    embedded["rateLimit"]["backend"]["redis"] = {"url": "redis://redis:6379"}
                    document["data"]["envoy-gateway.yaml"] = config.yaml.safe_dump(embedded, sort_keys=False)
                elif mutation == "secret":
                    embedded = config.yaml.safe_load(document["data"]["envoy-gateway.yaml"])
                    embedded["rateLimit"]["backend"]["redis"]["urlRef"]["secretKeyRef"]["name"] = "other"
                    document["data"]["envoy-gateway.yaml"] = config.yaml.safe_dump(embedded, sort_keys=False)
                else:
                    document["metadata"]["namespace"] = "ani-aigw"
                with self.assertRaises(SystemExit):
                    config.validate(document)


if __name__ == "__main__":
    unittest.main()
