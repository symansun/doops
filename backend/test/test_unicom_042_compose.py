import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch


path = Path(__file__).parents[1] / "deploy/instances/unicom-1/compose-upgrade.py"
spec = importlib.util.spec_from_file_location("unicom_compose", path)
upgrade = importlib.util.module_from_spec(spec)
spec.loader.exec_module(upgrade)


class ComposeTransactionTests(unittest.TestCase):
    def transaction(self, accepted=False, fail_install=False, fail_restore=False, wrong_id=False):
        with tempfile.TemporaryDirectory() as directory:
            state = Path(directory)
            source = state / "compose.yaml"
            source.write_text("original")
            calls = []
            running_image = "old-image"
            if accepted:
                (state / "acceptance.json").write_text(json.dumps({
                    "containerID": "wrong-id" if wrong_id else "new-id",
                    "functionMarker": "RELEASE_SMOKE_UNICOM1_042",
                    "acceptanceSession": "acceptance-test",
                }))

            def compose(*args, **kwargs):
                nonlocal running_image
                calls.append(source.read_text())
                running_image = "new-image" if source.read_text() == "candidate" else "old-image"
                if len(calls) == 1 and fail_install:
                    raise RuntimeError("candidate failed health")
                if len(calls) == 2 and fail_restore:
                    raise RuntimeError("restore failed health")

            def verify(expected):
                self.assertEqual(running_image, expected)
                return "new-id" if expected == "new-image" else "restored-id"

            ticks = [100, 101] if accepted else [100, 701]
            with patch.object(upgrade, "SOURCE", source), \
                 patch.object(upgrade, "STATE", state), \
                 patch.object(upgrade, "compose", side_effect=compose), \
                 patch.object(upgrade, "verify_running", side_effect=verify), \
                 patch.object(upgrade.time, "monotonic", side_effect=ticks):
                if not accepted or fail_install or wrong_id:
                    with self.assertRaises((RuntimeError, AssertionError)):
                        upgrade.replace_and_accept("new-image", "old-image", "candidate", "rollback")
                else:
                    upgrade.replace_and_accept("new-image", "old-image", "candidate", "rollback")
            receipt = json.loads((state / "receipt.json").read_text()) if (state / "receipt.json").exists() else {}
            return source.read_text(), calls, receipt

    def test_success_keeps_pinned_compose_and_verified_container(self):
        source, calls, receipt = self.transaction(accepted=True)
        self.assertEqual((source, calls), ("candidate", ["candidate"]))
        self.assertEqual(receipt["phase"], "complete")
        self.assertEqual(receipt["containerID"], "new-id")

    def test_install_failure_restores_exact_rollback_config(self):
        source, calls, receipt = self.transaction(fail_install=True)
        self.assertEqual((source, calls), ("rollback", ["candidate", "rollback"]))
        self.assertEqual(receipt["phase"], "restored")

    def test_missing_acceptance_restores_old_image(self):
        _, _, receipt = self.transaction()
        self.assertEqual(receipt["phase"], "restored")
        self.assertEqual(receipt["imageID"], "old-image")

    def test_acceptance_for_other_container_cannot_complete(self):
        _, _, receipt = self.transaction(accepted=True, wrong_id=True)
        self.assertEqual(receipt["phase"], "restored")

    def test_failed_recovery_never_claims_restored(self):
        _, _, receipt = self.transaction(fail_restore=True)
        self.assertNotIn(receipt.get("phase"), ["restored", "complete"])


if __name__ == "__main__":
    unittest.main()
