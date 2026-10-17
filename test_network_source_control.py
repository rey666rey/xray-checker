import copy
import importlib.util
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import time
import unittest

SPEC = importlib.util.spec_from_file_location("source_control", Path(__file__).with_name("network-source-control.py"))
control = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(control)


class SourceControlTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.directory = Path(self.temporary.name) / "control"
        self.inventory = [{"name": "en7", "label": "iPhone USB", "address": "172.20.10.2"},
                          {"name": "en9", "label": "Ethernet", "address": "192.168.8.2"}]
        self.calls = []
        self.controller = control.Controller(self.directory, inventory=lambda: self.inventory,
                                              starter=lambda *args: self.calls.append(args) or True)
        self.controller.tick()
        self.original = control.read_json(self.directory / "config.json")

    def submit(self, action="activate", source="router", interface="en9", **changes):
        request = {"id": "source-123", "action": action, "sourceId": source,
                   "interface": interface, "expectedEpoch": self.original["epoch"],
                   "requestedAt": int(time.time())}
        request.update(changes)
        control.atomic_json(self.directory / "request.json", request)
        self.controller.tick()
        return control.read_json(self.directory / "status.json")

    def test_preflight_never_changes_active_source_or_starts_vm(self):
        result = self.submit("validate")
        self.assertEqual(result["job"]["state"], "succeeded")
        self.assertIn("during activation", result["job"]["message"])
        self.assertEqual(self.calls, [])
        self.assertEqual(control.read_json(self.directory / "config.json"), self.original)

    def test_configuration_does_not_activate(self):
        result = self.submit("configure")
        self.assertEqual(result["activeSourceId"], "iphone")
        self.assertEqual(self.calls, [])
        self.assertEqual(control.read_json(self.directory / "config.json")["interfaces"]["router"], "en9")

    def test_activation_commits_only_after_route_is_ready(self):
        def starter(*args):
            self.assertEqual(control.read_json(self.directory / "config.json")["activeSourceId"], "iphone")
            self.calls.append(args)
            return True
        self.controller.starter = starter
        result = self.submit()
        self.assertEqual(result["activeSourceId"], "router")
        self.assertNotEqual(result["epoch"], self.original["epoch"])
        self.assertEqual(result["job"]["state"], "succeeded")
        self.assertEqual(self.calls[0][:2], ("router", "en9"))

    def test_failed_activation_rolls_back_with_new_epoch(self):
        def starter(*args):
            self.calls.append(args)
            return len(self.calls) == 2
        self.controller.starter = starter
        result = self.submit()
        self.assertEqual(result["activeSourceId"], "iphone")
        self.assertEqual(result["job"]["state"], "failed")
        self.assertEqual(result["job"]["rollback"], "succeeded")
        self.assertEqual(self.calls[1][:2], ("iphone", "en7"))
        self.assertNotEqual(result["epoch"], self.original["epoch"])

    def test_failed_rollback_keeps_previous_identity_and_explicit_failure(self):
        self.controller.starter = lambda *args: False
        result = self.submit()
        self.assertEqual(result["job"]["rollback"], "failed")
        self.assertEqual(control.read_json(self.directory / "config.json"), self.original)

    def test_invalid_stale_and_injection_requests_never_start_vm(self):
        for changes in [{"interface": "en9; touch BAD"}, {"expectedEpoch": "old"},
                        {"requestedAt": 1}, {"action": "shell"}, {"interface": "en88"},
                        {"extra": "value"}, {"sourceId": "other"}]:
            result = self.submit(**changes)
            self.assertEqual(result["job"]["state"], "failed", changes)
        self.assertEqual(self.calls, [])

    def test_adapter_disappeared_between_queue_and_processing(self):
        self.inventory.pop()
        result = self.submit()
        self.assertEqual(result["job"]["state"], "failed")
        self.assertEqual(self.calls, [])

    def test_no_ipv4_does_not_switch(self):
        self.inventory[1]["address"] = ""
        result = self.submit()
        self.assertEqual(result["job"]["state"], "failed")
        self.assertEqual(self.calls, [])

    def test_interrupted_request_does_not_fake_success(self):
        control.atomic_json(self.directory / "processing.json", {"id": "source-123"})
        self.controller.tick()
        self.assertEqual(control.read_json(self.directory / "status.json")["job"]["state"], "failed")
        self.assertEqual(self.calls, [])

    def test_private_files_and_stable_profile_reads(self):
        self.assertEqual(self.directory.stat().st_mode & 0o777, 0o700)
        for name in ["config.json", "status.json"]:
            self.assertEqual((self.directory / name).stat().st_mode & 0o777, 0o600)
        self.assertEqual(self.controller.active()[0], "iphone")
        self.assertEqual(self.controller.active()[2], self.original["epoch"])

    def test_start_script_adapter_with_stubbed_commands(self):
        # Run a copy: no real Docker/Colima, production .env, network or VM access.
        root = Path(self.temporary.name) / "start-test"
        root.mkdir()
        shutil.copy(Path(__file__).with_name("start.sh"), root / "start.sh")
        (root / ".env").write_text("SUBSCRIPTION_URL=https://example.invalid\n")
        tools = root / "bin"
        tools.mkdir()
        for command in ["docker", "colima", "ipconfig"]:
            path = tools / command
            path.write_text("#!/bin/bash\n"
                            'printf "%s %s source=%s interface=%s\\n" "$(basename "$0")" "$*" "$NETWORK_SOURCE_ID" "$NETWORK_HOST_INTERFACE" >> "$TRACE"\n'
                            'if [[ "$(basename "$0")" == colima && "$1" == ssh ]]; then\n'
                            '  if [[ "$*" == *"ip -4 route"* ]]; then echo "default via 192.168.8.1 dev col0"; else echo "ip=203.0.113.7"; fi\n'
                            'fi\nexit 0\n')
            path.chmod(0o700)
        env = os.environ.copy()
        env.update(PATH=str(tools) + ":" + env["PATH"], NETWORK_SOURCE_ID="router", NETWORK_HOST_INTERFACE="en9",
                   NETWORK_SOURCE_EPOCH="b" * 32, XRAY_RECOVERY_MODE="true", IPHONE_BRIDGE_READY_SUCCESSES="1", TRACE=str(root / "trace"))
        result = subprocess.run(["bash", str(root / "start.sh")], env=env, cwd=root, capture_output=True, text=True, timeout=10)
        self.assertEqual(result.returncode, 0, result.stderr)
        trace = (root / "trace").read_text()
        self.assertIn("--network-interface en9", trace)
        self.assertIn("--network-preferred-route", trace)
        self.assertIn("source=router interface=en9", trace)
        self.assertNotIn(" compose build", trace)
        self.assertNotIn("prune", trace)
        self.assertNotIn("/bin/rm", trace)


if __name__ == "__main__":
    unittest.main()
