import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import validate_generated_idempotence as validator


class ValidateGeneratedIdempotenceTest(unittest.TestCase):
    def test_passes_when_command_leaves_generated_files_unchanged(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            generated = root / "generated.txt"
            generated.write_text("stable\n", encoding="utf-8")

            result = validator.validate(
                root,
                [Path("generated.txt")],
                [sys.executable, "-c", "pass"],
            )

            self.assertEqual([], result.changed)
            self.assertEqual(0, result.command_returncode)

    def test_reports_content_changed_by_generator(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            generated = root / "generated.txt"
            generated.write_text("before\n", encoding="utf-8")

            result = validator.validate(
                root,
                [Path("generated.txt")],
                [
                    sys.executable,
                    "-c",
                    "from pathlib import Path; Path('generated.txt').write_text('after\\n')",
                ],
            )

            self.assertEqual(["generated.txt"], result.changed)
            self.assertEqual(0, result.command_returncode)

    def test_reports_new_and_deleted_generated_files(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            generated = root / "generated"
            generated.mkdir()
            (generated / "old.txt").write_text("old\n", encoding="utf-8")

            result = validator.validate(
                root,
                [Path("generated")],
                [
                    sys.executable,
                    "-c",
                    (
                        "from pathlib import Path; "
                        "Path('generated/old.txt').unlink(); "
                        "Path('generated/new.txt').write_text('new\\n')"
                    ),
                ],
            )

            self.assertEqual(
                ["generated/new.txt", "generated/old.txt"],
                result.changed,
            )

    def test_ignores_python_bytecode_and_cache_directories(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            generated = root / "generated"
            generated.mkdir()
            (generated / "source.py").write_text("VALUE = 1\n", encoding="utf-8")

            result = validator.validate(
                root,
                [Path("generated")],
                [
                    sys.executable,
                    "-c",
                    (
                        "from pathlib import Path; "
                        "Path('generated/__pycache__').mkdir(); "
                        "Path('generated/__pycache__/source.cpython-314.pyc').write_bytes(b'cache')"
                    ),
                ],
            )

            self.assertEqual([], result.changed)

    def test_ignores_java_compile_output_created_by_sdk_smoke(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            generated = root / "generated"
            generated.mkdir()

            result = validator.validate(
                root,
                [Path("generated")],
                [
                    sys.executable,
                    "-c",
                    (
                        "from pathlib import Path; "
                        "path = Path('generated/java/build/classes/Smoke.class'); "
                        "path.parent.mkdir(parents=True); path.write_bytes(b'class')"
                    ),
                ],
            )

            self.assertEqual([], result.changed)

    def test_preserves_generator_failure_code(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            generated = root / "generated.txt"
            generated.write_text("stable\n", encoding="utf-8")

            result = validator.validate(
                root,
                [Path("generated.txt")],
                [sys.executable, "-c", "raise SystemExit(7)"],
            )

            self.assertEqual(7, result.command_returncode)


if __name__ == "__main__":
    unittest.main()
