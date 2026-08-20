#!/usr/bin/env python3
"""Tests for the retired inference.v1 / operator control-plane guard."""

from __future__ import annotations

import tempfile
import textwrap
import unittest
from pathlib import Path
from unittest import mock

import validate_inference_legacy_control_plane as guard


class InferenceLegacyControlPlaneTest(unittest.TestCase):
    def test_repo_current_tree_is_retired(self) -> None:
        guard.main()

    def test_new_old_proto_import_fails(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            caller = root / "services" / "ani-gateway" / "internal" / "router" / "legacy.go"
            caller.parent.mkdir(parents=True)
            caller.write_text(
                textwrap.dedent(
                    """\
                    package router

                    import inferencev1 "github.com/kubercloud/ani/pkg/generated/pb/inference/v1"
                    """
                ),
                encoding="utf-8",
            )
            with mock.patch.object(guard, "ROOT", root):
                with mock.patch.object(guard, "iter_go_files", return_value=[caller]):
                    with self.assertRaises(SystemExit) as raised:
                        guard.validate_no_new_callers()
        self.assertIn("new callers of retired inference.v1 proto", str(raised.exception))

    def test_gateway_must_not_import_inference_service(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            caller = root / "services" / "ani-gateway" / "internal" / "router" / "bad.go"
            caller.parent.mkdir(parents=True)
            caller.write_text(
                textwrap.dedent(
                    """\
                    package router

                    import "github.com/kubercloud/ani/services/inference-service/internal/service"
                    """
                ),
                encoding="utf-8",
            )
            with mock.patch.object(guard, "ROOT", root), mock.patch.object(guard, "GATEWAY_ROOT", root / "services" / "ani-gateway"):
                with self.assertRaises(SystemExit) as raised:
                    guard.validate_gateway_wiring()
        self.assertIn("imports inference-service implementation", str(raised.exception))


if __name__ == "__main__":
    unittest.main()
