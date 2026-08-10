import json
import argparse
import io
import unittest

from runner import extract_plan, request_is_relevant, serve, tool_schemas


class RunnerTest(unittest.TestCase):
    def test_extracts_plan_json(self):
        generated = (
            "<start_function_call>call:set_lifx_state{"
            "target_serials:[<escape>d073d5000001<escape>],"
            "power:<escape>on<escape>,brightness:35}"
            "<end_function_call>"
        )
        got = extract_plan(generated, "desk on")
        self.assertEqual(got["commands"][0]["targets"][0]["serial"], "d073d5000001")
        self.assertTrue(got["commands"][0]["action"]["power"])
        self.assertEqual(got["commands"][0]["action"]["brightness"], 35)
        self.assertTrue(got["needs_confirmation"])

    def test_plain_response_becomes_low_confidence_empty_plan(self):
        got = extract_plan("I cannot do that", "play jazz")
        self.assertEqual(got["commands"], [])
        self.assertEqual(got["confidence_result"]["level"], "low")

    def test_merges_parallel_refinements_for_same_targets(self):
        first = (
            "<start_function_call>call:set_lifx_state{target_serials:["
            "<escape>a<escape>,<escape>b<escape>],brightness:100}"
            "<end_function_call>"
        )
        second = (
            "<start_function_call>call:set_lifx_state{target_serials:["
            "<escape>a<escape>,<escape>b<escape>],brightness:35}"
            "<end_function_call>"
        )
        got = extract_plan(first + second, "dim lights")
        self.assertEqual(len(got["commands"]), 1)
        self.assertEqual(got["commands"][0]["targets"], [{"serial": "a"}, {"serial": "b"}])
        self.assertEqual(got["commands"][0]["action"]["brightness"], 35)

    def test_tool_schema_is_non_executing_proposal(self):
        schemas = tool_schemas({"snapshot": {"devices": [{"serial": "abc", "label": "Desk"}]}})
        self.assertEqual(schemas[0]["function"]["name"], "set_lifx_state")
        self.assertIn("Never execute", schemas[0]["function"]["description"])
        serial_schema = schemas[0]["function"]["parameters"]["properties"]["target_serials"]
        self.assertEqual(serial_schema["items"]["enum"], ["abc"])

    def test_relevance_requires_selector_or_lighting_language(self):
        snapshot = {"devices": [{"serial": "abc", "label": "Desk", "group": "Office"}]}
        self.assertTrue(request_is_relevant({"text": "turn desk on", "snapshot": snapshot}))
        self.assertTrue(request_is_relevant({"text": "illuminate my work area", "snapshot": snapshot}))
        self.assertFalse(request_is_relevant({"text": "turn garage on", "snapshot": snapshot}))
        self.assertFalse(request_is_relevant({"text": "play jazz", "snapshot": snapshot}))

    def test_persistent_server_handshake_results_and_errors(self):
        class FakeRuntime:
            def generate(self, request, model_input):
                return {"text": model_input["text"]}

        requests = "\n".join([
            json.dumps({"contract_version": "1", "input": {"text": "dim lights", "snapshot": {}}}),
            json.dumps({"contract_version": "2", "input": {"text": "bad"}}),
        ])
        output = io.StringIO()
        args = argparse.Namespace()
        self.assertEqual(serve(FakeRuntime(), args, io.StringIO(requests), output), 0)
        messages = [json.loads(line) for line in output.getvalue().splitlines()]
        self.assertEqual(messages[0], {"type": "ready", "contract_version": "1"})
        self.assertEqual(messages[1]["result"], {"text": "dim lights"})
        self.assertIn("unsupported", messages[2]["error"])


if __name__ == "__main__":
    unittest.main()
