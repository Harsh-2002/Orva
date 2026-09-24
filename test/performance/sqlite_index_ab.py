#!/usr/bin/env python3
"""Compare execution-index write/read cost on consistent disposable DB copies.

This never modifies the source database. It requires --scratch, creates two
SQLite-backup copies in a temporary directory, and drops selected indexes only
from the candidate copy. Results are diagnostic, not a production migration.
"""

import argparse
from datetime import datetime, timezone
import json
from pathlib import Path
import random
import shutil
import sqlite3
import statistics
import tempfile
import time
import uuid


PRAGMAS = (
    "PRAGMA foreign_keys=ON",
    "PRAGMA busy_timeout=10000",
    "PRAGMA journal_mode=WAL",
    "PRAGMA synchronous=NORMAL",
    "PRAGMA cache_size=-64000",
    "PRAGMA mmap_size=268435456",
    "PRAGMA temp_store=MEMORY",
    "PRAGMA wal_autocheckpoint=10000",
)

INSERT = """INSERT INTO executions (
    id, function_id, status, cold_start, container_id,
    duration_ms, status_code, error_message, response_size,
    started_at, finished_at, trace_id, span_id, parent_span_id,
    trigger, parent_function_id, is_outlier, baseline_p95_ms
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"""


def configure(conn):
    for statement in PRAGMAS:
        conn.execute(statement)


def v7_like(sequence):
    # Orva's primary/trace keys are time-ordered UUIDv7, not random UUIDv4.
    millis = int(time.time() * 1000) & ((1 << 48) - 1)
    bits = (millis << 80) | (0x7 << 76) | ((sequence & 0xFFF) << 64)
    bits |= (0x2 << 62) | random.getrandbits(62)
    return str(uuid.UUID(int=bits))


def rows(function_id, batch_size, sequence):
    now = datetime.now(timezone.utc).strftime("%Y-%m-%d %H:%M:%S.%f +0000 UTC")
    result = []
    for offset in range(batch_size):
        key = v7_like(sequence + offset)
        result.append((key, function_id, "success", 0, "benchmark-worker",
                       10, 200, "", 12, now, now, key, key, None,
                       "http", None, 0, None))
    return result


def queries(function_id, trace_id):
    return {
        "global_history": ("SELECT * FROM executions ORDER BY started_at DESC LIMIT 50", ()),
        "function_history": ("SELECT * FROM executions WHERE function_id=? "
                             "ORDER BY started_at DESC LIMIT 50", (function_id,)),
        "status_history": ("SELECT * FROM executions WHERE status=? "
                           "ORDER BY started_at DESC LIMIT 50", ("success",)),
        "trace_members": ("SELECT * FROM executions WHERE trace_id=? "
                          "ORDER BY julianday(replace(started_at, ' +0000 UTC', 'Z')) "
                          "ASC, id ASC", (trace_id,)),
        "retention_plan": ("SELECT id FROM executions WHERE started_at < ?",
                           ("2020-01-01",)),
    }


def read_profile(conn, function_id, trace_id, repetitions):
    result = {}
    for name, (sql, args) in queries(function_id, trace_id).items():
        plan = [row[3] for row in conn.execute("EXPLAIN QUERY PLAN " + sql, args)]
        samples = []
        for _ in range(repetitions):
            start = time.perf_counter()
            conn.execute(sql, args).fetchall()
            samples.append((time.perf_counter() - start) * 1000)
        result[name] = {"plan": plan, "median_ms": statistics.median(samples),
                        "samples_ms": samples}
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--db", type=Path, required=True, help="source Orva SQLite database")
    parser.add_argument("--scratch", action="store_true", help="confirm disposable test environment")
    parser.add_argument("--drop-index", action="append", required=True,
                        help="execution index name to omit from candidate copy; repeatable")
    parser.add_argument("--batches", type=int, default=4)
    parser.add_argument("--batch-size", type=int, default=200)
    parser.add_argument("--read-repetitions", type=int, default=3)
    parser.add_argument("--workdir", type=Path, help="temporary-copy parent; defaults to system temp")
    args = parser.parse_args()

    if not args.scratch:
        parser.error("refusing to copy or mutate a database without --scratch")
    if min(args.batches, args.batch_size, args.read_repetitions) < 1:
        parser.error("batch and repetition counts must be positive")
    if len(set(args.drop_index)) != len(args.drop_index):
        parser.error("duplicate --drop-index")
    source = args.db.resolve(strict=True)
    parent = args.workdir.resolve(strict=True) if args.workdir else Path(tempfile.gettempdir())
    size = source.stat().st_size
    if shutil.disk_usage(parent).free < 2 * size + 256 * 1024 * 1024:
        parser.error("not enough free space for two complete SQLite backup copies")

    source_conn = sqlite3.connect(source.as_uri() + "?mode=ro", uri=True)
    try:
        indexes = {name for (name,) in source_conn.execute(
            "SELECT name FROM sqlite_master WHERE type='index' AND tbl_name='executions' "
            "AND name NOT LIKE 'sqlite_autoindex_%'")}
        missing = set(args.drop_index) - indexes
        if missing:
            parser.error("not execution indexes: " + ", ".join(sorted(missing)))
        function_row = source_conn.execute("SELECT id FROM functions LIMIT 1").fetchone()
        trace_row = source_conn.execute(
            "SELECT trace_id FROM executions WHERE trace_id IS NOT NULL LIMIT 1").fetchone()
        if function_row is None or trace_row is None:
            parser.error("source needs a function and at least one traced execution")
        function_id, trace_id = function_row[0], trace_row[0]
        source_rows = source_conn.execute("SELECT COUNT(*) FROM executions").fetchone()[0]

        with tempfile.TemporaryDirectory(prefix="orva-index-ab-", dir=parent) as directory:
            baseline_path = Path(directory) / "baseline.db"
            candidate_path = Path(directory) / "candidate.db"
            baseline_copy = sqlite3.connect(baseline_path)
            try:
                source_conn.backup(baseline_copy)
            finally:
                baseline_copy.close()
            shutil.copyfile(baseline_path, candidate_path)
            baseline = sqlite3.connect(baseline_path)
            candidate = sqlite3.connect(candidate_path)
            try:
                configure(baseline)
                configure(candidate)
                for name in args.drop_index:
                    candidate.execute('DROP INDEX "' + name.replace('"', '""') + '"')
                candidate.commit()
                profiles = {
                    "baseline": read_profile(baseline, function_id, trace_id,
                                             args.read_repetitions),
                    "candidate": read_profile(candidate, function_id, trace_id,
                                              args.read_repetitions),
                }
                write_ms = {"baseline": [], "candidate": []}
                for batch in range(args.batches):
                    # Alternate run order; both copies start from the same backup.
                    order = ("baseline", "candidate") if batch % 2 == 0 else (
                        "candidate", "baseline")
                    for variant in order:
                        conn = baseline if variant == "baseline" else candidate
                        values = rows(function_id, args.batch_size,
                                      batch * args.batch_size + (0 if variant == "baseline" else 2048))
                        start = time.perf_counter()
                        with conn:
                            conn.executemany(INSERT, values)
                        write_ms[variant].append((time.perf_counter() - start) * 1000)
                output = {
                    "source": str(source), "source_rows": source_rows,
                    "source_bytes": size, "candidate_dropped_indexes": args.drop_index,
                    "batches": args.batches, "batch_size": args.batch_size,
                    "read": profiles,
                    "write": {name: {"samples_ms": samples,
                                     "median_ms": statistics.median(samples)}
                              for name, samples in write_ms.items()},
                    "caveat": "Exploratory SQLite/Python-driver test on copied data; "
                              "not a sustained Orva HTTP capacity or migration result",
                }
                print(json.dumps(output, indent=2))
            finally:
                baseline.close()
                candidate.close()
    finally:
        source_conn.close()


if __name__ == "__main__":
    main()
