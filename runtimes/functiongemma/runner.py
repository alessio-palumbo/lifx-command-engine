#!/usr/bin/env python3
"""One-shot FunctionGemma adapter for the lifx-command-engine model contract."""

from __future__ import annotations

import argparse
import json
import re
import sys
from typing import Any


CONTRACT_VERSION = "1"
CALL_PATTERN = re.compile(
    r"<start_function_call>call:propose_lifx_plan\s*\{\s*"
    r"plan_json\s*:\s*<escape>(.*?)<escape>\s*\}"
    r"<end_function_call>",
    re.DOTALL,
)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--model", required=True, help="local FunctionGemma model directory")
    parser.add_argument("--device", default="auto", help="auto, cpu, cuda, or mps")
    parser.add_argument("--max-new-tokens", type=int, default=512)
    parser.add_argument(
        "--allow-download",
        action="store_true",
        help="allow Transformers to fetch missing model files",
    )
    parser.add_argument("--describe", action="store_true", help=argparse.SUPPRESS)
    return parser.parse_args()


def tool_schema(output_schema: str) -> dict[str, Any]:
    return {
        "type": "function",
        "function": {
            "name": "propose_lifx_plan",
            "description": (
                "Propose, but never execute, a LIFX command plan. The plan_json "
                "argument must be valid JSON matching this contract: " + output_schema
            ),
            "parameters": {
                "type": "object",
                "properties": {
                    "plan_json": {
                        "type": "string",
                        "description": "Complete CommandPlan encoded as strict JSON.",
                    }
                },
                "required": ["plan_json"],
            },
        },
    }


def extract_plan(generated: str, source_text: str) -> dict[str, Any]:
    match = CALL_PATTERN.search(generated)
    if match is None:
        return {
            "schema_version": "1",
            "confidence": 0.1,
            "confidence_result": {
                "level": "low",
                "reasons": ["model did not propose a LIFX function call"],
            },
            "needs_confirmation": True,
            "summary": "No supported LIFX command found",
            "commands": [],
        }

    try:
        plan = json.loads(match.group(1))
    except json.JSONDecodeError as exc:
        raise ValueError(f"model plan_json is invalid JSON: {exc}") from exc
    if not isinstance(plan, dict):
        raise ValueError("model plan_json must contain a JSON object")

    plan.setdefault("schema_version", "1")
    plan.setdefault("confidence", 0.65)
    plan.setdefault(
        "confidence_result",
        {"level": "medium", "reasons": ["FunctionGemma proposal"]},
    )
    plan["needs_confirmation"] = True
    plan.setdefault("summary", f"Model proposal for: {source_text}")
    plan.setdefault("commands", [])
    return plan


def resolve_device(torch: Any, requested: str) -> str:
    if requested != "auto":
        return requested
    if torch.cuda.is_available():
        return "cuda"
    if getattr(torch.backends, "mps", None) and torch.backends.mps.is_available():
        return "mps"
    return "cpu"


def generate(request: dict[str, Any], args: argparse.Namespace) -> dict[str, Any]:
    try:
        import torch
        from transformers import AutoModelForCausalLM, AutoProcessor
    except ImportError as exc:
        raise RuntimeError(
            "missing FunctionGemma dependencies; install requirements.txt"
        ) from exc

    model_input = request.get("input")
    if not isinstance(model_input, dict) or not isinstance(model_input.get("text"), str):
        raise ValueError("request.input.text must be a string")

    local_only = not args.allow_download
    processor = AutoProcessor.from_pretrained(args.model, local_files_only=local_only)
    model = AutoModelForCausalLM.from_pretrained(
        args.model, dtype="auto", local_files_only=local_only
    )
    device = resolve_device(torch, args.device)
    model.to(device)

    developer = request.get("developer_instruction", "")
    messages = [
        {
            "role": "developer",
            "content": (
                "You are a model that can do function calling with the following "
                f"functions. {developer}"
            ),
        },
        {
            "role": "user",
            "content": json.dumps(model_input, separators=(",", ":")),
        },
    ]
    inputs = processor.apply_chat_template(
        messages,
        tools=[tool_schema(request.get("output_schema", ""))],
        add_generation_prompt=True,
        tokenize=True,
        return_dict=True,
        return_tensors="pt",
    ).to(device)
    with torch.inference_mode():
        output = model.generate(
            **inputs,
            max_new_tokens=args.max_new_tokens,
            do_sample=False,
            pad_token_id=processor.eos_token_id,
        )
    generated = processor.decode(
        output[0][inputs["input_ids"].shape[-1] :], skip_special_tokens=False
    )
    return extract_plan(generated, model_input["text"])


def main() -> int:
    args = parse_args()
    if args.describe:
        print(json.dumps({"name": "functiongemma-transformers", "contract_version": CONTRACT_VERSION}))
        return 0
    try:
        request = json.load(sys.stdin)
        if request.get("contract_version") != CONTRACT_VERSION:
            raise ValueError("unsupported model contract version")
        print(json.dumps(generate(request, args), separators=(",", ":")))
        return 0
    except Exception as exc:  # runtime errors belong on stderr for the Go adapter
        print(f"functiongemma runner: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
