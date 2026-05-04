// @index Search evaluation corpus loading and ranking metric orchestration.
package eval

import (
	"encoding/json"
	"os"
)

func capResults(results []string, n int) []string {
	if len(results) <= n {
		return results
	}
	return results[:n]
}

// SearchFunc abstracts search execution so evaluation can run against any backend.
// @intent decouple ranking metrics from concrete search implementations.
type SearchFunc func(query string, limit int) ([]string, error)

// LoadQueryCorpus loads search evaluation cases from a JSON corpus file.
// @intent ingest the search query corpus before running ranking evaluation.
func LoadQueryCorpus(path string) (QueryCorpus, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return QueryCorpus{}, err
	}
	var qc QueryCorpus
	if err := json.Unmarshal(data, &qc); err != nil {
		return QueryCorpus{}, err
	}
	return qc, nil
}

// @intent execute ranked retrieval metrics across all search evaluation cases.
func EvaluateQueries(cases []QueryCase, searchFn SearchFunc) (SearchReport, error) {
	if len(cases) == 0 {
		return SearchReport{}, nil
	}

	report := SearchReport{QueriesTotal: len(cases)}
	var sumP1, sumP3, sumP5, sumR5, sumMRR, sumNDCG5 float64
	negativeQueries := 0
	negativeFalsePositives := 0
	positiveQueries := 0

	for _, qc := range cases {
		k := qc.K
		if k <= 0 {
			k = 5
		}
		limit := k
		if limit < 5 {
			limit = 5
		}

		results, err := searchFn(qc.Query, limit)
		if err != nil {
			return SearchReport{}, err
		}

		if len(qc.Relevant) == 0 {
			negativeQueries++
			falsePositive := FalsePositiveRate(results) > 0
			negativeFalsePositives += int(FalsePositiveRate(results))
			report.PerQuery = append(report.PerQuery, QueryDiagnostic{
				Query:           qc.Query,
				Kind:            "negative",
				ResultsReturned: len(results),
				FalsePositive:   falsePositive,
				TopResults:      capResults(results, 5),
			})
			continue
		}
		positiveQueries++

		relevant := make(map[string]bool, len(qc.Relevant))
		for _, r := range qc.Relevant {
			relevant[r] = true
		}

		p1 := PrecisionAtK(results, relevant, 1)
		p3 := PrecisionAtK(results, relevant, 3)
		p5 := PrecisionAtK(results, relevant, 5)
		r5 := RecallAtK(results, relevant, 5)
		mrr := MRR(results, relevant)
		ndcg5 := NDCG(results, relevant, 5)

		sumP1 += p1
		sumP3 += p3
		sumP5 += p5
		sumR5 += r5
		sumMRR += mrr
		sumNDCG5 += ndcg5
		report.PerQuery = append(report.PerQuery, QueryDiagnostic{
			Query:           qc.Query,
			Kind:            "positive",
			ResultsReturned: len(results),
			PAt1:            p1,
			PAt5:            p5,
			RecallAt5:       r5,
			MRR:             mrr,
			NDCGAt5:         ndcg5,
			TopResults:      capResults(results, 5),
		})
	}

	report.NegativeQueries = negativeQueries
	report.NegativeFalsePositives = negativeFalsePositives
	if positiveQueries > 0 {
		n := float64(positiveQueries)
		report.AvgPAt1 = sumP1 / n
		report.AvgPAt3 = sumP3 / n
		report.AvgPAt5 = sumP5 / n
		report.AvgRecallAt5 = sumR5 / n
		report.AvgMRR = sumMRR / n
		report.AvgNDCGAt5 = sumNDCG5 / n
	}
	if negativeQueries > 0 {
		report.NegativePassRate = 1 - float64(negativeFalsePositives)/float64(negativeQueries)
	}
	return report, nil
}
