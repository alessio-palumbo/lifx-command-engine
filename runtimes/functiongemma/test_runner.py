import json
import argparse
import io
import unittest

from runner import (
    apply_action_policy,
    explicit_device_targets,
    extract_plan,
    request_is_relevant,
    selector_inventory,
    serve,
    tool_schemas,
)


class RunnerTest(unittest.TestCase):
    def test_extracts_plan_json(self):
        generated = (
            "<start_function_call>call:set_lifx_state{"
            "target_selectors:[<escape>device:Desk<escape>],"
            "power:<escape>on<escape>,brightness:35}"
            "<end_function_call>"
        )
        got = extract_plan(generated, "desk brightness 35%", {"device:Desk": ["d073d5000001"]})
        self.assertEqual(got["commands"][0]["targets"][0]["serial"], "d073d5000001")
        self.assertEqual(got["commands"][0]["action"]["brightness"], 35)
        self.assertTrue(got["needs_confirmation"])

    def test_plain_response_becomes_low_confidence_empty_plan(self):
        got = extract_plan("I cannot do that", "play jazz")
        self.assertEqual(got["commands"], [])
        self.assertEqual(got["confidence_result"]["level"], "low")

    def test_merges_parallel_refinements_for_same_targets(self):
        first = (
            "<start_function_call>call:set_lifx_state{target_selectors:["
            "<escape>group:Office<escape>],brightness:100}"
            "<end_function_call>"
        )
        second = (
            "<start_function_call>call:set_lifx_state{target_selectors:["
            "<escape>group:Office<escape>],brightness:35}"
            "<end_function_call>"
        )
        got = extract_plan(first + second, "dim lights", {"group:Office": ["a", "b"]})
        self.assertEqual(len(got["commands"]), 1)
        self.assertEqual(got["commands"][0]["targets"], [{"serial": "a"}, {"serial": "b"}])
        self.assertEqual(got["commands"][0]["action"]["brightness"], 35)

    def test_tool_schema_is_non_executing_proposal(self):
        schemas = tool_schemas({"snapshot": {"devices": [{"serial": "abc", "label": "Desk"}]}})
        self.assertEqual(schemas[0]["function"]["name"], "set_lifx_state")
        self.assertIn("Never execute", schemas[0]["function"]["description"])
        selector_schema = schemas[0]["function"]["parameters"]["properties"]["target_selectors"]
        self.assertEqual(selector_schema["items"]["enum"], ["device:Desk"])

    def test_selector_inventory_expands_hierarchy_deterministically(self):
        model_input = {"snapshot": {"devices": [
            {"serial": "a", "label": "Desk", "group": "Office", "location": "Home"},
            {"serial": "b", "label": "Shelf", "group": "Office", "location": "Home"},
        ]}}
        selectors, mapping = selector_inventory(model_input)
        self.assertEqual(selectors, ["device:Desk", "group:Office", "location:Home", "device:Shelf"])
        self.assertEqual(mapping["device:Desk"], ["a"])
        self.assertEqual(mapping["group:Office"], ["a", "b"])

    def test_explicit_device_label_narrows_model_target(self):
        model_input = {"text": "make my desk light cozy", "snapshot": {"devices": [
            {"serial": "a", "label": "Desk", "group": "Office"},
            {"serial": "b", "label": "Shelf", "group": "Office"},
        ]}}
        self.assertEqual(explicit_device_targets(model_input), ["a"])
        generated = (
            "<start_function_call>call:set_lifx_state{target_selectors:["
            "<escape>group:Office<escape>],brightness:5}<end_function_call>"
        )
        got = extract_plan(
            generated,
            model_input["text"],
            {"group:Office": ["a", "b"]},
            explicit_device_targets(model_input),
        )
        self.assertEqual(got["commands"][0]["targets"], [{"serial": "a"}])

    def test_action_policy_strips_unsolicited_fields(self):
        proposed = {"power": True, "brightness": 100, "saturation": 100}
        self.assertEqual(apply_action_policy("turn desk on", proposed), {"power": True})

    def test_action_policy_defines_style_profiles(self):
        self.assertEqual(
            apply_action_policy("make desk cozy", {"brightness": 5}),
            {"power": True, "brightness": 35.0, "saturation": 0.0, "kelvin": 2700},
        )
        self.assertEqual(
            apply_action_policy("dim office for movie time", {"brightness": 100}),
            {"power": True, "brightness": 20.0},
        )

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
