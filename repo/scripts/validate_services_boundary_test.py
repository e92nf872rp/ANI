#!/usr/bin/env python3
"""Tests for Services boundary baseline validation."""

from __future__ import annotations

import tempfile
import textwrap
import unittest
from pathlib import Path

import validate_services_boundary as guard


class ServicesBoundaryValidationTest(unittest.TestCase):
    def test_repo_baseline_is_warn_only(self) -> None:
        result = guard.validate_workspace(guard.ROOT, run_spec_split=False)

        self.assertEqual(result.error_count, 0)
        self.assertEqual(result.warning_count, 3)

    def test_unregistered_core_internal_go_import_fails(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            self._write_fixture_layout(root)
            self._write_baseline(
                root,
                """
                version: 1
                exceptions: []
                """,
            )
            (root / "services" / "model-service").mkdir(parents=True, exist_ok=True)
            (root / "services" / "model-service" / "main.go").write_text(
                textwrap.dedent(
                    """\
                    package main

                    import "github.com/kubercloud/ani/pkg/bootstrap"

                    func main() {}
                    """
                ),
                encoding="utf-8",
            )

            result = guard.validate_workspace(root, run_spec_split=False)

        self.assertEqual(result.warning_count, 0)
        self.assertIn("services/model-service/main.go", "\n".join(result.errors))
        self.assertIn("github.com/kubercloud/ani/pkg/bootstrap", "\n".join(result.errors))

    def test_unregistered_provider_sdk_import_fails(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            self._write_fixture_layout(root)
            self._write_baseline(
                root,
                """
                version: 1
                exceptions: []
                """,
            )
            (root / "ai" / "rag-engine" / "app" / "core").mkdir(parents=True, exist_ok=True)
            (root / "ai" / "rag-engine" / "app" / "core" / "milvus.py").write_text(
                "from pymilvus import connections\n",
                encoding="utf-8",
            )

            result = guard.validate_workspace(root, run_spec_split=False)

        self.assertEqual(result.warning_count, 0)
        self.assertIn("ai/rag-engine/app/core/milvus.py", "\n".join(result.errors))
        self.assertIn("pymilvus", "\n".join(result.errors))

    def test_provider_sdk_import_anywhere_under_ai_fails(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            self._write_fixture_layout(root)
            self._write_baseline(
                root,
                """
                version: 1
                exceptions: []
                """,
            )
            (root / "ai" / "experimental-worker").mkdir(parents=True, exist_ok=True)
            (root / "ai" / "experimental-worker" / "provider.py").write_text(
                "import pymilvus\n",
                encoding="utf-8",
            )

            result = guard.validate_workspace(root, run_spec_split=False)

        self.assertEqual(result.warning_count, 0)
        self.assertIn("ai/experimental-worker/provider.py", "\n".join(result.errors))
        self.assertIn("provider_sdk_python_import", "\n".join(result.errors))
        self.assertIn("pymilvus", "\n".join(result.errors))

    def test_unknown_immediate_services_root_fails_closed(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            self._write_fixture_layout(root)
            self._write_baseline(
                root,
                """
                version: 1
                exceptions: []
                """,
            )
            (root / "services" / "new-service").mkdir(parents=True, exist_ok=True)

            result = guard.validate_workspace(root, run_spec_split=False)

        self.assertEqual(result.warning_count, 0)
        self.assertIn("services/new-service", "\n".join(result.errors))
        self.assertIn("unknown_service_root", "\n".join(result.errors))

    def test_docs_only_services_root_rejects_source_files(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            self._write_fixture_layout(root)
            self._write_baseline(
                root,
                """
                version: 1
                exceptions: []
                """,
            )
            (root / "services" / "tasks").mkdir(parents=True, exist_ok=True)
            (root / "services" / "tasks" / "helper.py").write_text(
                "print('not documentation')\n",
                encoding="utf-8",
            )

            result = guard.validate_workspace(root, run_spec_split=False)

        self.assertEqual(result.warning_count, 0)
        self.assertIn("services/tasks/helper.py", "\n".join(result.errors))
        self.assertIn("docs_only_service_source_file", "\n".join(result.errors))

    def test_exact_registered_exception_is_warn_only(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            self._write_fixture_layout(root)
            self._write_baseline(
                root,
                """
                version: 1
                exceptions:
                  - path: services/model-service/main.go
                    rule: core_internal_go_import
                    import: github.com/kubercloud/ani/pkg/bootstrap
                    status: accepted_baseline
                    owner: ANI Services 外部产品团队
                    reason: 保留冻结前 wiring 事实
                    disposition: 受控解冻时迁移为 Services 自有启动装配
                """,
            )
            (root / "services" / "model-service").mkdir(parents=True, exist_ok=True)
            (root / "services" / "model-service" / "main.go").write_text(
                textwrap.dedent(
                    """\
                    package main

                    import "github.com/kubercloud/ani/pkg/bootstrap"

                    func main() {}
                    """
                ),
                encoding="utf-8",
            )

            result = guard.validate_workspace(root, run_spec_split=False)

        self.assertEqual(result.error_count, 0)
        self.assertEqual(result.warning_count, 1)
        self.assertIn("accepted baseline", result.warnings[0])

    def test_cross_service_internal_import_fails(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            self._write_fixture_layout(root)
            self._write_baseline(
                root,
                """
                version: 1
                exceptions: []
                """,
            )
            (root / "services" / "model-service").mkdir(parents=True, exist_ok=True)
            (root / "services" / "model-service" / "main.go").write_text(
                textwrap.dedent(
                    """\
                    package main

                    import "github.com/kubercloud/ani/services/kb-service/internal/config"

                    func main() {
                        _ = config.Load
                    }
                    """
                ),
                encoding="utf-8",
            )

            result = guard.validate_workspace(root, run_spec_split=False)

        self.assertEqual(result.warning_count, 0)
        self.assertIn("cross_service_internal_go_import", "\n".join(result.errors))
        self.assertIn("services/kb-service/internal/config", "\n".join(result.errors))

    def test_empty_reason_on_exact_path_is_rejected(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            self._write_fixture_layout(root)
            (root / "services" / "model-service").mkdir(parents=True, exist_ok=True)
            (root / "services" / "model-service" / "main.go").write_text("package main\n", encoding="utf-8")
            self._write_baseline(
                root,
                """
                version: 1
                exceptions:
                  - path: services/model-service/main.go
                    rule: core_internal_go_import
                    import: github.com/kubercloud/ani/pkg/bootstrap
                    status: accepted_baseline
                    owner: ANI Services 外部产品团队
                    reason: ""
                    disposition: 受控解冻时迁移为 Services 自有启动装配
                """,
            )

            with self.assertRaises(ValueError) as raised:
                guard.load_baseline(root / "architecture" / "services-boundary-baseline.yaml", root)

        self.assertIn("requires rule, import, status, owner, reason, disposition", str(raised.exception))

    def test_wildcard_path_is_rejected(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            self._write_fixture_layout(root)
            self._write_baseline(
                root,
                """
                version: 1
                exceptions:
                  - path: services/model-service/*.go
                    rule: core_internal_go_import
                    import: github.com/kubercloud/ani/pkg/bootstrap
                    status: accepted_baseline
                    owner: ANI Services 外部产品团队
                    reason: ""
                    disposition: 受控解冻时迁移为 Services 自有启动装配
                """,
            )

            with self.assertRaises(ValueError) as raised:
                guard.load_baseline(root / "architecture" / "services-boundary-baseline.yaml", root)

        self.assertIn("exact file path", str(raised.exception))

    @staticmethod
    def _write_fixture_layout(root: Path) -> None:
        (root / "architecture").mkdir(parents=True, exist_ok=True)

    @staticmethod
    def _write_baseline(root: Path, contents: str) -> None:
        (root / "architecture" / "services-boundary-baseline.yaml").write_text(
            textwrap.dedent(contents).strip() + "\n",
            encoding="utf-8",
        )


if __name__ == "__main__":
    unittest.main()
