import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import yaml

ROOT = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location("configure_finding_ai", ROOT / "scripts/configure-finding-ai.py")
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class FindingAIConfigTest(unittest.TestCase):
    def test_reuses_engine_configuration_and_only_selected_secret_key(self):
        for same_namespace in (False, True):
            with self.subTest(same_namespace=same_namespace), tempfile.TemporaryDirectory() as work:
                path = Path(work) / "api.yaml"
                api = yaml.safe_load((ROOT / "deploy/api/deployment.yaml").read_text())
                engine = yaml.safe_load((ROOT / "deploy/k8sgpt/k8sgpt.yaml").read_text())
                ai = engine["spec"]["ai"]
                ai["baseUrl"] = "https://provider.example/api/v4/"
                ai["model"] = "engine-model"
                ai["secret"] = {"name": "existing", "key": "actual-key"}
                if same_namespace:
                    api["metadata"]["namespace"] = engine["metadata"]["namespace"]
                path.write_text(yaml.safe_dump(api))
                calls = []

                def fake_kubectl(context, args, document=None):
                    self.assertEqual(context, "test-cluster")
                    if args[1] == "k8sgpt.core.k8sgpt.ai":
                        return json.dumps(engine)
                    calls.append((args, document))
                    return json.dumps({"data": {"actual-key": "YWJj", "other": "do-not-copy"}})

                with patch.object(MODULE, "kubectl", side_effect=fake_kubectl):
                    MODULE.configure(path, "test-cluster")
                    MODULE.configure(path, "test-cluster")
                pod = yaml.safe_load(path.read_text())["spec"]["template"]["spec"]
                env = [item for item in pod["containers"][0]["env"] if item["name"].startswith("FINDING_AI_")]
                self.assertEqual(len(env), 3)
                self.assertIn({"name": "FINDING_AI_MODEL", "value": "engine-model"}, env)
                self.assertIn({"name": "FINDING_AI_URL", "value": "https://provider.example/api/v4/chat/completions"}, env)
                self.assertNotIn("YWJj", path.read_text())
                if same_namespace:
                    self.assertEqual(calls, [])
                    self.assertEqual(pod["volumes"][0]["secret"]["secretName"], "existing")
                else:
                    self.assertEqual(len(calls), 4)
                    self.assertEqual(json.loads(calls[1][1])["data"], {"actual-key": "YWJj"})

    def test_endpoint_validation(self):
        self.assertEqual(MODULE.chat_endpoint(None), "https://api.openai.com/v1/chat/completions")
        self.assertEqual(MODULE.chat_endpoint("https://example.com/chat/completions"), "https://example.com/chat/completions")
        with self.assertRaises(ValueError):
            MODULE.chat_endpoint("http://example.com/v1")


if __name__ == "__main__":
    unittest.main()
