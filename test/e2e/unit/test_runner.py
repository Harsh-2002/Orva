import importlib.util
import os
import tempfile
import textwrap
import unittest
from contextlib import redirect_stderr, redirect_stdout
from io import StringIO
from unittest import mock


RUNNER_PATH = os.path.join(os.path.dirname(__file__), "..", "run.py")
SPEC = importlib.util.spec_from_file_location("orva_e2e_runner", RUNNER_PATH)
runner = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(runner)


class RunnerResultTests(unittest.TestCase):
    def run_script(self, source):
        with tempfile.TemporaryDirectory() as tmp:
            path = os.path.join(tmp, "test_sample.py")
            with open(path, "w", encoding="utf-8") as handle:
                handle.write(textwrap.dedent(source))
            with redirect_stdout(StringIO()), redirect_stderr(StringIO()):
                return runner.run_module(path, dict(os.environ))

    def test_failed_assertion_is_classified_and_ansi_is_removed(self):
        result = self.run_script("""
            import sys
            print("\\x1b[31m✗\\x1b[0m sandbox failed (operation not permitted)")
            print("RESULT pass=5 fail=1")
            sys.exit(1)
        """)
        self.assertEqual(result["status"], "FAIL")
        self.assertEqual(result["fail"], 1)
        self.assertNotIn("\x1b", result["detail"])
        self.assertIn("operation not permitted", result["detail"])

    def test_crash_without_result_gets_one_failure_and_stderr(self):
        result = self.run_script("""
            import sys
            print("runner exploded", file=sys.stderr)
            sys.exit(2)
        """)
        self.assertEqual(result["status"], "FAIL")
        self.assertEqual(result["fail"], 1)
        self.assertIn("runner exploded", result["detail"])

    def test_explicit_skip_is_not_a_failure(self):
        result = self.run_script("""
            import sys
            print("RESULT pass=0 fail=0 skip=1")
            sys.exit(3)
        """)
        self.assertEqual(result["status"], "SKIP")
        self.assertEqual(result["fail"], 0)

    def test_missing_result_trailer_cannot_pass(self):
        result = self.run_script("""
            print("no machine-readable result")
        """)
        self.assertEqual(result["status"], "FAIL")
        self.assertEqual(result["fail"], 1)
        self.assertIn("missing RESULT trailer", result["detail"])

    def test_prefixed_result_text_cannot_impersonate_trailer(self):
        result = self.run_script("""
            print("debug: RESULT pass=99 fail=0")
        """)
        self.assertEqual(result["status"], "FAIL")
        self.assertIn("missing RESULT trailer", result["detail"])

    def test_result_must_be_final_nonempty_trailer(self):
        result = self.run_script("""
            print("RESULT pass=1 fail=0")
            print("late failure after result")
        """)
        self.assertEqual(result["status"], "FAIL")
        self.assertIn("missing RESULT trailer", result["detail"])

    def test_zero_check_module_cannot_pass(self):
        result = self.run_script("""
            print("RESULT pass=0 fail=0")
        """)
        self.assertEqual(result["status"], "FAIL")
        self.assertEqual(result["fail"], 1)
        self.assertIn("at least one passed check", result["detail"])

    def test_skip_zero_and_exit_three_is_a_protocol_failure(self):
        result = self.run_script("""
            import sys
            print("RESULT pass=0 fail=0 skip=0")
            sys.exit(3)
        """)
        self.assertEqual(result["status"], "FAIL")
        self.assertEqual(result["fail"], 1)
        self.assertIn("exit 3 requires", result["detail"])

    def test_skip_with_failed_checks_is_a_protocol_failure(self):
        result = self.run_script("""
            import sys
            print("RESULT pass=1 fail=1 skip=1")
            sys.exit(3)
        """)
        self.assertEqual(result["status"], "FAIL")
        self.assertEqual(result["fail"], 1)
        self.assertIn("skip result requires", result["detail"])

    def test_timeout_becomes_module_failure_instead_of_aborting_suite(self):
        expired = runner.subprocess.TimeoutExpired(["python", "test.py"], 900)
        with mock.patch.object(runner.subprocess, "run", side_effect=expired):
            with redirect_stdout(StringIO()), redirect_stderr(StringIO()):
                result = runner.run_module("test_timeout.py", dict(os.environ))
        self.assertEqual(result["status"], "FAIL")
        self.assertEqual(result["rc"], 124)
        self.assertIn("timed out", result["detail"])

    def test_checklist_persists_failure_detail(self):
        results = [{
            "name": "test_sample.py", "status": "FAIL", "pass": 2,
            "fail": 1, "rc": 1, "detail": "sandbox failed",
        }]
        with tempfile.TemporaryDirectory() as tmp:
            original_here = runner.HERE
            runner.HERE = tmp
            try:
                self.assertEqual(runner.write_checklist(results, "unit target"), 1)
                with open(os.path.join(tmp, "CHECKLIST.md"), encoding="utf-8") as handle:
                    checklist = handle.read()
            finally:
                runner.HERE = original_here
        self.assertIn("## Failure details", checklist)
        self.assertIn("`test_sample.py`: sandbox failed", checklist)


if __name__ == "__main__":
    unittest.main()
