package tooloptimize

import (
	"math"
	"regexp"
	"strings"
)

// BM25 relevance ranking, mirroring Hermes's tool_search.py approach (an inlined
// small implementation rather than a dependency). Used to decide which tools are
// in-domain for an agent — a semantically stronger signal than substring match,
// while staying pure Go with no model call.

const (
	bm25K1 = 1.5
	bm25B  = 0.75
)

var tokenRe = regexp.MustCompile(`[a-z0-9]+`)

// tokenize lowercases and splits text into alphanumeric terms, breaking
// snake_case / dotted / hyphenated names into words so "git_clone" matches "git".
func tokenize(text string) []string {
	if text == "" {
		return nil
	}
	return tokenRe.FindAllString(strings.ToLower(text), -1)
}

// bm25Doc is one indexed document with its precomputed term frequencies.
type bm25Doc struct {
	tokens []string
	tf     map[string]int
}

// bm25Index holds corpus statistics for scoring queries against documents.
type bm25Index struct {
	docs    []bm25Doc
	docFreq map[string]int
	avgLen  float64
	n       int
}

// buildBM25Index indexes the given document texts (parallel to a caller-held
// slice of the same length/order).
func buildBM25Index(texts []string) *bm25Index {
	ix := &bm25Index{docFreq: make(map[string]int), n: len(texts)}
	totalLen := 0
	for _, text := range texts {
		toks := tokenize(text)
		tf := make(map[string]int, len(toks))
		for _, tok := range toks {
			tf[tok]++
		}
		for tok := range tf {
			ix.docFreq[tok]++
		}
		totalLen += len(toks)
		ix.docs = append(ix.docs, bm25Doc{tokens: toks, tf: tf})
	}
	if ix.n > 0 {
		ix.avgLen = float64(totalLen) / float64(ix.n)
	}
	return ix
}

// score returns the BM25 score of the query against document docIdx.
func (ix *bm25Index) score(queryTokens []string, docIdx int) float64 {
	if docIdx < 0 || docIdx >= len(ix.docs) {
		return 0
	}
	doc := ix.docs[docIdx]
	if len(doc.tokens) == 0 {
		return 0
	}
	dl := float64(len(doc.tokens))
	var score float64
	for _, q := range queryTokens {
		df := ix.docFreq[q]
		if df == 0 {
			continue
		}
		tf := doc.tf[q]
		if tf == 0 {
			continue
		}
		idf := math.Log(1 + (float64(ix.n)-float64(df)+0.5)/(float64(df)+0.5))
		norm := float64(tf) * (bm25K1 + 1) / (float64(tf) + bm25K1*(1-bm25B+bm25B*dl/ix.avgLen))
		score += idf * norm
	}
	return score
}
