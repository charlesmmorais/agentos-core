package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestRetrievalFindsLaterChunks(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"} {
		if err := os.WriteFile(filepath.Join(dir, name+".txt"), []byte("Unrelated weather forecast."), 0600); err != nil {
			t.Fatal(err)
		}
	}
	content := strings.Repeat("Introdução sem informação. ", 160) + "\nFalha crítica no banco de dados: replicação interrompida."
	if err := os.WriteFile(filepath.Join(dir, "z.txt"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	sources, report, err := retrieveSources(context.Background(), dir, "replicação interrompida", RetrievalConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Documents != 10 || report.Method != "bm25-v1" || len(sources) == 0 {
		t.Fatal("retrieval missed corpus")
	}
	for _, s := range sources {
		if s.Name != "z.txt" || s.StartByte == 0 || s.Text != content[s.StartByte:s.EndByte] || !utf8.ValidString(s.Text) || s.SHA256 != digest([]byte(content)) || s.ExcerptSHA256 != digest([]byte(s.Text)) {
			t.Fatal("incorrect source provenance")
		}
	}
	again, _, err := retrieveSources(context.Background(), dir, "replicação interrompida", RetrievalConfig{})
	if err != nil {
		t.Fatal(err)
	}
	for i := range sources {
		if sources[i].ID != again[i].ID || sources[i].Score != again[i].Score {
			t.Fatal("ranking not deterministic")
		}
	}
}

func TestRetrievalLimitsAndNoMatch(t *testing.T) {
	docs := []sourceDocument{{Name: "a", Origin: "workspace", Text: strings.Repeat("memory evidence ", 2000)}}
	sources, report, err := rankSources(context.Background(), docs, "memory")
	if err != nil || len(sources) != 8 || report.Matches <= 8 {
		t.Fatal("top-k not enforced", err)
	}
	for _, s := range sources {
		if len(s.Text) > 2048 || s.Score <= 0 {
			t.Fatal("invalid chunk")
		}
	}
	if _, _, err := rankSources(context.Background(), docs, "unrelated"); err == nil {
		t.Fatal("unrelated sources admitted")
	}
	if _, _, err := rankSources(context.Background(), docs, "?!"); err == nil {
		t.Fatal("empty query admitted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := rankSources(ctx, docs, "memory"); err == nil {
		t.Fatal("cancellation ignored")
	}
}

func TestRetrievalSourceIdentity(t *testing.T) {
	docs := []sourceDocument{
		{Name: "report", Origin: "workspace", Text: "memory evidence"},
		{Name: "report", Origin: "mcp", Endpoint: "https://one/mcp", URI: "data:report", Text: "memory evidence"},
		{Name: "report", Origin: "mcp", Endpoint: "https://two/mcp", URI: "data:report", Text: "memory evidence"},
	}
	sources, _, err := rankSources(context.Background(), docs, "memory")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, s := range sources {
		if seen[s.ID] {
			t.Fatal("source identity collision")
		}
		seen[s.ID] = true
	}
	if len(seen) != 3 {
		t.Fatal("lost source")
	}
}
