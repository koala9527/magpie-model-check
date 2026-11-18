# Pinned ModelTrace numeric reference

Source: https://github.com/xqy2006/ModelTrace/tree/6cd4a8ff0f3f4e49d6d6969dd2fbadfa1569fde2

MIT License, Copyright (c) 2026 xqy2006. The complete notice is retained in `LICENSE`.

| File | Source at the pinned commit | Canonical LF SHA-256 |
| --- | --- | --- |
| `fingerprint-core.js` | `static/fingerprint-core.js` | `83fa5bd611e18f8339122582335123c8ea168ed242298bb31f4e363abeeb6e4a` |
| `unified_bank.json` | `static/data/unified_bank.json` | `f267d0513aa06981d024ec5394746fceddf44e4e8a75fa6bf8304255f4b32d6d` |
| `fingerprint.py` | `fingerprint.py` | `387fe5c44cc5f859995432267b0d3b462fc26e82419fc22e3ee7f36cc2502d98` |
| `LICENSE` | `LICENSE` | `238afb57e498990742f84b9ce6167b4e256ac06104a9938ce953bc41c14bea51` |

JS and bank are byte-preserving copies of the existing `internal/check/web/modeltrace` assets. Python is a byte-preserving copy of the previously downloaded pinned source. No reference source is patched. The JS test adapter removes only ES export markers in memory to expose existing private numeric functions.

Only the JSON bank is embedded into the Go runtime. JS, Python and the test adapter are offline regression references, not runtime dependencies.

`LegacyBankJSON()` exports the original embedded bytes as an independent copy. `LegacyBankSHA256()` hashes those exact bytes, including line endings; its raw digest can differ from the canonical LF digest above. Both digests are recorded in the regression evidence.

The bank is a legacy closed-set numerical baseline containing 18 candidates. Its labels, calibration fields, collection metadata and similarity scores are not verified model-identity credentials. No identity probability, statistical sample-adequacy claim or calibrated decision cutoff is supplied by this package.
