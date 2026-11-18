#!/usr/bin/env python3
"""Generate pinned JS fixtures and independently check numeric regressions."""
from __future__ import annotations

import argparse
import hashlib
import importlib.util
import json
import math
import os
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
PACKAGE = ROOT / "internal" / "fingerprint"
GOLDEN = PACKAGE / "testdata" / "golden.json"
REFERENCE = PACKAGE / "reference"
COMMIT = "6cd4a8ff0f3f4e49d6d6969dd2fbadfa1569fde2"
TOLERANCE = 1e-12  # float64 regression budget, not an identity threshold
EVIDENCE = ROOT / "artifacts" / "rebuild-work-20261117" / "fingerprint"
PYTHON_SHA256 = "387fe5c44cc5f859995432267b0d3b462fc26e82419fc22e3ee7f36cc2502d98"


def corpus() -> dict:
    cases = [
        {"name": "one-min", "numbers": [1]},
        {"name": "one-max", "numbers": [355]},
        {"name": "two-endpoints", "numbers": [1, 355]},
        {"name": "three-repeats", "numbers": [1, 1, 355]},
        {"name": "four-blocks", "numbers": [1, 23, 24, 355]},
        {"name": "five-remainder", "numbers": [1, 355, 23, 24, 10]},
        {"name": "constant-min", "numbers": [1] * 292},
        {"name": "constant-max", "numbers": [355] * 332},
        {"name": "uniform-forward", "numbers": list(range(1, 356))},
        {"name": "uniform-reverse", "numbers": list(range(355, 0, -1))},
    ]
    edges = [1, 355]
    for value in range(2, 356):
        if ((value - 1) * 16) // 355 != ((value - 2) * 16) // 355:
            edges.extend([value - 1, value])
    cases.append({"name": "all-bin-edges", "numbers": edges * 3})
    for length in [7, 79, 80, 81, 161, 291, 292, 293, 311, 312, 313, 331, 332, 333]:
        state, numbers = 0x6CD4A8FF, []
        for _ in range(length):
            state = (1664525 * state + 1013904223) & 0xFFFFFFFF
            numbers.append(state % 355 + 1)
        cases.append({"name": f"lcg-{length}", "numbers": numbers})
    cases.extend([
        {"name": "last-digit-zero", "numbers": [10 * (i % 35 + 1) for i in range(312)]},
        {"name": "ordered-quadrants", "numbers": [1] * 73 + [89] * 73 + [178] * 73 + [355] * 73},
        {"name": "interleaved-quadrants", "numbers": [1, 89, 178, 355] * 73},
    ])
    return {"cases": cases, "batches": [
        {"name": "one-output", "case_names": ["lcg-292"]},
        {"name": "two-outputs", "case_names": ["lcg-292", "lcg-312"]},
        {"name": "three-outputs", "case_names": ["lcg-292", "lcg-312", "lcg-332"]},
        {"name": "four-outputs", "case_names": ["lcg-292", "lcg-312", "lcg-332", "constant-max"]},
    ]}


def js_golden() -> dict:
    process = subprocess.run(
        ["node", str(PACKAGE / "testdata" / "golden.mjs")],
        cwd=ROOT, input=json.dumps(corpus()), text=True,
        encoding="utf-8", capture_output=True, check=True,
    )
    return json.loads(process.stdout)


def counts(numbers: list[int]) -> list[int]:
    result = [0] * 355
    for number in numbers:
        result[number - 1] += 1
    return result


def hellinger(histogram: list[int]) -> list[float]:
    smoothed = [count + 0.5 for count in histogram]
    total = sum(smoothed)
    return [math.sqrt(value / total) for value in smoothed]


def ordered_feature(numbers: list[int]) -> list[float]:
    result, start = [], 0
    base, remainder = divmod(len(numbers), 4)
    for block in range(4):
        size = base + (block < remainder)
        bins = [0.5] * 16
        for number in numbers[start:start + size]:
            bins[((number - 1) * 16) // 355] += 1
        total = sum(bins)
        result.extend(math.sqrt(value / total) for value in bins)
        start += size
    last = [0.5] * 10
    for number in numbers:
        last[number % 10] += 1
    result.extend(math.sqrt(value / sum(last)) for value in last)
    return result


def standardize(values: list[float]) -> list[float]:
    center = sum(values) / len(values)
    scale = max(math.sqrt(sum((value - center) ** 2 for value in values) / len(values)), 1e-12)
    return [(value - center) / scale for value in values]


def dot(left: list[float], right: list[float]) -> float:
    return sum(a * b for a, b in zip(left, right, strict=True))


def unit(values: list[float]) -> list[float]:
    scale = max(math.sqrt(dot(values, values)), 1e-12)
    return [value / scale for value in values]


def scaled_feature(feature: list[float], artifact: dict) -> list[float]:
    return [(v - mean) / scale for v, mean, scale in zip(
        feature, artifact["feature_mean"], artifact["feature_scale"], strict=True)]


def project_simultaneously(values: list[float], basis: list[list[float]]) -> list[float]:
    # Independent Python path follows the original Python's matrix projection,
    # NOT Go/JS's sequential projection of the evolving residual.
    projections = [dot(values, vector) for vector in basis]
    return [value - sum(weight * vector[i] for weight, vector in zip(projections, basis, strict=True))
            for i, value in enumerate(values)]


def python_scores(numbers: list[int], bank: dict) -> tuple:
    artifact = bank["robust"]["hellinger"]
    scaled = scaled_feature(hellinger(counts(numbers)), artifact)
    feature = unit(project_simultaneously(scaled, artifact["nuisance_basis"]))
    # The pinned Python, unlike JS, standardizes marginal scores twice.
    marginal = standardize(standardize([dot(feature, c) for c in artifact["centroids"]]))
    artifact = bank["robust"]["ordered_blocks"]
    scaled = scaled_feature(ordered_feature(numbers), artifact)
    normalized = unit(scaled)
    template = standardize([max(dot(normalized, environment[i])
                                for environment in artifact["environment_centroids"])
                            for i in range(len(bank["models"]))])
    projected = unit(project_simultaneously(scaled, artifact["nuisance_basis"]))
    nuisance = standardize([dot(projected, c) for c in artifact["centroids"]])
    ordered = standardize([0.5 * t + 0.5 * n for t, n in zip(template, nuisance, strict=True)])
    weight = artifact["weight"]
    blended = [(1 - weight) * m + weight * o for m, o in zip(marginal, ordered, strict=True)]
    return marginal, ordered, blended


def distance(left: list[int], right: list[int]) -> float:
    p = [value / sum(left) for value in left]
    total = sum(right) + 0.5 * 355
    q = [(value + 0.5) / total for value in right]
    midpoint = [(a + b) / 2 for a, b in zip(p, q, strict=True)]
    divergence = lambda values: sum(value * math.log(value / middle)
                                    for value, middle in zip(values, midpoint, strict=True) if value)
    return math.sqrt(((divergence(p) + divergence(q)) / 2) / math.log(2))


def ranked(scores: list[float], histogram: list[int], bank: dict, distance_fn=distance) -> list[dict]:
    return sorted([{"model": model["id"], "family": model.get("family") or "models",
                    "score": score, "distance": distance_fn(histogram, model["counts"])}
                   for model, score in zip(bank["models"], scores, strict=True)],
                  key=lambda item: -item["score"])


def python_result(sample: dict, bank: dict, upstream=None) -> dict:
    numbers = sample["numbers"]
    if upstream:
        histogram = upstream.count_numbers(numbers)
        features = upstream.hellinger_feature(histogram).tolist()
        ordered_features = upstream.ordered_block_feature(numbers).tolist()
        scores = upstream.robust_score_numbers(numbers, bank)
        marginal, ordered, blended = scores["marginal_fused"], scores["ordered"], scores["fused"]
        distance_fn = lambda a, b: 1 - upstream.js_similarity(a, b)
    else:
        histogram = counts(numbers)
        features, ordered_features = hellinger(histogram), ordered_feature(numbers)
        marginal, ordered, blended = python_scores(numbers, bank)
        distance_fn = distance
    return {"name": sample["name"], "numbers": numbers, "features": features,
            "ordered_features": ordered_features, "marginal_scores": marginal,
            "ordered_scores": ordered, "candidates": ranked(blended, histogram, bank, distance_fn)}


def python_batches(bank: dict, upstream=None) -> list[dict]:
    by_name = {sample["name"]: sample["numbers"] for sample in corpus()["cases"]}
    results = []
    for batch in corpus()["batches"]:
        samples = [by_name[name] for name in batch["case_names"]]
        individual = [upstream.robust_score_numbers(numbers, bank)["fused"] if upstream
                      else python_scores(numbers, bank)[2] for numbers in samples]
        averaged = [sum(row[i] for row in individual) / len(individual) for i in range(len(bank["models"]))]
        pooled = counts([value for numbers in samples for value in numbers])
        distance_fn = (lambda a, b: 1 - upstream.js_similarity(a, b)) if upstream else distance
        results.append({**batch, "candidates": ranked(averaged, pooled, bank, distance_fn)})
    return results


def compare_vectors(label: str, actual: list[float], expected: list[float], errors: dict) -> None:
    if len(actual) != len(expected):
        raise AssertionError(f"{label} dimension mismatch: {len(actual)} != {len(expected)}")
    metric = errors.setdefault(label, {"max_abs": 0.0, "max_rel": 0.0})
    for index, (got, want) in enumerate(zip(actual, expected, strict=True)):
        absolute = abs(got - want)
        if not math.isfinite(got) or not math.isfinite(want) or absolute > TOLERANCE:
            raise AssertionError(f"{label}[{index}] got={got!r} want={want!r} abs_error={absolute!r}")
        metric["max_abs"] = max(metric["max_abs"], absolute)
        if want != 0:
            metric["max_rel"] = max(metric["max_rel"], absolute / abs(want))


def compare_candidates(actual: list[dict], expected: list[dict], errors: dict) -> None:
    if [(c["model"], c["family"]) for c in actual] != [(c["model"], c["family"]) for c in expected]:
        raise AssertionError("complete candidate model/family ordering differs")
    for field in ("score", "distance"):
        compare_vectors(field, [c[field] for c in actual], [c[field] for c in expected], errors)


def compare_results(actual: dict, expected: dict) -> dict:
    if len(actual["cases"]) != len(expected["cases"]) or len(actual["batches"]) != len(expected["batches"]):
        raise AssertionError("fixture case/batch count differs")
    errors = {}
    for got, want in zip(actual["cases"], expected["cases"], strict=True):
        if (got["name"], got["numbers"]) != (want["name"], want["numbers"]):
            raise AssertionError("deterministic corpus changed")
        for field in ("features", "ordered_features", "marginal_scores", "ordered_scores"):
            compare_vectors(field, got[field], want[field], errors)
        compare_candidates(got["candidates"], want["candidates"], errors)
    for got, want in zip(actual["batches"], expected["batches"], strict=True):
        if (got["name"], got["case_names"]) != (want["name"], want["case_names"]):
            raise AssertionError("deterministic batch changed")
        compare_candidates(got["candidates"], want["candidates"], errors)
    return {"ranks_equal": True, "errors": errors}


def load_upstream():
    path = REFERENCE / "fingerprint.py"
    if hashlib.sha256(path.read_bytes()).hexdigest() != PYTHON_SHA256:
        raise AssertionError("pinned Python source digest mismatch")
    sys.dont_write_bytecode = True
    spec = importlib.util.spec_from_file_location("modeltrace_pinned_reference", path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def check_challenges(actual: list[dict], upstream) -> None:
    class FixedChoices:
        def sample(self, population, count):
            if list(population) != list(range(292, 333)) or count != 3:
                raise AssertionError("legacy challenge population changed")
            return [292, 312, 332]

        def choice(self, values):
            return values[0]

    original_rng, original_token = upstream.random.SystemRandom, upstream.secrets.token_hex
    try:
        upstream.random.SystemRandom = FixedChoices
        upstream.secrets.token_hex = lambda _: "fixedfixture"
        expected = upstream.generate_challenges()
    finally:
        upstream.random.SystemRandom, upstream.secrets.token_hex = original_rng, original_token
    if len(actual) != 3:
        raise AssertionError("default challenge count differs")
    for got, want in zip(actual, expected, strict=True):
        if (got["prompt"], got["count"]) != (want["prompt"], want["expected_count"]):
            raise AssertionError("default prompt does not reproduce fixed original choices")
        if got["sha256"] != hashlib.sha256(got["prompt"].encode("utf-8")).hexdigest():
            raise AssertionError("challenge prompt digest mismatch")
        if got["parameters"] != {} or (got["min"], got["max"], got["version"], got["parser_version"]) != (1, 355, 1, "strict-integers-v1"):
            raise AssertionError("versioned legacy challenge contract changed")


def go_observations() -> dict:
    EVIDENCE.mkdir(parents=True, exist_ok=True)
    target = EVIDENCE / "go-observed.json"
    target.unlink(missing_ok=True)
    environment = {**os.environ, "FINGERPRINT_GO_OBSERVED": str(target)}
    command = ["go", "test", "./internal/fingerprint", "-run",
               "Test(GoldenParity|LegacyBankAccessors|LegacyReferenceIntegrity)", "-count=1", "-v"]
    process = subprocess.run(command, cwd=ROOT, env=environment, text=True,
                             encoding="utf-8", capture_output=True)
    (EVIDENCE / "go-golden.txt").write_text(
        "COMMAND: " + json.dumps(command) + "\n" + process.stdout + process.stderr +
        f"EXIT_STATUS={process.returncode}\n", encoding="utf-8")
    if process.returncode != 0:
        raise AssertionError(f"Go golden tests exited {process.returncode}; see {EVIDENCE / 'go-golden.txt'}")
    if not target.is_file():
        raise AssertionError("Go golden command produced no fresh observation report")
    return json.loads(target.read_text(encoding="utf-8"))


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--write", action="store_true", help="explicitly regenerate the pinned JS golden fixture")
    parser.add_argument("--upstream-python", action="store_true", help="also execute the pinned unmodified NumPy reference and fixed-choice challenge generator")
    args = parser.parse_args()
    EVIDENCE.mkdir(parents=True, exist_ok=True)
    (EVIDENCE / "regression.json").unlink(missing_ok=True)
    if args.write:
        golden = js_golden()
        GOLDEN.write_text(json.dumps(golden, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        print(f"WROTE {GOLDEN} cases={len(golden['cases'])} batches={len(golden['batches'])}")
        return
    expected = json.loads(GOLDEN.read_text(encoding="utf-8"))
    if (expected["schema"], expected["algorithm_version"], expected["upstream_commit"]) != (1, "modeltrace-6cd4a8ff-v1", COMMIT):
        raise AssertionError("pinned golden metadata changed")
    bank = json.loads((REFERENCE / "unified_bank.json").read_text(encoding="utf-8"))
    live = js_golden()
    if expected["canonical_sha256"] != live["canonical_sha256"]:
        raise AssertionError("source digests differ")
    go = go_observations()
    report = {"status": "PASS", "cases": len(expected["cases"]), "batches": len(expected["batches"]),
              "absolute_tolerance": TOLERANCE, "upstream_commit": COMMIT,
              "identity_validation": "not_performed", "comparisons": {
                  "live_js_vs_saved": compare_results(live, expected),
                  "go_vs_js": compare_results(go, expected),
                  "python_stdlib_vs_js": compare_results({
                      "cases": [python_result(s, bank) for s in corpus()["cases"]],
                      "batches": python_batches(bank)}, expected)}}
    if args.upstream_python:
        upstream = load_upstream()
        report["comparisons"]["upstream_python_vs_js"] = compare_results({
            "cases": [python_result(s, bank, upstream) for s in corpus()["cases"]],
            "batches": python_batches(bank, upstream)}, expected)
        check_challenges(go["challenges"], upstream)
        report["fixed_choice_challenges_equal"] = True
    raw_bank = (REFERENCE / "unified_bank.json").read_bytes()
    report["raw_bank_sha256"] = hashlib.sha256(raw_bank).hexdigest()
    report["canonical_bank_sha256"] = hashlib.sha256(raw_bank.replace(b"\r\n", b"\n")).hexdigest()
    (EVIDENCE / "regression.json").write_text(json.dumps(report, indent=2) + "\n", encoding="utf-8")
    for name, comparison in report["comparisons"].items():
        errors = comparison["errors"]
        print(f"{name}: ranks_equal=true " + " ".join(
            f"max_abs_{field}={errors[field]['max_abs']:.17g}" for field in
            ("features", "ordered_features", "score", "distance")))
    print(f"PASS cases={report['cases']} batches={report['batches']} abs_tolerance={TOLERANCE:g} "
          f"upstream_python={str(args.upstream_python).lower()} identity_validation=not_performed")
    print(f"REPORT {EVIDENCE / 'regression.json'}")


if __name__ == "__main__":
    try:
        main()
    except (AssertionError, OSError, ValueError, subprocess.SubprocessError, ImportError) as error:
        print(f"FAIL fingerprint regression: {error}", file=sys.stderr)
        sys.exit(1)
