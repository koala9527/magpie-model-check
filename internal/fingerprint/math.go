// ModelTrace numeric scorer port, MIT License.
// Copyright (c) 2026 xqy2006. See reference/LICENSE.
package fingerprint

import "math"

func dot(left, right []float64) float64 {
	value := 0.0
	for i, v := range left {
		value += v * right[i]
	}
	return value
}

func normalized(values []float64) []float64 {
	scale := math.Max(math.Sqrt(dot(values, values)), 1e-12)
	output := make([]float64, len(values))
	for i, value := range values {
		output[i] = value / scale
	}
	return output
}

func standardize(values []float64) []float64 {
	center := 0.0
	for _, value := range values {
		center += value
	}
	center /= float64(len(values))
	variance := 0.0
	for _, value := range values {
		delta := value - center
		variance += delta * delta
	}
	scale := math.Max(math.Sqrt(variance/float64(len(values))), 1e-12)
	output := make([]float64, len(values))
	for i, value := range values {
		output[i] = (value - center) / scale
	}
	return output
}

func standardizedFeature(feature []float64, artifact numericArtifact) []float64 {
	output := make([]float64, len(feature))
	for i, value := range feature {
		output[i] = (value - artifact.FeatureMean[i]) / artifact.FeatureScale[i]
	}
	return output
}

func subtractBasis(values []float64, basis [][]float64) []float64 {
	output := append([]float64(nil), values...)
	// JS subtracts each projection from the current residual, sequentially.
	// The upstream Python uses a simultaneous matrix projection; regression
	// measures the roundoff difference with the pinned near-orthonormal basis.
	for _, vector := range basis {
		projection := dot(output, vector)
		for i := range output {
			output[i] -= projection * vector[i]
		}
	}
	return output
}

func centroidScores(feature []float64, centroids [][]float64) []float64 {
	scores := make([]float64, len(centroids))
	for i, centroid := range centroids {
		scores[i] = dot(feature, centroid)
	}
	return scores
}
