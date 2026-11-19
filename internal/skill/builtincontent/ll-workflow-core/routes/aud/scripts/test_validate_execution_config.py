#!/usr/bin/env python3
"""Unit tests for validate_execution_config.py.

Run:
    python -m unittest .github.skills.ll-audit.scripts.test_validate_execution_config
"""

from __future__ import annotations

import importlib.util
import json
import tempfile
import unittest
from pathlib import Path


def _load_module():
    script = Path(__file__).with_name("validate_execution_config.py")
    spec = importlib.util.spec_from_file_location("validate_execution_config", script)
    if spec is None or spec.loader is None:
        raise RuntimeError("cannot load validate_execution_config.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class ValidateExecutionConfigTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.mod = _load_module()
        try:
            import yaml  # noqa: F401
            import jsonschema  # noqa: F401
        except Exception as exc:  # pragma: no cover
            raise unittest.SkipTest(f"missing runtime deps: {exc}")

    def test_validate_success(self):
        schema = {
            "type": "object",
            "properties": {"mode": {"type": "string"}},
            "required": ["mode"],
            "additionalProperties": False,
        }
        data = {"mode": "ok"}
        self.mod._validate(data, schema)

    def test_validate_failure(self):
        schema = {
            "type": "object",
            "properties": {"mode": {"type": "string"}},
            "required": ["mode"],
            "additionalProperties": False,
        }
        data = {"mode": 1}
        with self.assertRaises(Exception):
            self.mod._validate(data, schema)

    def test_load_json(self):
        with tempfile.TemporaryDirectory() as td:
            p = Path(td) / "x.json"
            p.write_text(json.dumps({"a": 1}), encoding="utf-8")
            d = self.mod._load_json(p)
            self.assertEqual(d["a"], 1)


if __name__ == "__main__":
    unittest.main()
