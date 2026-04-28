"""CPU-bound function — sorting + matrix operations."""
import json
import random

def handler(event):
    body = event.get("body", "{}")
    if isinstance(body, str):
        body = json.loads(body) if body else {}

    n = min(body.get("n", 5000), 10000)

    # Generate and sort a random list
    random.seed(42)  # deterministic for reproducibility
    data = [random.randint(0, 1000000) for _ in range(n)]
    sorted_data = sorted(data)

    # Simple matrix multiplication (10x10)
    size = 10
    a = [[random.randint(0, 100) for _ in range(size)] for _ in range(size)]
    b = [[random.randint(0, 100) for _ in range(size)] for _ in range(size)]
    result = [[0] * size for _ in range(size)]
    for i in range(size):
        for j in range(size):
            for k in range(size):
                result[i][j] += a[i][k] * b[k][j]

    return {
        "statusCode": 200,
        "headers": {"Content-Type": "application/json"},
        "body": json.dumps({
            "sorted_count": len(sorted_data),
            "sorted_min": sorted_data[0],
            "sorted_max": sorted_data[-1],
            "matrix_trace": sum(result[i][i] for i in range(size)),
        }),
    }
