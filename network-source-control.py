#!/usr/bin/env python3
"""Local macOS source adapter. No panel APIs, shell evaluation or modem writes.

Only `tick` consumes dashboard requests. `active` prepares/reads local settings; `new-epoch` is
used by start.sh to fence a new VM session. All persistent files are local.
"""
from __future__ import annotations

import contextlib
import copy
import fcntl
import ipaddress
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import sys
import tempfile
import time
import uuid

ROOT = Path(__file__).resolve().parent
INTERFACE = re.compile(r"en[0-9]{1,3}\Z")
LABELS = {"iphone": "iPhone USB", "router": "LTE router"}


def atomic_json(path: Path, value: dict) -> None:
    fd, name = tempfile.mkstemp(prefix=".source-", dir=path.parent)
    try:
        with os.fdopen(fd, "w") as output:
            json.dump(value, output, ensure_ascii=False)
            output.write("\n")
            output.flush()
            os.fsync(output.fileno())
        os.replace(name, path)
    finally:
        with contextlib.suppress(FileNotFoundError):
            os.unlink(name)


def read_json(path: Path) -> dict:
    flags = os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0)
    fd = os.open(path, flags)
    with os.fdopen(fd) as source:
        info = os.fstat(source.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_uid != os.getuid() or info.st_size > 65536:
            raise ValueError("Invalid local control file")
        value = json.load(source)
    if not isinstance(value, dict):
        raise ValueError("Expected control object")
    return value


def run_read(args: list[str], timeout: int = 3) -> str:
    try:
        result = subprocess.run(args, capture_output=True, text=True, timeout=timeout, check=False)
        return result.stdout if result.returncode == 0 else ""
    except (OSError, subprocess.TimeoutExpired):
        return ""


def discover() -> list[dict]:
    ports = run_read(["/usr/sbin/networksetup", "-listallhardwareports"])
    result = []
    for block in ports.split("\n\n"):
        name = re.search(r"^Device: (en[0-9]{1,3})$", block, re.M)
        label = re.search(r"^Hardware Port: (.+)$", block, re.M)
        if not name:
            continue
        address = run_read(["/usr/sbin/ipconfig", "getifaddr", name[1]], 1).strip()
        try:
            parsed = ipaddress.IPv4Address(address)
            if parsed.is_loopback or parsed.is_unspecified or parsed.is_link_local:
                address = ""
        except ValueError:
            address = ""
        result.append({"name": name[1], "label": label[1][:100] if label else name[1], "address": address})
    return result


class Controller:
    def __init__(self, directory: Path | None = None, inventory=discover, starter=None):
        self.directory = directory or Path(os.environ.get("XRAY_NETWORK_CONTROL_DIR", ROOT / ".runtime/control/network-sources"))
        self.inventory = inventory
        self.starter = starter or self.start_bridge

    def prepare(self):
        self.directory.mkdir(parents=True, exist_ok=True, mode=0o700)
        info = self.directory.lstat()
        if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.getuid():
            raise ValueError("Control directory must be owned by the supervisor user")
        os.chmod(self.directory, 0o700)

    @contextlib.contextmanager
    def lock(self):
        self.prepare()
        fd = os.open(self.directory / ".lock", os.O_CREAT | os.O_RDWR | getattr(os, "O_NOFOLLOW", 0), 0o600)
        with os.fdopen(fd, "w") as lock:
            fcntl.flock(lock, fcntl.LOCK_EX)
            yield

    def config(self):
        path = self.directory / "config.json"
        if path.exists():
            config = read_json(path)
            if config.get("activeSourceId") not in LABELS or not re.fullmatch(r"[a-f0-9]{32}", config.get("epoch", "")):
                raise ValueError("Invalid source configuration")
            for key in LABELS:
                interface = config.get("interfaces", {}).get(key, "")
                if interface and not INTERFACE.fullmatch(interface):
                    raise ValueError("Invalid configured interface")
            return config
        phone = os.environ.get("IPHONE_INTERFACE", "en7")
        if not INTERFACE.fullmatch(phone):
            raise ValueError("Invalid iPhone interface")
        return {"version": 1, "activeSourceId": "iphone", "epoch": uuid.uuid4().hex,
                "interfaces": {"iphone": phone, "router": ""}}

    def publish(self, config, job=None, interfaces=None):
        if job is None:
            try:
                job = read_json(self.directory / "status.json").get("job", {"state": "idle"})
            except FileNotFoundError:
                job = {"state": "idle"}
        interfaces = self.inventory() if interfaces is None else interfaces
        profiles = []
        for source_id, label in LABELS.items():
            interface = config["interfaces"].get(source_id, "")
            found = next((item for item in interfaces if item["name"] == interface), {})
            profiles.append({"id": source_id, "label": label, "interface": interface,
                             "available": bool(found.get("address")), "address": found.get("address", "")})
        snapshot = {"version": 1, "enabled": True, "activeSourceId": config["activeSourceId"],
                    "epoch": config["epoch"], "updatedAt": int(time.time()), "profiles": profiles,
                    "interfaces": interfaces, "job": job}
        atomic_json(self.directory / "status.json", snapshot)
        return snapshot

    def active(self):
        with self.lock():
            config = self.config()
            atomic_json(self.directory / "config.json", config)
            return config["activeSourceId"], config["interfaces"][config["activeSourceId"]], config["epoch"]

    def new_epoch(self):
        with self.lock():
            config = self.config()
            phone = os.environ.get("IPHONE_INTERFACE", "")
            if config["activeSourceId"] == "iphone" and phone:
                if not INTERFACE.fullmatch(phone):
                    raise ValueError("Invalid iPhone interface")
                config["interfaces"]["iphone"] = phone
            config["epoch"] = uuid.uuid4().hex
            atomic_json(self.directory / "config.json", config)
            self.publish(config)
            return config["epoch"]

    def start_bridge(self, source_id, interface, epoch):
        env = os.environ.copy()
        env.update({"XRAY_RECOVERY_MODE": "true", "XRAY_PRESERVE_RESULTS": "true",
                    "NETWORK_SOURCE_ID": source_id, "NETWORK_HOST_INTERFACE": interface,
                    "NETWORK_SOURCE_EPOCH": epoch, "XRAY_NETWORK_SWITCH": "true"})
        # Inherited output goes to the supervisor log, never into JSON/browser.
        result = subprocess.run(["/bin/bash", str(ROOT / "start.sh")], cwd=ROOT, env=env, timeout=900, check=False)
        return result.returncode == 0

    def validate_request(self, request, config, interfaces):
        if set(request) != {"id", "action", "sourceId", "interface", "expectedEpoch", "requestedAt"}:
            raise ValueError("Unknown source request fields")
        if not re.fullmatch(r"source-[0-9]{1,24}", str(request["id"])):
            raise ValueError("Invalid request ID")
        if request["action"] not in {"configure", "validate", "activate"} or request["sourceId"] not in LABELS:
            raise ValueError("Invalid source action")
        if not isinstance(request["interface"], str) or not INTERFACE.fullmatch(request["interface"]):
            raise ValueError("Invalid interface")
        if request["expectedEpoch"] != config["epoch"]:
            raise ValueError("Source context changed; refresh and retry")
        age = time.time() - request["requestedAt"]
        if age < -5 or age > 120:
            raise ValueError("Source request expired")
        item = next((i for i in interfaces if i["name"] == request["interface"]), None)
        if item is None:
            raise ValueError("Interface is no longer in the host inventory")
        if request["action"] != "configure" and not item["address"]:
            raise ValueError("Selected adapter has no usable IPv4 address")
        if request["action"] == "configure" and request["sourceId"] == config["activeSourceId"]:
            raise ValueError("Use activation to change the active interface")

    def process(self, config, request, interfaces):
        job = {"id": str(request.get("id", "invalid"))[:40], "action": request.get("action", ""),
               "sourceId": request.get("sourceId", ""), "state": "running",
               "message": "Checking the selected adapter", "updatedAt": int(time.time())}
        self.publish(config, job, interfaces)
        try:
            self.validate_request(request, config, interfaces)
            source, interface, action = request["sourceId"], request["interface"], request["action"]
            if action == "configure":
                config["interfaces"][source] = interface
                atomic_json(self.directory / "config.json", config)
                message = "Adapter saved; active source was not changed"
            elif action == "validate":
                message = "Adapter has IPv4. Guest bridge and internet will be verified during activation"
            else:
                previous = copy.deepcopy(config)
                epoch = uuid.uuid4().hex
                job["message"] = "Switching the bridge; dashboard may temporarily disconnect"
                self.publish(config, job, interfaces)
                try:
                    success = self.starter(source, interface, epoch)
                except (OSError, subprocess.TimeoutExpired):
                    success = False
                if success:
                    config["activeSourceId"], config["epoch"] = source, epoch
                    config["interfaces"][source] = interface
                    atomic_json(self.directory / "config.json", config)
                    message = "Selected source connected; checks resume in a new session"
                else:
                    # Rollback is part of this explicit transition, not automatic WAN failover.
                    job["message"] = "New source failed; restoring the previous bridge"
                    self.publish(previous, job, interfaces)
                    rollback_epoch = uuid.uuid4().hex
                    try:
                        restored = self.starter(previous["activeSourceId"], previous["interfaces"][previous["activeSourceId"]], rollback_epoch)
                    except (OSError, subprocess.TimeoutExpired):
                        restored = False
                    config = previous
                    if restored:
                        config["epoch"] = rollback_epoch
                    atomic_json(self.directory / "config.json", config)
                    job["rollback"] = "succeeded" if restored else "failed"
                    raise ValueError("New source failed; previous route restored" if restored else "New source and rollback failed; checks remain paused")
            job.update(state="succeeded", message=message)
        except (ValueError, TypeError, KeyError) as error:
            job.update(state="failed", message=str(error)[:240])
        job["updatedAt"] = int(time.time())
        self.publish(config, job, self.inventory())

    def tick(self):
        with self.lock():
            config = self.config()
            atomic_json(self.directory / "config.json", config)
            processing = self.directory / "processing.json"
            request_path = self.directory / "request.json"
            interfaces = self.inventory()
            if processing.exists():
                self.publish(config, {"state": "failed", "message": "Previous source change was interrupted; reconnect the selected source", "updatedAt": int(time.time())}, interfaces)
                processing.unlink()
            if request_path.exists():
                # Request publication is atomic; only one supervisor can claim it.
                os.replace(request_path, processing)
                try:
                    request = read_json(processing)
                    self.process(config, request, interfaces)
                except (ValueError, json.JSONDecodeError) as error:
                    self.publish(config, {"state": "failed", "message": str(error)[:240], "updatedAt": int(time.time())}, interfaces)
                processing.unlink(missing_ok=True)
            else:
                self.publish(config, interfaces=interfaces)


def main():
    control = Controller()
    if len(sys.argv) != 2:
        raise ValueError("Usage: network-source-control.py {tick|active|new-epoch}")
    command = sys.argv[1]
    if command == "tick":
        control.tick()
    elif command == "active":
        print("\t".join(control.active()))
    elif command == "new-epoch":
        print(control.new_epoch())
    else:
        raise ValueError("Unknown source-control command")


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError) as error:
        print(str(error), file=sys.stderr)
        sys.exit(1)
