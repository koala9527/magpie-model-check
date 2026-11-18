// Numeric baseline adapter for unmodified ModelTrace at the pinned commit.
// It only removes ES export markers in memory to expose private numeric helpers.
import { readFileSync } from "node:fs";
import { createHash } from "node:crypto";
import { fileURLToPath } from "node:url";
import vm from "node:vm";

const reference = new URL("../reference/", import.meta.url);
const expected = {
  "fingerprint-core.js": "83fa5bd611e18f8339122582335123c8ea168ed242298bb31f4e363abeeb6e4a",
  "unified_bank.json": "f267d0513aa06981d024ec5394746fceddf44e4e8a75fa6bf8304255f4b32d6d",
};
const files = {};
for (const [name, hash] of Object.entries(expected)) {
  const bytes = readFileSync(new URL(name, reference));
  const canonical = bytes.toString("utf8").replaceAll("\r\n", "\n");
  if (createHash("sha256").update(canonical).digest("hex") !== hash) {
    throw new Error(`pinned reference hash mismatch: ${name}`);
  }
  files[name] = canonical;
}
const bank = JSON.parse(files["unified_bank.json"]);
const core = vm.runInNewContext(
  files["fingerprint-core.js"].replace(/^export /gm, "") +
    "\n({ countNumbers, hellingerFeature, orderedBlockFeature, robustScoreCounts, orderedBlockScores, robustScoreNumbers, jsSimilarity, analyzeGlobalOutputs });",
  {},
  { filename: fileURLToPath(new URL("fingerprint-core.js", reference)) },
);
const input = JSON.parse(readFileSync(0, "utf8"));
const candidates = (scores, counts) => bank.models.map((model, i) => ({
  model: model.id,
  family: model.family || "models",
  score: scores[i],
  distance: 1 - core.jsSimilarity(counts, model.counts),
})).sort((a, b) => b.score - a.score);

const cases = input.cases.map(({ name, numbers }) => {
  const counts = core.countNumbers(numbers);
  return {
    name,
    numbers,
    features: core.hellingerFeature(counts),
    ordered_features: core.orderedBlockFeature(numbers),
    marginal_scores: core.robustScoreCounts(counts, bank),
    ordered_scores: core.orderedBlockScores(numbers, bank),
    candidates: candidates(core.robustScoreNumbers(numbers, bank), counts),
  };
});
const byName = Object.fromEntries(cases.map((item) => [item.name, item]));
const batches = (input.batches || []).map(({ name, case_names }) => {
  const outputs = case_names.map((key) => ({
    text: byName[key].numbers.join(","), expected_count: byName[key].numbers.length,
  }));
  // Exercise the original public multi-output path, but do not export its
  // closed-set softmax fields as identity probabilities.
  const result = core.analyzeGlobalOutputs(outputs, bank);
  if (result.used_outputs !== case_names.length) throw new Error("invalid batch fixture");
  return {
    name, case_names,
    candidates: result.results.map((item) => ({
      model: item.model, family: item.family, score: item.score,
      distance: 1 - item.profile_similarity,
    })),
  };
});
process.stdout.write(JSON.stringify({
  schema: 1,
  algorithm_version: "modeltrace-6cd4a8ff-v1",
  upstream_commit: "6cd4a8ff0f3f4e49d6d6969dd2fbadfa1569fde2",
  canonical_sha256: expected,
  cases,
  batches,
}) + "\n");
