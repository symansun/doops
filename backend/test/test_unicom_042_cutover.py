import importlib.util
import json
import unittest
from pathlib import Path
from unittest.mock import patch


path = Path(__file__).parents[1] / "deploy/instances/unicom-k8s/cutover.py"
spec = importlib.util.spec_from_file_location("unicom_cutover", path)
cutover = importlib.util.module_from_spec(spec)
spec.loader.exec_module(cutover)


class CutoverTests(unittest.TestCase):
    def execute(self, failure=None, accepted=True):
        commands = []
        phases = []
        manifest = "kind: Deployment\nmetadata:\n  name: doops-unicom\n"
        old = {"metadata": {"uid": "old-uid"}, "status": {"readyReplicas": 1}}
        installed = False
        retired = False

        def run(*args, **kwargs):
            nonlocal installed, retired
            commands.append(args)
            if args[:2] == ("/app/doops-agent", "-version"):
                return f"release=v1.3.123 commit={cutover.COMMIT}"
            if args[:2] == ("/usr/local/bin/do-agent", "--version"):
                return "do-agent 0.2.42"
            if args[:2] == ("helm", "list"):
                return json.dumps([{"name": "doops-agent" if installed else "doops-unicom",
                                    "chart": "doops-agent-0.3.0"}])
            if args[:2] == ("helm", "history"):
                return '[{"revision": 5, "status": "deployed"}]'
            if args[:3] == ("helm", "get", "values"):
                return json.dumps({"gateway": {"cluster": "doops-unicom",
                    "instance": "unicom-k8s", "url": "https://doops.l8ai.cn"}})
            if args[:3] == ("helm", "get", "manifest"):
                return manifest
            if args[:2] == ("helm", "upgrade"):
                if "--dry-run=server" in args:
                    if failure == "dry-run":
                        raise RuntimeError("dry-run rejected")
                else:
                    installed = True
                    if failure == "install":
                        raise RuntimeError("install failed")
            if args[:2] == ("helm", "uninstall"):
                if args[2] == "doops-unicom":
                    if failure == "retire":
                        raise RuntimeError("old release cleanup failed")
                    retired = True
                else:
                    installed = False
            if args[:len(cutover.KUBECTL) + 2] == (*cutover.KUBECTL, "get", "deployments"):
                return json.dumps({"items": [] if retired else [old]})
            return ""

        def get(kind, name):
            if kind == "deployment":
                return old
            return {"data": {"verifiedPodUID": "new-uid",
                            "acceptanceSession": "functional-smoke",
                            "functionMarker": "RELEASE_SMOKE_UNICOM_042"}}

        ticks = [100, 101] if accepted else [100, 701]
        with patch.object(cutover, "run", side_effect=run), \
             patch.object(cutover, "get", side_effect=get), \
             patch.object(cutover, "ready_candidate", return_value=("new-pod", "new-uid")), \
             patch.object(cutover, "receipt", side_effect=lambda **kw: phases.append(kw["phase"])), \
             patch.object(cutover.signal, "signal"), \
             patch.object(cutover.time, "monotonic", side_effect=ticks), \
             patch.dict(cutover.os.environ, {"KUBECONFIG": "/tmp/test-config",
                        "KUBERNETES_SERVICE_HOST": "127.0.0.1", "KUBERNETES_SERVICE_PORT": "443"}):
            if failure or not accepted:
                with self.assertRaises(RuntimeError):
                    cutover.main()
            else:
                cutover.main()
        return commands, phases

    def test_only_verified_candidate_can_retire_old_release(self):
        commands, phases = self.execute()
        self.assertEqual(phases[-1], "complete")
        self.assertIn(("helm", "uninstall", "doops-unicom", "-n",
                       "doops-system", "--wait", "--timeout", "120s"), commands)
        self.assertFalse(any(c[:3] == ("helm", "uninstall", "doops-agent") for c in commands))

    def test_preflight_failure_keeps_both_workloads_untouched(self):
        commands, phases = self.execute(failure="dry-run")
        self.assertEqual(phases[-1], "old-preserved")
        self.assertFalse(any(c[:2] == ("helm", "uninstall") for c in commands))

    def test_failed_install_or_missing_acceptance_removes_only_candidate(self):
        for failure, accepted in [("install", True), (None, False)]:
            with self.subTest(failure=failure):
                commands, phases = self.execute(failure, accepted)
                removed = [c[2] for c in commands if c[:2] == ("helm", "uninstall")]
                self.assertEqual(removed, ["doops-agent"])
                self.assertEqual(phases[-1], "old-preserved")

    def test_cleanup_error_keeps_verified_candidate(self):
        commands, phases = self.execute(failure="retire")
        self.assertEqual(phases[-1], "old-cleanup-incomplete")
        self.assertFalse(any(c[:3] == ("helm", "uninstall", "doops-agent") for c in commands))


if __name__ == "__main__":
    unittest.main()
