#!/usr/bin/env python3
"""Scratch-VM-only, real NSTUN HTTPS PUT/GET/DELETE with verified TLS."""
import argparse
import concurrent.futures
import hashlib
import http.client
import http.server
import json
import pathlib
import shutil
import socket
import ssl
import subprocess
import sys
import tempfile
import threading
import time


def transfer(address, port, ca, size, number):
    data = bytes((i * 131 + i // 251) % 256 for i in range(size))
    key = f"/regression-{size}-{number}"
    context = ssl.create_default_context(cafile=ca)
    context.minimum_version = ssl.TLSVersion.TLSv1_2

    def request(method, body=None):
        connection = http.client.HTTPSConnection("nstun-regression.test", port, timeout=20, context=context)
        connection.sock = context.wrap_socket(socket.create_connection((address, port), timeout=20),
                                             server_hostname="nstun-regression.test")
        try:
            connection.request(method, key, body=body)
            response = connection.getresponse()
            return response.status, response.read()
        finally:
            connection.close()

    try:
        status, _ = request("PUT", data)
        assert status == 201, status
        status, actual = request("GET")
        assert status == 200 and actual == data, (status, len(actual), size)
        return {"size": size, "sha256": hashlib.sha256(data).hexdigest(), "passed": True}
    finally:
        status, _ = request("DELETE")
        assert status == 204, status
        status, _ = request("GET")
        assert status == 404, status


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--scratch", action="store_true")
    parser.add_argument("--nsjail", default="/usr/local/bin/nsjail")
    parser.add_argument("--disable-userns", action="store_true")
    parser.add_argument("--client", nargs=3, metavar=("ADDRESS", "PORT", "CA"))
    args = parser.parse_args()
    if args.client:
        address, port, ca = args.client
        sizes = [1024, 56288, 262144, 1048576, 4194304]
        results = [transfer(address, int(port), ca, size, 0) for size in sizes]
        with concurrent.futures.ThreadPoolExecutor(max_workers=8) as executor:
            futures = [executor.submit(transfer, address, int(port), ca, size, index + 1)
                       for index in range(3) for size in sizes]
            results.extend(future.result() for future in futures)
        print(json.dumps({"sequential": 5, "concurrent": 15, "clients": 8,
                          "put_get_delete": "PASS", "results": results}))
        return
    if not args.scratch:
        parser.error("--scratch is required; run only in a disposable isolated VM")
    objects = {}
    lock = threading.Lock()

    class Handler(http.server.BaseHTTPRequestHandler):
        protocol_version = "HTTP/1.1"

        def log_message(self, *_):
            pass

        def reply(self, status, data=b""):
            self.send_response(status)
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)

        def do_PUT(self):
            remaining = int(self.headers.get("Content-Length", "0"))
            if not 0 < remaining <= 4194304:
                self.reply(413)
                return
            chunks = []
            while remaining:
                chunk = self.rfile.read(min(remaining, 16384))
                if not chunk:
                    self.reply(400)
                    return
                chunks.append(chunk)
                remaining -= len(chunk)
                time.sleep(0.001)
            with lock:
                objects[self.path] = b"".join(chunks)
            self.reply(201)

        def do_GET(self):
            with lock:
                data = objects.get(self.path)
            self.reply(404 if data is None else 200, data or b"")

        def do_DELETE(self):
            with lock:
                objects.pop(self.path, None)
            self.reply(204)

    with tempfile.TemporaryDirectory(prefix="orva-nstun-https-") as work:
        # Do not rely on traversal permissions of the CI runner's private home.
        client = str(pathlib.Path(work) / "client.py")
        shutil.copyfile(__file__, client)
        cert = str(pathlib.Path(work) / "cert.pem")
        key = str(pathlib.Path(work) / "key.pem")
        subprocess.run(["openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes",
                        "-keyout", key, "-out", cert, "-days", "1",
                        "-subj", "/CN=nstun-regression.test", "-addext",
                        "subjectAltName=DNS:nstun-regression.test"],
                       check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        server = http.server.ThreadingHTTPServer(("0.0.0.0", 0), Handler)
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.minimum_version = ssl.TLSVersion.TLSv1_2
        context.load_cert_chain(cert, key)
        server.socket = context.wrap_socket(server.socket, server_side=True)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as probe:
            probe.connect(("192.0.2.1", 80))
            address = probe.getsockname()[0]
        try:
            flags = ["--disable_clone_newuser"] if args.disable_userns else []
            result = subprocess.run([args.nsjail, "-Mo", *flags, "--user_net", "--chroot", "/",
                                     "--time_limit", "120", "--rlimit_as", "2048", "--",
                                     sys.executable, client,
                                     "--client", address, str(server.server_port), cert],
                                    timeout=150, check=False)
            assert result.returncode == 0, f"sandbox client exited {result.returncode}"
            assert not objects, f"test objects leaked: {len(objects)}"
        finally:
            server.shutdown()
            server.server_close()


if __name__ == "__main__":
    main()
