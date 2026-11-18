#!/usr/bin/env python3
"""Regression-driver guard tests; no reference source or golden is edited."""
from __future__ import annotations

import importlib.util
import json
import sys
import tempfile
import types
import unittest
from pathlib import Path
from unittest.mock import patch

sys.dont_write_bytecode = True
ROOT = Path(__file__).resolve().parents[3]
SPEC = importlib.util.spec_from_file_location("fingerprint_regression", ROOT / "scripts" / "fingerprint-golden.py")
REGRESSION = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(REGRESSION)


class RegressionDriverTests(unittest.TestCase):
    def test_detects_numeric_drift(self):
        with self.assertRaises(AssertionError):
            REGRESSION.compare_vectors("score", [1.01], [1.0], {})

    def test_detects_nonfinite(self):
        with self.assertRaises(AssertionError):
            REGRESSION.compare_vectors("score", [float("nan")], [1.0], {})

    def test_detects_complete_rank_drift(self):
        candidates = [{"model": "a", "family": "f", "score": 1.0, "distance": 0.1},
                      {"model": "b", "family": "f", "score": 0.0, "distance": 0.2}]
        with self.assertRaises(AssertionError):
            REGRESSION.compare_candidates(list(reversed(candidates)), candidates, {})

    def test_rejects_stale_go_report_after_zero_exit(self):
        REGRESSION.EVIDENCE.mkdir(parents=True, exist_ok=True)
        # A successful child exit without writing an observation is a stub of
        # that external failure mode; the stale file and read boundary are real.
        with tempfile.TemporaryDirectory(dir=REGRESSION.EVIDENCE) as directory:
            evidence = Path(directory)
            self.assertTrue(evidence.resolve().is_relative_to(REGRESSION.EVIDENCE.resolve()))
            (evidence / "go-observed.json").write_text(json.dumps({"cases": [], "batches": []}), encoding="utf-8")
            process = types.SimpleNamespace(returncode=0, stdout="", stderr="")
            with patch.object(REGRESSION, "EVIDENCE", evidence), patch.object(REGRESSION.subprocess, "run", return_value=process):
                with self.assertRaisesRegex(AssertionError, "fresh"):
                    REGRESSION.go_observations()

    def test_failed_main_removes_old_pass_report(self):
        REGRESSION.EVIDENCE.mkdir(parents=True, exist_ok=True)
        with tempfile.TemporaryDirectory(dir=REGRESSION.EVIDENCE) as directory:
            evidence = Path(directory)
            self.assertTrue(evidence.resolve().is_relative_to(REGRESSION.EVIDENCE.resolve()))
            report = evidence / "regression.json"
            report.write_text(json.dumps({"status": "PASS"}), encoding="utf-8")
            golden = json.loads(REGRESSION.GOLDEN.read_text(encoding="utf-8"))
            with patch.object(REGRESSION, "EVIDENCE", evidence), patch.object(sys, "argv", ["fingerprint-golden.py"]), \
                    patch.object(REGRESSION, "js_golden", return_value=golden), \
                    patch.object(REGRESSION, "go_observations", side_effect=AssertionError("fresh report missing")):
                with self.assertRaisesRegex(AssertionError, "fresh"):
                    REGRESSION.main()
            self.assertFalse(report.exists(), "a failed run must not leave a stale PASS report")


if __name__ == "__main__":
    unittest.main(verbosity=2)
