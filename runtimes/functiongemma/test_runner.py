import json
import unittest

from runner import extract_plan, tool_schema


class RunnerTest(unittest.TestCase):
    def test_extracts_plan_json(self):
        expected = {
            "schema_version": "1",
            "confidence": 0.8,
            "confidence_result": {"level": "high", "reasons": ["match"]},
            "needs_confirmation": False,
            "summary": "Turn on Desk",
            "commands": [],
        }
        generated = (
            "<start_function_call>call:propose_lifx_plan{plan_json:<escape>"
            + json.dumps(expected)
            + "<escape>}<end_function_call>"
        )
        got = extract_plan(generated, "desk on")
        self.assertEqual(got["summary"], "Turn on Desk")
        self.assertTrue(got["needs_confirmation"])

    def test_plain_response_becomes_low_confidence_empty_plan(self):
        got = extract_plan("I cannot do that", "play jazz")
        self.assertEqual(got["commands"], [])
        self.assertEqual(got["confidence_result"]["level"], "low")

    def test_invalid_plan_json_fails(self):
        generated = (
            "<start_function_call>call:propose_lifx_plan{plan_json:<escape>{bad}"
            "<escape>}<end_function_call>"
        )
        with self.assertRaisesRegex(ValueError, "invalid JSON"):
            extract_plan(generated, "desk on")

    def test_tool_schema_is_non_executing_proposal(self):
        schema = tool_schema("CommandPlan v1")
        self.assertEqual(schema["function"]["name"], "propose_lifx_plan")
        self.assertIn("never execute", schema["function"]["description"])


if __name__ == "__main__":
    unittest.main()
