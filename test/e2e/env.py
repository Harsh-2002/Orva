#!/usr/bin/env python3
"""
env.py — spin an ISOLATED Orva instance in a freshly-built local Docker image
for end-to-end tests. Never touches the dev instance.

Build the image from the current source, run a container with the capabilities
nsjail needs (so real deploy + invoke work when the host kernel allows it), wait
for health, read the first-boot admin key, and tear everything down afterward.

Stdlib only.
"""
import os
import subprocess
import time

HERE = os.path.dirname(os.path.abspath(__file__))
REPO_ROOT = os.path.abspath(os.path.join(HERE, "..", ".."))
IMAGE = os.environ.get("ORVA_E2E_IMAGE", "orva:e2e")
CONTAINER = os.environ.get("ORVA_E2E_CONTAINER", "orva-e2e")
VOLUME = "orva-e2e-data"


def _docker(*args, check=True, capture=True, timeout=1800):
    p = subprocess.run(["docker", *args], capture_output=capture, text=True, timeout=timeout)
    if check and p.returncode != 0:
        raise RuntimeError(f"docker {' '.join(args)} failed: {p.stderr or p.stdout}")
    return p


def docker_available():
    try:
        return _docker("version", "--format", "{{.Server.Version}}", check=False).returncode == 0
    except Exception:
        return False


def image_exists(tag=IMAGE):
    return _docker("image", "inspect", tag, check=False).returncode == 0


def ensure_image(rebuild=False, tag=IMAGE):
    if image_exists(tag) and not rebuild:
        print(f"[env] reusing image {tag}")
        return
    print(f"[env] building image {tag} from {REPO_ROOT} (this takes a few minutes)…")
    p = subprocess.run(
        ["docker", "build", "-t", tag, "--build-arg", "VERSION=e2e-test", "."],
        cwd=REPO_ROOT, text=True, timeout=2400,
    )
    if p.returncode != 0:
        raise RuntimeError("docker build failed")
    print(f"[env] built {tag}")


class Instance:
    """A running, isolated Orva container."""

    def __init__(self, port=None, name=CONTAINER, image=IMAGE):
        self.port = int(port or os.environ.get("ORVA_E2E_PORT", "8455"))
        self.name = name
        self.image = image
        self.base_url = f"http://127.0.0.1:{self.port}"

    def _cleanup_existing(self):
        _docker("rm", "-f", self.name, check=False)
        _docker("volume", "rm", VOLUME, check=False)

    def start(self):
        self._cleanup_existing()
        args = [
            "run", "-d", "--name", self.name,
            "-p", f"127.0.0.1:{self.port}:8443",
            "-v", f"{VOLUME}:/var/lib/orva",
            # Reach the host's mock LLM from inside the container.
            "--add-host", "host.docker.internal:host-gateway",
            # SYS_ADMIN is all nsjail needs for sandboxed deploy/invoke: each
            # sandbox's TAP device is created inside nsjail's own user
            # namespace, so egress needs no NET_ADMIN on the container. If the
            # host kernel won't allow nested sandboxing, those scenarios skip
            # themselves; everything else still runs.
            "--cap-add", "SYS_ADMIN",
            "--security-opt", "seccomp=unconfined",
            "--security-opt", "apparmor=unconfined",
            "--cgroupns=host", "--pid=host",
            "-v", "/sys/fs/cgroup:/sys/fs/cgroup:rw",
        ]
        if os.path.exists("/dev/net/tun"):
            args += ["--device", "/dev/net/tun:/dev/net/tun"]
        args += [self.image]
        _docker(*args)
        print(f"[env] started {self.name} on {self.base_url}")
        return self

    def wait_healthy(self, timeout=60):
        import urllib.request
        deadline = time.time() + timeout
        url = self.base_url + "/api/v1/system/health"
        while time.time() < deadline:
            try:
                with urllib.request.urlopen(url, timeout=3) as r:
                    if r.status == 200:
                        print("[env] healthy")
                        return True
            except Exception:
                pass
            # Surface a hard crash early instead of waiting the full timeout.
            if _docker("inspect", "-f", "{{.State.Running}}", self.name, check=False).stdout.strip() == "false":
                raise RuntimeError("container exited:\n" + self.logs())
            time.sleep(1)
        raise RuntimeError("container did not become healthy:\n" + self.logs())

    def admin_key(self, timeout=20):
        deadline = time.time() + timeout
        while time.time() < deadline:
            p = _docker("exec", self.name, "cat", "/var/lib/orva/.admin-key", check=False)
            if p.returncode == 0 and p.stdout.strip():
                return p.stdout.strip()
            time.sleep(0.5)
        raise RuntimeError("admin key not found in container")

    def logs(self, tail="120"):
        return _docker("logs", "--tail", str(tail), self.name, check=False).stdout or ""

    def exec(self, *args):
        return _docker("exec", self.name, *args, check=False)

    def stop(self):
        self._cleanup_existing()
        print(f"[env] removed {self.name} + volume")
