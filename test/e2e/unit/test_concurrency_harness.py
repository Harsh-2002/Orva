"""Offline safety/correctness checks for independent concurrency bursts."""
import contextlib
import importlib.util
import io
from pathlib import Path
from types import SimpleNamespace
import threading
import unittest
from unittest.mock import Mock, patch


SCRIPT = Path(__file__).resolve().parents[2] / "performance" / "concurrency.py"
SPEC = importlib.util.spec_from_file_location("concurrency_harness", SCRIPT)
h = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(h)


class ConcurrencyHarnessTest(unittest.TestCase):
    def setUp(self):
        network = patch.object(h.urllib.request, "urlopen", side_effect=AssertionError("network forbidden"))
        self.network = network.start()
        self.addCleanup(network.stop)
        self.args = SimpleNamespace(scratch=True, label="same label", url="http://scratch.invalid")

    def test_refusal_before_any_network_or_target_creation(self):
        with patch.object(h, "Target") as target, contextlib.redirect_stderr(io.StringIO()):
            self.assertEqual(h.main(["--api-key", "dummy"]), 2)
        target.assert_not_called()
        t = Mock()
        self.args.scratch = False
        with self.assertRaisesRegex(h.HarnessError, "without --scratch"):
            h.run(t, self.args)
        self.assertEqual(t.mock_calls, [])
        self.network.assert_not_called()

    def test_ok_rejects_status_zero_and_every_non_2xx_without_payload(self):
        t = h.Target("http://scratch.invalid", "dummy")
        for code in (0, 101, 199, 300, 401, 500):
            with self.subTest(code=code), patch.object(t, "call", return_value=(code, "SECRET")):
                with self.assertRaises(h.HarnessError) as caught:
                    t.ok("GET", "/safe")
                self.assertNotIn("SECRET", str(caught.exception))
        for code in (200, 201, 204, 299):
            with patch.object(t, "call", return_value=(code, None)):
                self.assertIsNone(t.ok("DELETE", "/safe"))

    def test_fixture_specific_bodies_and_wrong_200(self):
        t = Mock()
        fixtures = [h.python_fixture(True), h.python_fixture(False),
                    h.node_fixture(True), h.node_fixture(False)]
        for fx in fixtures:
            with self.subTest(expected=fx["expected"]):
                t.call.return_value = (200, fx["expected"])
                self.assertTrue(h.one(t, "fn", fx["expected"])["ok"])
                t.call.return_value = (200, {"secret": "do not print"})
                result = h.one(t, "fn", fx["expected"])
                self.assertFalse(result["ok"])
                self.assertTrue(result["body_mismatch"])
                report = h.summarise("wrong", [result], 100, 0)
                self.assertEqual(report["validity"], "INVALID")
                self.assertEqual(report["errors"], 1)
                self.assertNotIn("do not print", str(report))
        t.call.return_value = (200, {"ok": 1})
        self.assertFalse(h.one(t, "fn", {"ok": True})["ok"])
        t.call.return_value = (0, {"ok": True})
        self.assertFalse(h.one(t, "fn", {"ok": True})["ok"])

    def failed_creation_run(self, delete_code):
        t = h.Target("http://scratch.invalid", "dummy")
        replies = [(201, {"id": "first"}), (503, "SECRET creation"),
                   (delete_code, "SECRET cleanup")]
        with patch.object(t, "call", side_effect=replies) as call, \
                patch.object(h, "deploy"), patch.object(h, "wait_active"):
            report = h.run(t, self.args)
        self.assertEqual(call.call_args_list[-1].args[:2], ("DELETE", "/api/v1/functions/first"))
        self.assertIn("HTTP 503", report["harness_errors"][0])
        self.assertEqual(report["validity"], "INVALID")
        self.assertNotIn("SECRET", str(report))
        return report, call

    def test_second_create_failure_cleans_first(self):
        report, _ = self.failed_creation_run(204)
        self.assertEqual(report["cleanup_errors"], [])

    def test_failed_delete_reported_without_masking_original(self):
        report, _ = self.failed_creation_run(500)
        self.assertEqual(len(report["harness_errors"]), 1)
        self.assertIn("HTTP 500", report["cleanup_errors"][0])
        rendered = h.render(report)
        self.assertIn("validity=INVALID", rendered)
        self.assertIn("harness_errors=1 cleanup_errors=1", rendered)
        self.assertIn("no sustained capacity or dynamic-load proof", rendered)
        self.assertNotIn("SECRET", rendered)

    def test_deploy_failure_cleanup_and_unique_run_names(self):
        names = []
        for _ in range(2):
            t = Mock()
            t.ok.return_value = {"id": "first"}
            with patch.object(h, "deploy", side_effect=h.HarnessError("deploy failed")):
                report = h.run(t, self.args)
            names.append(t.ok.call_args_list[0].args[2]["name"])
            self.assertEqual(t.ok.call_args_list[-1].args, ("DELETE", "/api/v1/functions/first"))
            self.assertEqual(report["harness_errors"], ["deploy failed"])
        self.assertNotEqual(*names)

    def test_cleanup_continues_after_failed_delete(self):
        t = Mock()
        t.ok.side_effect = [{"id": "first"}, {"id": "second"}, h.HarnessError("delete failed"), None]
        with patch.object(h, "deploy"), patch.object(h, "wait_active"), \
                patch.object(h, "warm_pool", side_effect=h.HarnessError("warm failed")):
            report = h.run(t, self.args)
        self.assertEqual([c.args[1] for c in t.ok.call_args_list[-2:]],
                         ["/api/v1/functions/first", "/api/v1/functions/second"])
        self.assertEqual(report["harness_errors"], ["warm failed"])
        self.assertEqual(report["cleanup_errors"], ["delete failed"])

    def test_burst_65_and_invalid_sizes_refused_before_executor_or_calls(self):
        t = Mock()
        for total in (0, -1, 65, 10000, 1.5):
            with self.subTest(total=total), patch.object(h, "ThreadPoolExecutor") as executor:
                with self.assertRaises(h.HarnessError):
                    h.burst(t, ["fn"], total, {"fn": {"ok": True}})
                executor.assert_not_called()
        t.call.assert_not_called()

    def test_max_burst_completes_and_barrier_has_timeout(self):
        t = Mock()
        t.call.return_value = (200, {"ok": True})
        results, wall = h.burst(t, ["fn"], 64, {"fn": {"ok": True}})
        self.assertEqual(len(results), 64)
        self.assertTrue(all(r["ok"] for r in results))
        self.assertGreater(wall, 0)
        with patch.object(h.threading, "Barrier") as barrier:
            barrier.return_value.wait.side_effect = threading.BrokenBarrierError
            with self.assertRaisesRegex(h.HarnessError, "barrier timed out"):
                h.burst(t, ["fn"], 1, {"fn": {"ok": True}})
            barrier.return_value.wait.assert_called_once_with(timeout=h.BARRIER_TIMEOUT)

    def test_classification_checks_every_pool_and_warm_zero_delta(self):
        before = {"a": {"spawned": 2}}
        after = {"a": {"spawned": 2}}
        self.assertEqual(h.classify("warm", ["a"], before, after)[1], [])
        self.assertTrue(h.classify("warm", ["a"], before, {"a": {"spawned": 3}})[1])
        self.assertTrue(h.classify("warm", ["a"], {}, after)[1])
        self.assertTrue(h.classify("warm", ["a"], before, {})[1])
        after = {"a": {"spawned": 1}, "b": {"spawned": 1}}
        self.assertEqual(h.classify("cold", ["a", "b"], {}, after)[1], [])
        self.assertTrue(h.classify("cold", ["a", "b"], {}, {"a": after["a"]})[1])
        self.assertTrue(h.classify("cold", ["a"], before, after)[1])

    def test_measure_marks_warm_spawn_invalid_and_cold_reuse_explicit(self):
        result = {"ms": 100, "code": 200, "ok": True, "body_mismatch": False}
        for mode, before, after, validity in (
                ("warm", {"a": {"spawned": 1}}, {"a": {"spawned": 2}}, "INVALID"),
                ("cold", {}, {"a": {"spawned": 1}}, "VALID")):
            with patch.object(h, "pools", side_effect=[before, after]), \
                    patch.object(h, "burst", return_value=([result] * 16, 100)), patch.object(h, "retire"):
                report = h.measure(Mock(), "burst", ["a"], 16, {}, mode)
            self.assertEqual(report["validity"], validity)
            if mode == "cold":
                self.assertIn("reused", report["pool_semantics"])
                self.assertEqual(report["spawned_by_function"], {"a": 1})

    def test_nearest_rank_and_small_sample_p99(self):
        self.assertIsNone(h.pct([], 99))
        self.assertEqual(h.pct([1, 2, 3, 4], 50), 2)
        self.assertEqual(h.pct(range(1, 101), 95), 95)
        self.assertEqual(h.pct(range(1, 101), 99), 99)
        result = {"ms": 10000, "code": 200, "ok": True, "body_mismatch": False}
        report = h.summarise("slow but valid", [result], 10000, 0)
        self.assertIsNone(report["p99_ms"])
        self.assertTrue(report["warnings"])
        self.assertNotIn("rps", report)
        self.assertEqual(report["burst_completion_rate_rps"], 0.1)
        self.assertEqual(report["validity"], "VALID")
        self.assertEqual(h.summarise("large", [result] * 100, 10000, 0)["p99_ms"], 10000)

    def test_main_nonzero_for_invalid_report_not_latency(self):
        for validity, expected_exit in (("INVALID", 1), ("VALID", 0)):
            with patch.object(h, "Target"), patch.object(h, "run", return_value={"validity": validity}), \
                    patch.object(h, "render", return_value="report"), contextlib.redirect_stdout(io.StringIO()):
                self.assertEqual(h.main(["--scratch", "--api-key", "dummy"]), expected_exit)


if __name__ == "__main__":
    unittest.main()
