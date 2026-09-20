package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// RetrievalConfig is operator-owned and persisted with the mission.
type RetrievalConfig struct {
	MCP *MCPConfig `json:"mcp,omitempty"`
}

func (c RetrievalConfig) Validate() error {
	if c.MCP != nil {
		return c.MCP.Validate()
	}
	return nil
}

type sourceDocument struct{ Name, Origin, Endpoint, URI, Text string }
type RetrievalReport struct {
	Method     string `json:"method"`
	Query      string `json:"query"`
	Documents  int    `json:"documents"`
	Candidates int    `json:"candidate_chunks"`
	Matches    int    `json:"matching_chunks"`
	Selected   int    `json:"selected_chunks"`
}

func retrieveSources(ctx context.Context, dir, query string, config RetrievalConfig) ([]Source, *RetrievalReport, error) {
	if err := config.Validate(); err != nil {
		return nil, nil, err
	}
	if len(query) > 8192 {
		return nil, nil, errors.New("retrieval query exceeds 8 KiB")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, err
	}
	var docs []sourceDocument
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if entry.IsDir() || (ext != ".txt" && ext != ".md" && ext != ".csv" && ext != ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, nil, err
		}
		if !utf8.Valid(b) || bytes.IndexByte(b, 0) >= 0 || len(bytes.TrimSpace(b)) == 0 {
			continue
		}
		docs = append(docs, sourceDocument{Name: entry.Name(), Origin: "workspace", Text: string(b)})
	}
	if config.MCP != nil {
		remote, err := readMCP(ctx, *config.MCP, os.Getenv("AGENTOS_MCP_TOKEN"))
		if err != nil {
			return nil, nil, err
		}
		docs = append(docs, remote...)
	}
	return rankSources(ctx, docs, query)
}

// Tokenization deliberately has no stemming, synonyms or language model.
func terms(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}
func rankSources(ctx context.Context, docs []sourceDocument, query string) ([]Source, *RetrievalReport, error) {
	queryTerms := map[string]bool{}
	for _, term := range terms(query) {
		queryTerms[term] = true
	}
	if len(queryTerms) == 0 || len(queryTerms) > 128 {
		return nil, nil, errors.New("retrieval requires 1..128 distinct query terms")
	}
	// Sort documents so ties and IDs do not depend on provider ordering.
	sort.Slice(docs, func(i, j int) bool { return documentKey(docs[i]) < documentKey(docs[j]) })
	type candidate struct {
		source Source
		counts map[string]int
		length int
		score  float64
	}
	var candidates []candidate
	df := map[string]int{}
	totalLength := 0
	captured := time.Now().UTC()
	for _, doc := range docs {
		hash := digest([]byte(doc.Text))
		docID := digest([]byte(documentKey(doc) + "\x00" + hash))
		for start := 0; start < len(doc.Text); {
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
			end := start + 2048
			if end > len(doc.Text) {
				end = len(doc.Text)
			}
			for end < len(doc.Text) && !utf8.RuneStart(doc.Text[end]) {
				end--
			}
			excerpt := doc.Text[start:end]
			tokens := terms(excerpt)
			if len(tokens) > 0 {
				counts := map[string]int{}
				for _, term := range tokens {
					if queryTerms[term] {
						counts[term]++
					}
				}
				for term := range counts {
					df[term]++
				}
				s := Source{ID: digest([]byte(fmt.Sprintf("%s:%d:%d", docID, start, end))), Name: doc.Name, SHA256: hash, ExcerptSHA256: digest([]byte(excerpt)), Text: excerpt, Truncated: start > 0 || end < len(doc.Text), CapturedAt: captured, Origin: doc.Origin, Endpoint: doc.Endpoint, URI: doc.URI, DocumentID: docID, StartByte: start, EndByte: end}
				candidates = append(candidates, candidate{source: s, counts: counts, length: len(tokens)})
				totalLength += len(tokens)
				if len(candidates) > 12000 {
					return nil, nil, errors.New("retrieval exceeds 12000 chunks")
				}
			}
			if end == len(doc.Text) {
				break
			}
			start = end - 256
			for start < end && !utf8.RuneStart(doc.Text[start]) {
				start++
			}
		}
	}
	if len(candidates) == 0 {
		return nil, nil, errors.New("no eligible retrieval sources")
	}
	avg := float64(totalLength) / float64(len(candidates))
	// Sort query terms: floating-point summation must be deterministic too.
	keys := make([]string, 0, len(queryTerms))
	for term := range queryTerms {
		keys = append(keys, term)
	}
	sort.Strings(keys)
	matches := 0
	for i := range candidates {
		c := &candidates[i]
		for _, term := range keys {
			tf := float64(c.counts[term])
			if tf == 0 {
				continue
			}
			idf := math.Log(1 + (float64(len(candidates)-df[term])+0.5)/(float64(df[term])+0.5))
			c.score += idf * tf * 2.2 / (tf + 1.2*(0.25+0.75*float64(c.length)/avg))
		}
		if c.score > 0 {
			matches++
		}
	}
	if matches == 0 {
		return nil, nil, errors.New("no source chunks match the mission; refine the mission or sources")
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score == candidates[j].score {
			return candidates[i].source.ID < candidates[j].source.ID
		}
		return candidates[i].score > candidates[j].score
	})
	var selected []Source
	for _, c := range candidates {
		if c.score <= 0 || len(selected) == 8 {
			break
		}
		c.source.Score = c.score
		selected = append(selected, c.source)
	}
	return selected, &RetrievalReport{"bm25-v1", query, len(docs), len(candidates), matches, len(selected)}, nil
}
func documentKey(d sourceDocument) string {
	return d.Origin + "\x00" + d.Endpoint + "\x00" + d.URI + "\x00" + d.Name
}
