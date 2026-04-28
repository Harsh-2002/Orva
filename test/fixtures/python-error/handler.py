"""Error-prone function — fails 20% of the time."""
import json
import random
import os

def handler(event):
    # Use PID + time for randomness (each nsjail is a fresh process)
    seed = os.getpid() ^ int.from_bytes(os.urandom(4), "big")
    random.seed(seed)

    if random.random() < 0.2:
        raise RuntimeError("Simulated random failure (20% chance)")

    return {
        "statusCode": 200,
        "headers": {"Content-Type": "application/json"},
        "body": json.dumps({"ok": True, "pid": os.getpid()}),
    }
