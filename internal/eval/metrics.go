package eval

import "math"

type ClassificationMetrics struct {
	TruePositive  int
	FalsePositive int
	FalseNegative int
	Precision     float64
	Recall        float64
	F1            float64
}

func ComputeClassification(expected, actual []string) ClassificationMetrics {
	expectedSet := make(map[string]bool, len(expected))
	for _, e := range expected {
		expectedSet[e] = true
	}
	actualSet := make(map[string]bool, len(actual))
	for _, a := range actual {
		actualSet[a] = true
	}

	var m ClassificationMetrics
	for a := range actualSet {
		if expectedSet[a] {
			m.TruePositive++
		} else {
			m.FalsePositive++
		}
	}
	for e := range expectedSet {
		if !actualSet[e] {
			m.FalseNegative++
		}
	}

	if m.TruePositive+m.FalsePositive > 0 {
		m.Precision = float64(m.TruePositive) / float64(m.TruePositive+m.FalsePositive)
	}
	if m.TruePositive+m.FalseNegative > 0 {
		m.Recall = float64(m.TruePositive) / float64(m.TruePositive+m.FalseNegative)
	}
	if m.Precision+m.Recall > 0 {
		m.F1 = 2 * m.Precision * m.Recall / (m.Precision + m.Recall)
	}

	return m
}

func PrecisionAtK(ranked []string, relevant map[string]bool, k int) float64 {
	if k <= 0 || len(ranked) == 0 || len(relevant) == 0 {
		return 0
	}
	n := k
	if n > len(ranked) {
		n = len(ranked)
	}
	hits := 0
	for i := 0; i < n; i++ {
		if relevant[ranked[i]] {
			hits++
		}
	}
	return float64(hits) / float64(n)
}

func RecallAtK(ranked []string, relevant map[string]bool, k int) float64 {
	if k <= 0 || len(ranked) == 0 || len(relevant) == 0 {
		return 0
	}
	n := k
	if n > len(ranked) {
		n = len(ranked)
	}
	hits := 0
	for i := 0; i < n; i++ {
		if relevant[ranked[i]] {
			hits++
		}
	}
	return float64(hits) / float64(len(relevant))
}

func MRR(ranked []string, relevant map[string]bool) float64 {
	for i, r := range ranked {
		if relevant[r] {
			return 1.0 / float64(i+1)
		}
	}
	return 0
}

func NDCG(ranked []string, relevant map[string]bool, k int) float64 {
	if k <= 0 || len(relevant) == 0 {
		return 0
	}
	n := k
	if n > len(ranked) {
		n = len(ranked)
	}

	dcg := 0.0
	for i := 0; i < n; i++ {
		if relevant[ranked[i]] {
			dcg += 1.0 / math.Log2(float64(i+2))
		}
	}

	idealCount := len(relevant)
	if idealCount > k {
		idealCount = k
	}
	idcg := 0.0
	for i := 0; i < idealCount; i++ {
		idcg += 1.0 / math.Log2(float64(i+2))
	}

	if idcg == 0 {
		return 0
	}
	return dcg / idcg
}
