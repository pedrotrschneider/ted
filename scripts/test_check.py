"""Exercise the check entry point with an isolated tool PATH, without a database."""

import os
from pathlib import Path
import subprocess
import tempfile
import unittest


CHECK = Path(__file__).with_name("check.sh").resolve()


class CheckPrerequisitesTests(unittest.TestCase):
    def run_check(self, mode, available):
        with tempfile.TemporaryDirectory(prefix="ted-check-tools-") as directory:
            root = Path(directory)
            trace = root / "calls"
            for tool in available:
                executable = root / tool
                executable.write_text(
                    '#!/bin/bash\nprintf "%s\\n" "$*" >> "$TRACE_FILE"\n',
                    encoding="utf-8",
                )
                executable.chmod(0o755)
            result = subprocess.run(
                ["/bin/bash", str(CHECK), mode],
                env={**os.environ, "PATH": str(root), "TRACE_FILE": str(trace)},
                capture_output=True,
                text=True,
                check=False,
            )
            return result, trace.read_text() if trace.exists() else ""

    def test_missing_media_tools_stop_both_test_modes_before_go(self):
        for mode in ("behavior", "race"):
            for missing in ("ffmpeg", "ffprobe"):
                with self.subTest(mode=mode, missing=missing):
                    result, calls = self.run_check(mode, {"go", "ffmpeg", "ffprobe"} - {missing})
                    self.assertEqual(result.returncode, 1)
                    self.assertIn(missing, result.stderr)
                    self.assertEqual(calls, "", "Go must not start an incomplete test environment")

    def test_ready_environment_keeps_database_serialization_and_independent_race_mode(self):
        for mode, expected in (
            ("behavior", "test -p 1 -count=1 ./...\n"),
            ("race", "test -p 1 -race -count=1 ./...\n"),
        ):
            with self.subTest(mode=mode):
                result, calls = self.run_check(mode, {"go", "ffmpeg", "ffprobe"})
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(calls, expected)

    def test_vet_remains_independent_of_media_tools(self):
        result, calls = self.run_check("vet", {"go"})
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(calls, "vet ./...\n")


if __name__ == "__main__":
    unittest.main()
