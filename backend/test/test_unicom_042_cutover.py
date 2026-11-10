import importlib.util
import copy
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
                    "instance": "unicom-k8s", "url": "https://doops.aiedulab.cn"}})
            if args[:3] == ("helm", "get", "manifest"):
                return manifest
            if args[:3] == ("helm", "show", "chart"):
                return "name: doops-agent\nversion: 0.3.0\n"
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
            if "rollout" in args and failure == "recover":
                raise RuntimeError("old readiness unavailable")
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
                with self.assertRaises(RuntimeError) as caught:
                    cutover.main()
                if failure == "recover":
                    self.assertIn("acceptance was not received", str(caught.exception))
                    self.assertIn("old readiness unavailable", str(caught.exception))
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

    def test_recovery_failure_cannot_report_old_preserved(self):
        _, phases = self.execute(failure="recover", accepted=False)
        self.assertNotIn("old-preserved", phases)

    def test_candidate_requires_current_generation_unique_ready_pod_and_exact_image(self):
        deployment = {"metadata": {"generation": 1},
                      "status": {"observedGeneration": 1, "readyReplicas": 1}}
        pod = {"metadata": {"name": "new-pod", "uid": "new-uid"},
               "spec": {"containers": [{"image": cutover.IMAGE}]},
               "status": {"containerStatuses": [{"ready": True, "imageID": cutover.IMAGE}]}}
        for failure in [None, "generation", "duplicate", "image", "unready", "deleting"]:
            with self.subTest(failure=failure):
                dep, candidate = copy.deepcopy(deployment), copy.deepcopy(pod)
                pods = [candidate]
                if failure == "generation":
                    dep["status"]["observedGeneration"] = 0
                if failure == "duplicate":
                    pods.append(copy.deepcopy(candidate))
                if failure == "image":
                    candidate["status"]["containerStatuses"][0]["imageID"] = "wrong-image"
                if failure == "unready":
                    candidate["status"]["containerStatuses"][0]["ready"] = False
                if failure == "deleting":
                    candidate["metadata"]["deletionTimestamp"] = "2026-11-08T07:00:00Z"
                with patch.object(cutover, "get", return_value=dep), \
                     patch.object(cutover, "run", return_value=json.dumps({"items": pods})):
                    if failure:
                        with self.assertRaises(AssertionError):
                            cutover.ready_candidate()
                    else:
                        self.assertEqual(cutover.ready_candidate(), ("new-pod", "new-uid"))


if __name__ == "__main__":
    unittest.main()
