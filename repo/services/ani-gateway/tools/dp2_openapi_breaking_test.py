#!/usr/bin/env python3

from __future__ import annotations

import copy
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import dp2_openapi_breaking as breaking


def baseline() -> dict:
    return {
        "openapi": "3.1.0",
        "components": {
            "securitySchemes": {
                "BearerAuth": {"type": "http", "scheme": "bearer"},
                "ApiKeyAuth": {"type": "apiKey", "in": "header", "name": "X-API-Key"},
            },
            "schemas": {
                "Thing": {
                    "type": "object",
                    "required": ["id", "name"],
                    "properties": {"id": {"type": "string"}, "name": {"type": "string"}},
                }
            },
        },
        "security": [{"BearerAuth": []}, {"ApiKeyAuth": []}],
        "paths": {
            "/things": {
                "get": {
                    "operationId": "listThings",
                    "responses": {"200": {"description": "ok"}},
                }
            },
            "/things/{thing_id}": {
                "get": {
                    "operationId": "getThing",
                    "responses": {
                        "200": {
                            "description": "ok",
                            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Thing"}}},
                        }
                    },
                }
            },
        },
    }


class BreakingReportTest(unittest.TestCase):
    def test_reports_removed_renamed_security_schema_and_semantic_changes(self) -> None:
        target = copy.deepcopy(baseline())
        del target["paths"]["/things"]
        operation = target["paths"]["/things/{thing_id}"]["get"]
        operation["operationId"] = "readThing"
        operation["x-ani-owner"] = "core-control"
        operation["x-ani-auth-classification"] = "authorized"
        target["security"] = [{"BearerAuth": []}]
        del target["components"]["schemas"]["Thing"]["properties"]["name"]
        target["components"]["schemas"]["NewThing"] = {"type": "object"}

        report = breaking.compare_specs(baseline(), target)

        self.assertEqual(report["summary"]["removed_operations"], 1)
        self.assertEqual(report["summary"]["operation_id_changes"], 1)
        self.assertEqual(report["summary"]["security_changes"], 1)
        self.assertEqual(report["summary"]["changed_schemas"], 1)
        self.assertEqual(report["summary"]["added_schemas"], 1)
        self.assertEqual(report["removed_operations"][0]["operation_id"], "listThings")
        self.assertEqual(report["operation_id_changes"][0]["after"], "readThing")
        self.assertEqual(report["changed_schemas"][0]["schema"], "Thing")
        self.assertIn("/properties/name", {row["pointer"] for row in report["changed_schemas"][0]["changes"]})
        self.assertEqual(report["semantic_operation_changes"][0]["operation_id"], "readThing")

    def test_is_deterministic(self) -> None:
        first = breaking.compare_specs(baseline(), baseline())
        second = breaking.compare_specs(copy.deepcopy(baseline()), copy.deepcopy(baseline()))
        self.assertEqual(first, second)
        self.assertEqual(first["summary"]["breaking_changes"], 0)

    def test_reports_shared_component_contract_changes(self) -> None:
        source = baseline()
        source["components"]["responses"] = {
            "Unavailable": {
                "description": "dependency unavailable (code=UNAVAILABLE)",
                "content": {
                    "application/json": {
                        "schema": {"$ref": "#/components/schemas/Thing"}
                    }
                },
            }
        }
        target = copy.deepcopy(source)
        target["components"]["responses"]["Unavailable"]["description"] = (
            "IAM unavailable (code=IAM_UNAVAILABLE)"
        )
        target["components"]["responses"]["Unavailable"]["x-ani-error-codes"] = [
            "IAM_UNAVAILABLE"
        ]
        target["components"]["parameters"] = {
            "IdempotencyKey": {
                "name": "Idempotency-Key",
                "in": "header",
                "required": True,
                "schema": {"type": "string"},
            }
        }

        report = breaking.compare_specs(source, target)

        pointers = {row["pointer"] for row in report["component_contract_changes"]}
        self.assertIn("/components/parameters", pointers)
        self.assertIn(
            "/components/responses/Unavailable/description",
            pointers,
        )
        self.assertIn(
            "/components/responses/Unavailable/x-ani-error-codes",
            pointers,
        )
        self.assertEqual(report["summary"]["component_contract_changes"], 3)

    def test_reports_non_documentary_top_level_contract_changes(self) -> None:
        target = copy.deepcopy(baseline())
        target["servers"] = [{"url": "https://{host}/api/v2"}]

        report = breaking.compare_specs(baseline(), target)

        self.assertEqual(
            {row["pointer"] for row in report["document_contract_changes"]},
            {"/servers"},
        )
        self.assertEqual(report["summary"]["document_contract_changes"], 1)


if __name__ == "__main__":
    unittest.main()
