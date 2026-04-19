package eval

import (
	"math"
	"testing"
)

const epsilon = 1e-9

func approxEqual(a, b float64) bool {
	return math.Abs(a-b) < epsilon
}

func TestComputeClassification_AllCorrect(t *testing.T) {
	expected := []string{"a", "b", "c"}
	actual := []string{"a", "b", "c"}
	m := ComputeClassification(expected, actual)

	if m.TruePositive != 3 {
		t.Errorf("TP: got %d, want 3", m.TruePositive)
	}
	if m.FalsePositive != 0 {
		t.Errorf("FP: got %d, want 0", m.FalsePositive)
	}
	if m.FalseNegative != 0 {
		t.Errorf("FN: got %d, want 0", m.FalseNegative)
	}
	if !approxEqual(m.Precision, 1.0) {
		t.Errorf("Precision: got %f, want 1.0", m.Precision)
	}
	if !approxEqual(m.Recall, 1.0) {
		t.Errorf("Recall: got %f, want 1.0", m.Recall)
	}
	if !approxEqual(m.F1, 1.0) {
		t.Errorf("F1: got %f, want 1.0", m.F1)
	}
}

func TestComputeClassification_PartialMatch(t *testing.T) {
	expected := []string{"a", "b", "c"}
	actual := []string{"a", "b", "d"} // d is FP, c is FN
	m := ComputeClassification(expected, actual)

	if m.TruePositive != 2 {
		t.Errorf("TP: got %d, want 2", m.TruePositive)
	}
	if m.FalsePositive != 1 {
		t.Errorf("FP: got %d, want 1", m.FalsePositive)
	}
	if m.FalseNegative != 1 {
		t.Errorf("FN: got %d, want 1", m.FalseNegative)
	}
	// Precision = 2/3
	if !approxEqual(m.Precision, 2.0/3.0) {
		t.Errorf("Precision: got %f, want %f", m.Precision, 2.0/3.0)
	}
	// Recall = 2/3
	if !approxEqual(m.Recall, 2.0/3.0) {
		t.Errorf("Recall: got %f, want %f", m.Recall, 2.0/3.0)
	}
}

func TestComputeClassification_Empty(t *testing.T) {
	m := ComputeClassification(nil, nil)
	if m.Precision != 0 || m.Recall != 0 || m.F1 != 0 {
		t.Errorf("empty should be zeros: %+v", m)
	}
}

func TestComputeClassification_NoOverlap(t *testing.T) {
	m := ComputeClassification([]string{"a", "b"}, []string{"c", "d"})
	if m.TruePositive != 0 {
		t.Errorf("TP: got %d, want 0", m.TruePositive)
	}
	if !approxEqual(m.Precision, 0) {
		t.Errorf("Precision: got %f, want 0", m.Precision)
	}
}

func TestPrecisionAtK(t *testing.T) {
	relevant := map[string]bool{"a": true, "b": true, "c": true}
	ranked := []string{"a", "d", "b", "e", "c"}

	if got := PrecisionAtK(ranked, relevant, 1); !approxEqual(got, 1.0) {
		t.Errorf("P@1: got %f, want 1.0", got)
	}
	if got := PrecisionAtK(ranked, relevant, 3); !approxEqual(got, 2.0/3.0) {
		t.Errorf("P@3: got %f, want %f", got, 2.0/3.0)
	}
	if got := PrecisionAtK(ranked, relevant, 5); !approxEqual(got, 3.0/5.0) {
		t.Errorf("P@5: got %f, want %f", got, 3.0/5.0)
	}
}

func TestRecallAtK(t *testing.T) {
	relevant := map[string]bool{"a": true, "b": true, "c": true}
	ranked := []string{"a", "d", "b", "e", "c"}

	if got := RecallAtK(ranked, relevant, 1); !approxEqual(got, 1.0/3.0) {
		t.Errorf("R@1: got %f, want %f", got, 1.0/3.0)
	}
	if got := RecallAtK(ranked, relevant, 5); !approxEqual(got, 1.0) {
		t.Errorf("R@5: got %f, want 1.0", got)
	}
}

func TestMRR(t *testing.T) {
	relevant := map[string]bool{"b": true}
	ranked := []string{"a", "b", "c"}
	if got := MRR(ranked, relevant); !approxEqual(got, 0.5) {
		t.Errorf("MRR: got %f, want 0.5", got)
	}

	// not found
	if got := MRR(ranked, map[string]bool{"z": true}); !approxEqual(got, 0) {
		t.Errorf("MRR not found: got %f, want 0", got)
	}
}

func TestNDCG(t *testing.T) {
	relevant := map[string]bool{"a": true, "b": true, "c": true}
	// Perfect ranking
	perfect := []string{"a", "b", "c"}
	if got := NDCG(perfect, relevant, 3); !approxEqual(got, 1.0) {
		t.Errorf("nDCG perfect: got %f, want 1.0", got)
	}

	// Worst: no relevant in top K
	worst := []string{"x", "y", "z"}
	if got := NDCG(worst, relevant, 3); !approxEqual(got, 0) {
		t.Errorf("nDCG worst: got %f, want 0", got)
	}
}

func TestPrecisionAtK_EmptyInputs(t *testing.T) {
	if got := PrecisionAtK(nil, nil, 5); got != 0 {
		t.Errorf("empty: got %f, want 0", got)
	}
}
