#!/usr/bin/env python3
"""Tests for Sprint 13 GPU inventory live gate contract."""

from __future__ import annotations

import tempfile
import unittest
from copy import deepcopy
from pathlib import Path
from unittest.mock import patch

import validate_gpu_inventory_live_gate as gate


class GPUInventoryLiveGateTest(unittest.TestCase):
    def test_contract_gate_defines_gpu_inventory_and_occupancy_checks(self) -> None:
        document = gate.load_gate(gate.DEFAULT_GATE)

        gate.validate_contract(document)

        check_ids = {check["id"] for check in document["live_checks"]}
        self.assertIn("nvidia-device-plugin-node-capacity", check_ids)
        self.assertIn("core-gpu-inventory-list", check_ids)
        self.assertIn("core-gpu-occupancy-get", check_ids)
        self.assertIn("dcgm-exporter-metrics-readable", check_ids)

    def test_contract_gate_rejects_missing_check(self) -> None:
        document = deepcopy(gate.load_gate(gate.DEFAULT_GATE))
        document["live_checks"] = [check for check in document["live_checks"] if check["id"] != "core-gpu-occupancy-get"]

        with self.assertRaises(SystemExit) as raised:
            gate.validate_contract(document)

        self.assertIn("missing live checks: core-gpu-occupancy-get", str(raised.exception))

    def test_contract_gate_rejects_production_like_status(self) -> None:
        document = deepcopy(gate.load_gate(gate.DEFAULT_GATE))
        document["status"] = "production_like"

        with self.assertRaises(SystemExit) as raised:
            gate.validate_contract(document)

        self.assertIn("status must be contract or live", str(raised.exception))

    def test_cli_reports_missing_gate_path_without_traceback(self) -> None:
        missing_gate = Path(tempfile.gettempdir()) / "ani-missing-gpu-inventory-live-gate.yaml"
        with (
            patch("sys.argv", ["validate_gpu_inventory_live_gate.py", "--gate", str(missing_gate)]),
            patch.object(gate, "validate_docs"),
        ):
            with self.assertRaises(SystemExit) as raised:
                gate.main()

        self.assertIn(f"missing {missing_gate}", str(raised.exception))

    def test_cli_validates_docs(self) -> None:
        document = gate.load_gate(gate.DEFAULT_GATE)
        with (
            patch("sys.argv", ["validate_gpu_inventory_live_gate.py"]),
            patch.object(gate, "load_gate", return_value=document),
            patch.object(gate, "validate_docs") as validate_docs,
        ):
            gate.main()

        validate_docs.assert_called_once()


if __name__ == "__main__":
    unittest.main()
