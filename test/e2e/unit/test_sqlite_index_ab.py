"""Safety contract for the scratch-only SQLite index comparison harness."""

from pathlib import Path
import json
import sqlite3
import subprocess
import sys
import tempfile
import unittest


SCRIPT = Path(__file__).resolve().parents[2] / "performance" / "sqlite_index_ab.py"


class SQLiteIndexABTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.directory = Path(self.temp.name)
        self.source = self.directory / "source.db"
        with sqlite3.connect(self.source) as conn:
            conn.executescript("""
                CREATE TABLE functions (id TEXT PRIMARY KEY);
                INSERT INTO functions VALUES ('fn-test');
                CREATE TABLE executions (
                    id TEXT PRIMARY KEY, function_id TEXT NOT NULL,
                    status TEXT, cold_start INTEGER, container_id TEXT,
                    duration_ms INTEGER, status_code INTEGER, error_message TEXT,
                    response_size INTEGER, started_at TEXT, finished_at TEXT,
                    trace_id TEXT, span_id TEXT, parent_span_id TEXT,
                    trigger TEXT, parent_function_id TEXT, is_outlier INTEGER,
                    baseline_p95_ms INTEGER,
                    FOREIGN KEY (function_id) REFERENCES functions(id)
                );
                CREATE INDEX idx_executions_trace_id ON executions(trace_id);
                CREATE INDEX idx_executions_parent_span_id ON executions(parent_span_id);
                CREATE INDEX idx_executions_started ON executions(started_at DESC);
                CREATE INDEX idx_executions_function ON executions(function_id, started_at DESC);
                CREATE INDEX idx_executions_status ON executions(status);
                CREATE INDEX idx_executions_trace_parent ON executions(trace_id, parent_span_id);
                INSERT INTO executions (id, function_id, status, started_at,
                                        trace_id, span_id)
                    VALUES ('original', 'fn-test', 'success', '2026-09-24 00:00:00',
                            'trace-original', 'original');
            """)

    def command(self, *extra):
        return [sys.executable, str(SCRIPT), "--db", str(self.source),
                "--workdir", str(self.directory),
                "--drop-index", "idx_executions_trace_id", *extra]

    def test_refuses_without_scratch_confirmation(self):
        result = subprocess.run(self.command(), capture_output=True, text=True, check=False)
        self.assertEqual(result.returncode, 2)
        self.assertIn("without --scratch", result.stderr)
        self.assertFalse(list(self.directory.glob("orva-index-ab-*")))

    def test_compares_copies_and_leaves_source_unchanged(self):
        result = subprocess.run(self.command("--scratch", "--batches", "1",
                                             "--batch-size", "2",
                                             "--read-repetitions", "1"),
                                capture_output=True, text=True, check=False)
        self.assertEqual(result.returncode, 0, result.stderr)
        output = json.loads(result.stdout)
        self.assertEqual(output["source_rows"], 1)
        self.assertEqual(output["candidate_dropped_indexes"], ["idx_executions_trace_id"])
        self.assertEqual(len(output["write"]["baseline"]["samples_ms"]), 1)
        self.assertEqual(len(output["write"]["candidate"]["samples_ms"]), 1)
        with sqlite3.connect(f"file:{self.source}?mode=ro", uri=True) as conn:
            self.assertEqual(conn.execute("SELECT COUNT(*) FROM executions").fetchone()[0], 1)
            self.assertIsNotNone(conn.execute(
                "SELECT name FROM sqlite_master WHERE name='idx_executions_trace_id'").fetchone())
        self.assertFalse(list(self.directory.glob("orva-index-ab-*")))


if __name__ == "__main__":
    unittest.main()
