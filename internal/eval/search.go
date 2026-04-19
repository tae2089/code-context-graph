package eval

import (
	"encoding/json"
	"os"
)

type SearchFunc func(query string, limit int) ([]string, error)

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

func EvaluateQueries(cases []QueryCase, searchFn SearchFunc) (SearchReport, error) {
	if len(cases) == 0 {
		return SearchReport{}, nil
	}

	var sumP1, sumP3, sumP5, sumR5, sumMRR, sumNDCG5 float64

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

		relevant := make(map[string]bool, len(qc.Relevant))
		for _, r := range qc.Relevant {
			relevant[r] = true
		}

		sumP1 += PrecisionAtK(results, relevant, 1)
		sumP3 += PrecisionAtK(results, relevant, 3)
		sumP5 += PrecisionAtK(results, relevant, 5)
		sumR5 += RecallAtK(results, relevant, 5)
		sumMRR += MRR(results, relevant)
		sumNDCG5 += NDCG(results, relevant, 5)
	}

	n := float64(len(cases))
	return SearchReport{
		QueriesTotal: len(cases),
		AvgPAt1:      sumP1 / n,
		AvgPAt3:      sumP3 / n,
		AvgPAt5:      sumP5 / n,
		AvgRecallAt5: sumR5 / n,
		AvgMRR:       sumMRR / n,
		AvgNDCGAt5:   sumNDCG5 / n,
	}, nil
}
