#!/usr/bin/env python3
"""Turn `go test -json` output into GitHub check-run annotations.

Actions job logs require repository admin rights to download through the API;
annotations are readable for public repositories and expose the failing test
names and their output without that permission.
"""

import json
import sys


def load(path: str) -> dict[tuple[str, str], str]:
    failed: dict[tuple[str, str], str] = {}
    outputs: dict[tuple[str, str], str] = {}
    with open(path, encoding="utf-8", errors="replace") as handle:
        for line in handle:
            line = line.strip()
            if not line.startswith("{"):
                continue
            try:
                event = json.loads(line)
            except json.JSONDecodeError:
                continue
            pkg = event.get("Package") or ""
            test = event.get("Test")
            action = event.get("Action")
            key = (pkg, test or "")
            if action == "output":
                text = event.get("Output") or ""
                outputs[key] = (outputs.get(key, "") + text)[-4000:]
            elif action == "fail":
                failed[key] = outputs.get(key, "")
    return failed


def main() -> int:
    path = sys.argv[1] if len(sys.argv) > 1 else "/tmp/go-test.json"
    try:
        failed = load(path)
    except OSError as exc:
        print(f"::error::cannot read go test output: {exc}")
        return 0
    if not failed:
        print("::warning::no failing tests found in go test JSON output")
        return 0
    for (pkg, test), output in sorted(failed.items()):
        name = test or "(package build)"
        message = output.replace("%", "%25").replace("\r", "").replace("\n", "%0A")
        print(f"::error file={pkg or 'go.mod'}::{pkg} {name}%0A{message}")
    print(f"::error::{len(failed)} go test target(s) failed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
