package fingerprint

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"sync"
)

//go:embed reference/unified_bank.json
var legacyBankJSON []byte

var legacyBankRawSHA256 = fmt.Sprintf("%x", sha256.Sum256(legacyBankJSON))

// LegacyBankJSON returns an independent copy of the unmodified embedded legacy
// bank. Original UTF-8 bytes and line endings are preserved for exact exports.
// This numeric reference artifact is not a verified model-identity credential.
func LegacyBankJSON() []byte {
	return bytes.Clone(legacyBankJSON)
}

// LegacyBankSHA256 returns the lowercase SHA-256 of LegacyBankJSON's raw bytes.
// It is deliberately not the canonical-LF digest used to pin upstream content.
func LegacyBankSHA256() string {
	return legacyBankRawSHA256
}

type referenceModel struct {
	ID     string    `json:"id"`
	Family string    `json:"family"`
	Counts []float64 `json:"counts"`
}

type numericArtifact struct {
	FeatureMean   []float64   `json:"feature_mean"`
	FeatureScale  []float64   `json:"feature_scale"`
	NuisanceBasis [][]float64 `json:"nuisance_basis"`
	Centroids     [][]float64 `json:"centroids"`
}

type orderedArtifact struct {
	numericArtifact
	Weight               float64       `json:"weight"`
	EnvironmentCentroids [][][]float64 `json:"environment_centroids"`
}

type referenceBank struct {
	Models []referenceModel `json:"models"`
	Robust struct {
		ModelOrder    []string         `json:"model_order"`
		Hellinger     numericArtifact  `json:"hellinger"`
		OrderedBlocks *orderedArtifact `json:"ordered_blocks"`
	} `json:"robust"`
}

var cachedLegacyBank = sync.OnceValues(func() (*referenceBank, error) {
	canonical := bytes.ReplaceAll(legacyBankJSON, []byte("\r\n"), []byte("\n"))
	if got := fmt.Sprintf("%x", sha256.Sum256(canonical)); got != "f267d0513aa06981d024ec5394746fceddf44e4e8a75fa6bf8304255f4b32d6d" {
		return nil, fmt.Errorf("fingerprint: pinned legacy bank LF SHA-256 mismatch: %s", got)
	}
	return decodeBank(legacyBankJSON)
})

func loadLegacyBank() (*referenceBank, error) {
	return cachedLegacyBank()
}

func decodeBank(raw []byte) (*referenceBank, error) {
	var bank referenceBank
	if err := json.Unmarshal(raw, &bank); err != nil {
		return nil, fmt.Errorf("fingerprint: decode legacy bank: %w", err)
	}
	modelCount := len(bank.Models)
	if modelCount == 0 || len(bank.Robust.ModelOrder) != modelCount {
		return nil, fmt.Errorf("fingerprint: invalid legacy model order")
	}
	seen := make(map[string]bool, modelCount)
	for i, model := range bank.Models {
		if model.ID == "" || seen[model.ID] || model.ID != bank.Robust.ModelOrder[i] {
			return nil, fmt.Errorf("fingerprint: invalid legacy model at index %d", i)
		}
		seen[model.ID] = true
		if err := validateVector("counts", model.Counts, featureDimension); err != nil {
			return nil, err
		}
		for _, count := range model.Counts {
			if count < 0 {
				return nil, fmt.Errorf("fingerprint: negative legacy count")
			}
		}
	}
	if err := validateArtifact(bank.Robust.Hellinger, featureDimension, modelCount); err != nil {
		return nil, err
	}
	if ordered := bank.Robust.OrderedBlocks; ordered != nil {
		if math.IsNaN(ordered.Weight) || math.IsInf(ordered.Weight, 0) || ordered.Weight < 0 || ordered.Weight > 1 {
			return nil, fmt.Errorf("fingerprint: invalid ordered weight")
		}
		if err := validateArtifact(ordered.numericArtifact, orderedDimension, modelCount); err != nil {
			return nil, err
		}
		if len(ordered.EnvironmentCentroids) == 0 {
			return nil, fmt.Errorf("fingerprint: missing ordered environments")
		}
		for _, centroids := range ordered.EnvironmentCentroids {
			if err := validateCentroids(centroids, orderedDimension, modelCount); err != nil {
				return nil, err
			}
		}
	}
	return &bank, nil
}

func validateVector(name string, values []float64, dimension int) error {
	if len(values) != dimension {
		return fmt.Errorf("fingerprint: %s dimension is %d, want %d", name, len(values), dimension)
	}
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return fmt.Errorf("fingerprint: nonfinite %s", name)
		}
	}
	return nil
}

func validateArtifact(artifact numericArtifact, dimension, modelCount int) error {
	if err := validateVector("feature_mean", artifact.FeatureMean, dimension); err != nil {
		return err
	}
	if err := validateVector("feature_scale", artifact.FeatureScale, dimension); err != nil {
		return err
	}
	for _, scale := range artifact.FeatureScale {
		if scale <= 0 {
			return fmt.Errorf("fingerprint: nonpositive feature scale")
		}
	}
	for _, vector := range artifact.NuisanceBasis {
		if err := validateVector("nuisance_basis", vector, dimension); err != nil {
			return err
		}
	}
	return validateCentroids(artifact.Centroids, dimension, modelCount)
}

func validateCentroids(centroids [][]float64, dimension, modelCount int) error {
	if len(centroids) != modelCount {
		return fmt.Errorf("fingerprint: centroid count is %d, want %d", len(centroids), modelCount)
	}
	for _, centroid := range centroids {
		if err := validateVector("centroid", centroid, dimension); err != nil {
			return err
		}
	}
	return nil
}
