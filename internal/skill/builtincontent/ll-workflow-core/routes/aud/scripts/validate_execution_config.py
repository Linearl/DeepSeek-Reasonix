#!/usr/bin/env python3
"""Validate ll-audit execution.yaml against execution.schema.json.

Usage:
  python validate_execution_config.py --config ../config/execution.yaml --schema ../config/execution.schema.json
"""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path


def _load_yaml(path: Path):
    try:
        import yaml  # type: ignore
    except Exception as exc:  # pragma: no cover
        raise RuntimeError("PyYAML is required: pip install pyyaml") from exc

    with path.open("r", encoding="utf-8") as f:
        return yaml.safe_load(f)


def _load_json(path: Path):
    with path.open("r", encoding="utf-8") as f:
        return json.load(f)


def _validate(data, schema):
    try:
        import jsonschema  # type: ignore
    except Exception as exc:  # pragma: no cover
        raise RuntimeError("jsonschema is required: pip install jsonschema") from exc

    jsonschema.validate(instance=data, schema=schema)


def main() -> int:
    parser = argparse.ArgumentParser(description="Validate execution.yaml for ll-audit")
    parser.add_argument("--config", required=True, help="Path to execution.yaml")
    parser.add_argument("--schema", required=True, help="Path to execution.schema.json")
    args = parser.parse_args()

    config_path = Path(args.config).resolve()
    schema_path = Path(args.schema).resolve()

    if not config_path.exists():
        print(f"[ERROR] config not found: {config_path}")
        return 2
    if not schema_path.exists():
        print(f"[ERROR] schema not found: {schema_path}")
        return 2

    try:
        data = _load_yaml(config_path)
        schema = _load_json(schema_path)
        _validate(data, schema)
    except Exception as exc:
        print(f"[INVALID] {exc}")
        return 1

    print("[VALID] execution.yaml passed schema validation")
    return 0


if __name__ == "__main__":
    sys.exit(main())
