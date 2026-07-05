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

func TestFileStoreRewritesPricesActionsAndValidationMeta(t *testing.T) {
	root := t.TempDir()
	store := NewFileStore(root)
	updatedAt := time.Date(2026, 7, 4, 7, 1, 12, 0, time.UTC)

	records := []PriceRecord{
		priceWithClose("2026-07-03", "AAPL", 50),
		priceWithClose("2026-07-01", "AAPL", 100),
	}
	actions := []CorporateAction{
		{Date: "2020-08-31", Ticker: "AAPL", Type: ActionTypeSplit, Numerator: 4, Denominator: 1, Ratio: 4, Source: SourceYahoo},
		{Date: "2020-08-31", Ticker: "AAPL", Type: ActionTypeSplit, Numerator: 4, Denominator: 1, Ratio: 4, Source: SourceYahoo},
		{Date: "2026-05-15", Ticker: "AAPL", Type: ActionTypeDividend, Amount: 0.26, Source: SourceYahoo},
	}

	meta, actionsWritten, err := store.RewriteTickerData("AAPL", records, actions, updatedAt, RewriteOptions{
		BackfillCompleted:       true,
		AdjustedSeriesValidated: true,
		FullValidationAt:        updatedAt,
	})
	if err != nil {
		t.Fatalf("RewriteTickerData() error = %v", err)
	}
	if actionsWritten != 2 {
		t.Fatalf("actionsWritten = %d", actionsWritten)
	}

	priceRaw, err := os.ReadFile(filepath.Join(root, "AAPL", "AAPL.jsonl"))
	if err != nil {
		t.Fatalf("read prices: %v", err)
	}
	priceLines := strings.Split(strings.TrimSpace(string(priceRaw)), "\n")
	if len(priceLines) != 2 {
		t.Fatalf("expected 2 price lines, got %q", string(priceRaw))
	}
	var first PriceRecord
	if err := json.Unmarshal([]byte(priceLines[0]), &first); err != nil {
		t.Fatalf("decode first price: %v", err)
	}
	if first.Date != "2026-07-01" || first.AdjustmentVersion == "" {
		t.Fatalf("unexpected first price: %+v", first)
	}

	actionRaw, err := os.ReadFile(filepath.Join(root, "actions", "AAPL", "AAPL.actions.jsonl"))
	if err != nil {
		t.Fatalf("read actions: %v", err)
	}
	actionLines := strings.Split(strings.TrimSpace(string(actionRaw)), "\n")
	if len(actionLines) != 2 {
		t.Fatalf("expected 2 action lines after dedupe, got %q", string(actionRaw))
	}
	var firstAction CorporateAction
	if err := json.Unmarshal([]byte(actionLines[0]), &firstAction); err != nil {
		t.Fatalf("decode first action: %v", err)
	}
	if firstAction.Date != "2020-08-31" || firstAction.Type != ActionTypeSplit || firstAction.Ratio != 4 {
		t.Fatalf("unexpected first action: %+v", firstAction)
	}

	if meta.FirstDate != "2026-07-01" || meta.LastDate != "2026-07-03" || !meta.AdjustedSeriesValidated || meta.LastCorporateActionDate != "2026-05-15" || meta.LastSplitDate != "2020-08-31" {
		t.Fatalf("unexpected meta: %+v", meta)
	}
	if !strings.HasPrefix(meta.CorporateActionHash, "sha256:") || !strings.HasPrefix(meta.PriceDataHash, "sha256:") || meta.LastFullValidationAt != "2026-07-04T07:01:12Z" {
		t.Fatalf("meta hashes/full validation not set: %+v", meta)
	}
}
