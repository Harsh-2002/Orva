import json


def handler(event):
    return {
        "statusCode": 200,
        "headers": {"Content-Type": "application/json"},
        "body": json.dumps({
            "message": "Hello from Python!",
            "method": event["method"],
            "path": event["path"],
        }),
    }
