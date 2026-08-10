#!/usr/bin/env python3
"""FunctionGemma adapter for the lifx-command-engine model contract."""

from __future__ import annotations

import argparse
import json
import re
import sys
from typing import Any


CONTRACT_VERSION = "1"
CALL_PATTERN = re.compile(
    r"<start_function_call>call:set_lifx_state\s*\{(.*?)\}"
    r"<end_function_call>",
    re.DOTALL,
)
LIGHTING_WORDS = re.compile(
    r"\b(light|lights|illuminate|brightness|bright|dim|colour|color|kelvin|warm|cool)\b",
    re.IGNORECASE,
)
STYLE_PROFILES = {
    "cozy": {"power": True, "brightness": 35.0, "saturation": 0.0, "kelvin": 2700},
    "movie": {"power": True, "brightness": 20.0},
}


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--model", required=True, help="local FunctionGemma model directory")
    parser.add_argument("--device", default="auto", help="auto, cpu, cuda, or mps")
    parser.add_argument("--max-new-tokens", type=int, default=512)
    parser.add_argument("--debug", action="store_true", help="write raw model generation to stderr")
    parser.add_argument("--serve", action="store_true", help="serve persistent JSONL requests")
    parser.add_argument(
        "--allow-download",
        action="store_true",
        help="allow Transformers to fetch missing model files",
    )
    parser.add_argument("--describe", action="store_true", help=argparse.SUPPRESS)
    return parser.parse_args()


def tool_schemas(model_input: dict[str, Any]) -> list[dict[str, Any]]:
    selectors, _ = selector_inventory(model_input)
    set_state = {
        "type": "function",
        "function": {
            "name": "set_lifx_state",
            "description": (
                "Propose new power or color state for known LIFX devices. Never "
                "execute it. Select the narrowest matching target. Available target "
                f"selectors: {', '.join(selectors)}"
            ),
            "parameters": {
                "type": "object",
                "properties": {
                    "target_selectors": {
                        "type": "array", "items": {"type": "string", "enum": selectors},
                        "description": (
                            "Semantic selectors from the inventory. Use device: for one "
                            "named light, group: for a group, and location: for a location."
                        ),
                    },
                    "power": {"type": "string", "enum": ["on", "off", "unchanged"]},
                    "hue": {"type": "number", "minimum": 0, "maximum": 360},
                    "saturation": {"type": "number", "minimum": 0, "maximum": 100},
                    "brightness": {"type": "number", "minimum": 0, "maximum": 100},
                    "kelvin": {"type": "integer", "minimum": 1500, "maximum": 9000},
                    "duration_ms": {"type": "integer", "minimum": 0},
                },
                "required": ["target_selectors"],
            },
        },
    }
    return [set_state]


def selector_inventory(model_input: dict[str, Any]) -> tuple[list[str], dict[str, list[str]]]:
    devices = model_input.get("snapshot", {}).get("devices", [])
    labels: dict[str, int] = {}
    for device in devices:
        if device.get("label"):
            labels[device["label"]] = labels.get(device["label"], 0) + 1

    mapping: dict[str, list[str]] = {}
    for device in devices:
        serial = device.get("serial")
        if not serial:
            continue
        label = device.get("label") or serial
        selector = f"device:{label}"
        if labels.get(label, 0) > 1:
            selector += f" [{serial}]"
        mapping[selector] = [serial]
        for kind in ("group", "location"):
            value = device.get(kind)
            if value:
                mapping.setdefault(f"{kind}:{value}", []).append(serial)

    for selector, serials in mapping.items():
        mapping[selector] = list(dict.fromkeys(serials))
    return list(mapping), mapping


def explicit_device_targets(model_input: dict[str, Any]) -> list[str]:
    text = model_input.get("text", "").casefold()
    matches: list[tuple[int, str]] = []
    for device in model_input.get("snapshot", {}).get("devices", []):
        label = device.get("label")
        serial = device.get("serial")
        if label and serial and re.search(rf"(?<!\w){re.escape(label.casefold())}(?!\w)", text):
            matches.append((len(label), serial))
    if not matches:
        return []
    longest = max(length for length, _ in matches)
    return list(dict.fromkeys(serial for length, serial in matches if length == longest))


def extract_plan(
    generated: str,
    source_text: str,
    selector_map: dict[str, list[str]] | None = None,
    explicit_targets: list[str] | None = None,
) -> dict[str, Any]:
    matches = CALL_PATTERN.findall(generated)
    if not matches:
        return empty_plan("model did not propose a LIFX function call")

    commands = []
    for body in matches:
        action: dict[str, Any] = {}
        power = escaped_value(body, "power")
        if power in ("on", "off"):
            action["power"] = power == "on"
        for field in ("hue", "saturation", "brightness"):
            value = numeric_value(body, field, float)
            if value is not None: action[field] = value
        for field in ("kelvin", "duration_ms"):
            value = numeric_value(body, field, int)
            if value is not None: action[field] = value
        selectors = escaped_array(body, "target_selectors")
        serials: list[str] = []
        for selector in selectors:
            serials.extend((selector_map or {}).get(selector, []))
        # Keep parsing the old field for compatibility with saved model traces.
        if not selectors:
            serials.extend(escaped_array(body, "target_serials"))
        if explicit_targets:
            serials = explicit_targets
        action = apply_action_policy(source_text, action)
        if serials and action:
            commands.append({
                "targets": [{"serial": serial} for serial in dict.fromkeys(serials)],
                "action": action,
            })
    if not commands:
        return empty_plan("model proposal had no valid target and action")
    return {
        "schema_version": "1",
        "confidence": 0.65,
        "confidence_result": {"level": "medium", "reasons": ["FunctionGemma proposal"]},
        "needs_confirmation": True,
        "summary": f"Model proposal for: {source_text}",
        "commands": merge_commands(commands),
    }


def merge_commands(commands: list[dict[str, Any]]) -> list[dict[str, Any]]:
    merged: dict[str, dict[str, Any]] = {}
    for command in commands:
        targets = []
        for target in command["targets"]:
            if target["serial"] not in targets: targets.append(target["serial"])
        key = json.dumps(sorted(targets), separators=(",", ":"))
        existing = merged.setdefault(key, {"targets": [{"serial": value} for value in targets], "action": {}})
        existing["action"].update(command["action"])
    return list(merged.values())


def apply_action_policy(text: str, proposed: dict[str, Any]) -> dict[str, Any]:
    normalized = text.casefold()
    for style, action in STYLE_PROFILES.items():
        if re.search(rf"\b{style}\b", normalized):
            return action.copy()

    action: dict[str, Any] = {}
    if re.search(r"\b(off|disable)\b", normalized):
        action["power"] = False
    elif re.search(r"\b(on|enable|illuminate)\b", normalized):
        action["power"] = True

    percentage = re.search(r"\b(\d{1,3}(?:\.\d+)?)\s*%", normalized)
    if percentage and re.search(r"\b(bright|brightness|dim)\b", normalized):
        action["brightness"] = min(100.0, float(percentage.group(1)))
    elif re.search(r"\b(dim|bright|brightness)\b", normalized) and "brightness" in proposed:
        action["brightness"] = proposed["brightness"]

    color_language = re.search(
        r"\b(hue|saturation|colo(?:u)?r|red|green|blue|yellow|purple|pink|orange|warm|cool|kelvin|white)\b",
        normalized,
    )
    if color_language:
        for field in ("hue", "saturation", "kelvin"):
            if field in proposed:
                action[field] = proposed[field]
    if re.search(r"\b(over|in|for)\s+\d+\s*(ms|milliseconds?|seconds?|minutes?)\b", normalized):
        if "duration_ms" in proposed:
            action["duration_ms"] = proposed["duration_ms"]
    return action


def empty_plan(reason: str) -> dict[str, Any]:
    return {
        "schema_version": "1",
        "confidence": 0.1,
        "confidence_result": {"level": "low", "reasons": [reason]},
        "needs_confirmation": True,
        "summary": "No supported LIFX command found",
        "commands": [],
    }


def request_is_relevant(model_input: dict[str, Any]) -> bool:
    text = model_input.get("text", "").casefold()
    if LIGHTING_WORDS.search(text): return True
    for device in model_input.get("snapshot", {}).get("devices", []):
        for value in (device.get("serial"), device.get("label"), device.get("group"), device.get("location")):
            if value and value.casefold() in text: return True
    return False


def escaped_value(body: str, field: str) -> str | None:
    match = re.search(rf"(?:^|,)\s*{re.escape(field)}\s*:\s*<escape>(.*?)<escape>", body, re.DOTALL)
    return match.group(1).strip() if match else None


def escaped_array(body: str, field: str) -> list[str]:
    match = re.search(rf"(?:^|,)\s*{re.escape(field)}\s*:\s*\[(.*?)\]", body, re.DOTALL)
    return re.findall(r"<escape>(.*?)<escape>", match.group(1), re.DOTALL) if match else []


def numeric_value(body: str, field: str, cast: Any) -> Any | None:
    match = re.search(rf"(?:^|,)\s*{re.escape(field)}\s*:\s*([^,}}]+)", body)
    if not match: return None
    try: return cast(match.group(1).strip())
    except ValueError: return None


def resolve_device(torch: Any, requested: str) -> str:
    if requested != "auto":
        return requested
    if torch.cuda.is_available():
        return "cuda"
    if getattr(torch.backends, "mps", None) and torch.backends.mps.is_available():
        return "mps"
    return "cpu"


class Runtime:
    def __init__(self, args: argparse.Namespace):
        try:
            import torch
            from transformers import AutoModelForCausalLM, AutoProcessor
        except ImportError as exc:
            raise RuntimeError(
                "missing FunctionGemma dependencies; install requirements.txt"
            ) from exc
        local_only = not args.allow_download
        self.torch = torch
        self.processor = AutoProcessor.from_pretrained(args.model, local_files_only=local_only)
        self.model = AutoModelForCausalLM.from_pretrained(
            args.model, dtype="auto", local_files_only=local_only
        )
        self.device = resolve_device(torch, args.device)
        self.model.to(self.device)
        self.args = args

    def generate(self, request: dict[str, Any], model_input: dict[str, Any]) -> dict[str, Any]:
        developer = request.get("developer_instruction", "")
        messages = [
            {
                "role": "developer",
                "content": (
                    "You are a model that can do function calling with the following "
                    f"functions. {developer}"
                ),
            },
            {"role": "user", "content": json.dumps(model_input, separators=(",", ":"))},
        ]
        inputs = self.processor.apply_chat_template(
            messages,
            tools=tool_schemas(model_input),
            add_generation_prompt=True,
            tokenize=True,
            return_dict=True,
            return_tensors="pt",
        ).to(self.device)
        with self.torch.inference_mode():
            output = self.model.generate(
                **inputs,
                max_new_tokens=self.args.max_new_tokens,
                do_sample=False,
                pad_token_id=self.processor.eos_token_id,
            )
        generated = self.processor.decode(
            output[0][inputs["input_ids"].shape[-1] :], skip_special_tokens=False
        )
        if self.args.debug:
            print(f"functiongemma raw generation: {generated!r}", file=sys.stderr)
        _, selector_map = selector_inventory(model_input)
        return extract_plan(
            generated,
            model_input["text"],
            selector_map,
            explicit_device_targets(model_input),
        )


def generate(
    request: dict[str, Any], args: argparse.Namespace, runtime: Runtime | None = None
) -> dict[str, Any]:
    model_input = request.get("input")
    if not isinstance(model_input, dict) or not isinstance(model_input.get("text"), str):
        raise ValueError("request.input.text must be a string")
    if not request_is_relevant(model_input):
        return empty_plan("request has no known target or lighting language")
    active_runtime = runtime or Runtime(args)
    return active_runtime.generate(request, model_input)


def validate_request(request: dict[str, Any]) -> None:
    if request.get("contract_version") != CONTRACT_VERSION:
        raise ValueError("unsupported model contract version")


def serve(
    runtime: Runtime, args: argparse.Namespace, stdin: Any, stdout: Any
) -> int:
    print(
        json.dumps({"type": "ready", "contract_version": CONTRACT_VERSION}),
        file=stdout,
        flush=True,
    )
    for line in stdin:
        try:
            request = json.loads(line)
            validate_request(request)
            response = {
                "contract_version": CONTRACT_VERSION,
                "result": generate(request, args, runtime),
            }
        except Exception as exc:
            response = {"contract_version": CONTRACT_VERSION, "error": str(exc)}
        print(json.dumps(response, separators=(",", ":")), file=stdout, flush=True)
    return 0


def main() -> int:
    args = parse_args()
    if args.describe:
        print(json.dumps({"name": "functiongemma-transformers", "contract_version": CONTRACT_VERSION}))
        return 0
    try:
        if args.serve:
            return serve(Runtime(args), args, sys.stdin, sys.stdout)
        request = json.load(sys.stdin)
        validate_request(request)
        print(json.dumps(generate(request, args), separators=(",", ":")))
        return 0
    except Exception as exc:  # runtime errors belong on stderr for the Go adapter
        print(f"functiongemma runner: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
