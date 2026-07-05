package collector

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFileStoreAppendsJSONLAndUpdatesMeta(t *testing.T) {
	root := t.TempDir()
	store := NewFileStore(root)
	updatedAt := time.Date(2026, 7, 4, 7, 1, 12, 0, time.UTC)

	records := []PriceRecord{
		{
			Date:     "2026-07-02",
			Ticker:   "AAPL",
			Open:     210.10,
			High:     214.20,
			Low:      209.90,
			Close:    213.70,
			AdjClose: 213.70,
			Volume:   52_413_300,
			Source:   "yahoo",
		},
		{
			Date:     "2026-07-03",
			Ticker:   "AAPL",
			Open:     213.80,
			High:     215.00,
			Low:      212.30,
			Close:    214.10,
			AdjClose: 214.10,
			Volume:   48_000_000,
			Source:   "yahoo",
		},
	}

	if err := store.AppendPrices("AAPL", records, updatedAt); err != nil {
		t.Fatalf("AppendPrices() error = %v", err)
	}

	jsonlPath := filepath.Join(root, "AAPL", "AAPL.jsonl")
	raw, err := os.ReadFile(jsonlPath)
	if err != nil {
		t.Fatalf("read jsonl: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 JSONL lines, got %d: %q", len(lines), string(raw))
	}

	var first PriceRecord
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("first line is not JSON: %v", err)
	}
	if first.Date != "2026-07-02" || first.Ticker != "AAPL" || first.Source != "yahoo" {
		t.Fatalf("unexpected first record: %+v", first)
	}

	meta, ok, err := store.LoadMeta("AAPL")
	if err != nil {
		t.Fatalf("LoadMeta() error = %v", err)
	}
	if !ok {
		t.Fatal("expected meta file to exist")
	}
	if meta.LastDate != "2026-07-03" || meta.Records != 2 || meta.UpdatedAt != "2026-07-04T07:01:12Z" || meta.Source != "yahoo" {
		t.Fatalf("unexpected meta: %+v", meta)
	}
}
