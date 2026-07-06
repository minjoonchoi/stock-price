package collector

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProgressStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "collect-prices.state.json")
	state := ProgressState{
		JobName:             "collect-prices",
		RunID:               "2026-07-06",
		SECTickerHash:       "sha256:sec",
		UniverseHash:        "sha256:universe",
		TotalTargets:        4200,
		ProcessedTargets:    1300,
		LastProcessedTicker: "MSFT",
		CursorIndex:         1300,
		Completed:           false,
		StartedAt:           "2026-07-06T22:00:00Z",
		UpdatedAt:           "2026-07-07T00:40:00Z",
	}

	if err := WriteProgressState(path, state); err != nil {
		t.Fatalf("WriteProgressState() error = %v", err)
	}
	loaded, ok, err := LoadProgressState(path)
	if err != nil {
		t.Fatalf("LoadProgressState() error = %v", err)
	}
	if !ok {
		t.Fatal("expected state file to exist")
	}
	if loaded.JobName != state.JobName || loaded.CursorIndex != 1300 || loaded.UniverseHash != "sha256:universe" || loaded.LastProcessedTicker != "MSFT" {
		t.Fatalf("loaded state mismatch: %+v", loaded)
	}
}

func TestLoadProgressStateMissingFile(t *testing.T) {
	state, ok, err := LoadProgressState(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatalf("LoadProgressState() error = %v", err)
	}
	if ok {
		t.Fatalf("expected missing state, got %+v", state)
	}
}

func TestHashCompaniesIsDeterministic(t *testing.T) {
	first := HashCompanies([]Company{
		{Ticker: "msft", CIK: 789019, Title: "Microsoft Corp."},
		{Ticker: "AAPL", CIK: 320193, Title: "Apple Inc."},
	})
	second := HashCompanies([]Company{
		{Ticker: "AAPL", CIK: 320193, Title: "Apple Inc."},
		{Ticker: "MSFT", CIK: 789019, Title: "Microsoft Corp."},
	})
	if first != second {
		t.Fatalf("hash should be deterministic regardless of order: %q != %q", first, second)
	}
	if first == HashCompanies([]Company{{Ticker: "AAPL", CIK: 320193, Title: "Apple Inc."}}) {
		t.Fatal("hash should change when input list changes")
	}
}

func TestRuntimeBudgetStopsBeforeGraceWindow(t *testing.T) {
	startedAt := time.Date(2026, 7, 6, 0, 0, 0, 0, time.UTC)
	budget := RuntimeBudget{
		StartedAt:    startedAt,
		MaxRuntime:   330 * time.Minute,
		GracefulStop: 10 * time.Minute,
		Clock:        func() time.Time { return startedAt.Add(319 * time.Minute) },
	}
	if budget.ShouldStopBeforeNext() {
		t.Fatal("should still allow work before grace window")
	}

	budget.Clock = func() time.Time { return startedAt.Add(320 * time.Minute) }
	if !budget.ShouldStopBeforeNext() {
		t.Fatal("should stop at the graceful stop boundary")
	}
}

func TestWriteProgressStateCreatesParentDirectory(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "nested", "state.json")
	if err := WriteProgressState(path, ProgressState{JobName: "update-universe"}); err != nil {
		t.Fatalf("WriteProgressState() error = %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("state file was not created: %v", err)
	}
}

func TestSortAndShardCompaniesByTicker(t *testing.T) {
	companies := []Company{
		{Ticker: "MSFT"},
		{Ticker: "AAPL"},
		{Ticker: "NVDA"},
		{Ticker: "AMZN"},
		{Ticker: ""},
	}

	sorted := SortCompaniesByTicker(companies)
	if got := []string{sorted[0].Ticker, sorted[1].Ticker, sorted[2].Ticker, sorted[3].Ticker}; got[0] != "AAPL" || got[1] != "AMZN" || got[2] != "MSFT" || got[3] != "NVDA" {
		t.Fatalf("unexpected sort order: %+v", got)
	}

	shard := SelectCompanyShard(companies, 1, 2)
	if len(shard) != 2 || shard[0].Ticker != "AMZN" || shard[1].Ticker != "NVDA" {
		t.Fatalf("unexpected shard: %+v", shard)
	}
}
