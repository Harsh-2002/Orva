"""Data processing function — parse, filter, aggregate."""
import json
from collections import Counter

SAMPLE_DATA = [
    {"name": "Alice", "age": 30, "dept": "eng", "salary": 120000},
    {"name": "Bob", "age": 25, "dept": "eng", "salary": 95000},
    {"name": "Carol", "age": 35, "dept": "sales", "salary": 110000},
    {"name": "Dave", "age": 28, "dept": "eng", "salary": 105000},
    {"name": "Eve", "age": 32, "dept": "sales", "salary": 98000},
    {"name": "Frank", "age": 40, "dept": "hr", "salary": 90000},
    {"name": "Grace", "age": 27, "dept": "eng", "salary": 115000},
    {"name": "Hank", "age": 45, "dept": "hr", "salary": 95000},
    {"name": "Ivy", "age": 29, "dept": "sales", "salary": 102000},
    {"name": "Jack", "age": 33, "dept": "eng", "salary": 130000},
]


def handler(event):
    body = event.get("body", "{}")
    if isinstance(body, str):
        body = json.loads(body) if body else {}

    # Filter by department if specified
    dept = body.get("dept")
    min_age = body.get("min_age", 0)
    data = [r for r in SAMPLE_DATA if r["age"] >= min_age]
    if dept:
        data = [r for r in data if r["dept"] == dept]

    # Aggregate
    dept_counts = Counter(r["dept"] for r in data)
    avg_salary = sum(r["salary"] for r in data) / max(len(data), 1)
    avg_age = sum(r["age"] for r in data) / max(len(data), 1)

    # Sort by salary descending
    ranked = sorted(data, key=lambda r: r["salary"], reverse=True)

    return {
        "statusCode": 200,
        "headers": {"Content-Type": "application/json"},
        "body": json.dumps({
            "total": len(data),
            "departments": dict(dept_counts),
            "avg_salary": round(avg_salary, 2),
            "avg_age": round(avg_age, 1),
            "top_earner": ranked[0]["name"] if ranked else None,
            "filtered_by": {"dept": dept, "min_age": min_age},
        }),
    }
