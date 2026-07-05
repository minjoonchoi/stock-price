package collector

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

type fakeProvider struct {
	calls   []providerCall
	history PriceHistory
	err     error
}

type providerCall struct {
	ticker string
	start  string
	end    string
}

func (p *fakeProvider) FetchHistory(ctx context.Context, ticker string, start time.Time, end time.Time) (PriceHistory, error) {
	p.calls = append(p.calls, providerCall{
		ticker: ticker,
		start:  formatCallDate(start),
		end:    FormatDate(end),
	})
	if p.err != nil {
		return PriceHistory{}, p.err
	}
	return p.history, nil
}

type fakeProviderFunc func(ctx context.Context, ticker string, start time.Time, end time.Time) (PriceHistory, error)

func (f fakeProviderFunc) FetchHistory(ctx context.Context, ticker string, start time.Time, end time.Time) (PriceHistory, error) {
	return f(ctx, ticker, start, end)
}

func TestRunnerSkipsTickerWhenMetaAlreadyReachedYesterday(t *testing.T) {
	root := t.TempDir()
	store := NewFileStore(root)
	updatedAt := time.Date(2026, 7, 4, 0, 0, 0, 0, time.UTC)
	if _, _, err := store.RewriteTickerData("AAPL", []PriceRecord{price("2026-07-03", "AAPL")}, nil, updatedAt, RewriteOptions{
		BackfillCompleted:       true,
		AdjustedSeriesValidated: true,
		FullValidationAt:        updatedAt,
	}); err != nil {
		t.Fatalf("RewriteTickerData() error = %v", err)
	}
	provider := &fakeProvider{}

	runner := NewRunner(RunnerConfig{
		Store:     store,
		Provider:  provider,
		StartDate: mustDate(t, "2026-01-01"),
		Clock:     fixedClock(time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)),
	})

	summary, err := runner.CollectTickers(context.Background(), []Company{{Ticker: "AAPL"}})
	if err != nil {
		t.Fatalf("CollectTickers() error = %v", err)
	}

	if len(provider.calls) != 0 {
		t.Fatalf("expected provider not to be called, got %+v", provider.calls)
	}
	if summary.Skipped != 1 || summary.Appended != 0 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
}

func TestRunnerWithCompletedBackfillFetchesFromDayAfterLastDateAndAppendsOnlyNewRows(t *testing.T) {
	root := t.TempDir()
	store := NewFileStore(root)
	updatedAt := time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)
	if _, _, err := store.RewriteTickerData("AAPL", []PriceRecord{
		price("2026-07-01", "AAPL"),
		price("2026-07-02", "AAPL"),
	}, nil, updatedAt, RewriteOptions{
		BackfillCompleted:       true,
		AdjustedSeriesValidated: true,
		FullValidationAt:        updatedAt,
	}); err != nil {
		t.Fatalf("RewriteTickerData() error = %v", err)
	}
	provider := &fakeProvider{
		history: PriceHistory{
			Records: []PriceRecord{
				price("2026-07-01", "AAPL"),
				price("2026-07-02", "AAPL"),
				price("2026-07-03", "AAPL"),
			},
		},
	}

	runner := NewRunner(RunnerConfig{
		Store:     store,
		Provider:  provider,
		StartDate: mustDate(t, "2026-01-01"),
		Clock:     fixedClock(time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)),
	})

	summary, err := runner.CollectTickers(context.Background(), []Company{{Ticker: "AAPL"}})
	if err != nil {
		t.Fatalf("CollectTickers() error = %v", err)
	}

	if len(provider.calls) != 1 {
		t.Fatalf("expected 1 provider call, got %+v", provider.calls)
	}
	if provider.calls[0] != (providerCall{ticker: "AAPL", start: "2026-07-03", end: "2026-07-03"}) {
		t.Fatalf("unexpected provider call: %+v", provider.calls[0])
	}
	if summary.Appended != 1 || summary.Skipped != 0 || summary.IncrementalUpdated != 1 || summary.Backfilled != 0 {
		t.Fatalf("unexpected summary: %+v", summary)
	}

	meta, ok, err := store.LoadMeta("AAPL")
	if err != nil {
		t.Fatalf("LoadMeta() error = %v", err)
	}
	if !ok {
		t.Fatal("expected meta to exist")
	}
	if meta.FirstDate != "2026-07-01" || meta.LastDate != "2026-07-03" || meta.Records != 3 || !meta.BackfillCompleted {
		t.Fatalf("unexpected meta: %+v", meta)
	}
}

func TestRunnerWithNoMetaAndNoStartDateRequestsProviderEarliestAvailable(t *testing.T) {
	root := t.TempDir()
	store := NewFileStore(root)
	provider := &fakeProvider{
		history: PriceHistory{
			Records: []PriceRecord{
				price("2024-01-02", "AAPL"),
				price("2026-07-03", "AAPL"),
			},
		},
	}

	runner := NewRunner(RunnerConfig{
		Store:    store,
		Provider: provider,
		Clock:    fixedClock(time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)),
	})

	summary, err := runner.CollectTickers(context.Background(), []Company{{Ticker: "AAPL"}})
	if err != nil {
		t.Fatalf("CollectTickers() error = %v", err)
	}
	if len(provider.calls) != 1 {
		t.Fatalf("expected 1 provider call, got %+v", provider.calls)
	}
	if provider.calls[0] != (providerCall{ticker: "AAPL", start: "", end: "2026-07-03"}) {
		t.Fatalf("unexpected provider call: %+v", provider.calls[0])
	}
	if summary.Appended != 2 || summary.Failed != 0 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	meta, ok, err := store.LoadMeta("AAPL")
	if err != nil {
		t.Fatalf("LoadMeta() error = %v", err)
	}
	if !ok || meta.FirstDate != "2024-01-02" || meta.LastDate != "2026-07-03" || meta.Records != 2 || !meta.BackfillCompleted {
		t.Fatalf("unexpected meta after full backfill: ok=%v meta=%+v", ok, meta)
	}
}

func TestRunnerFullBackfillIgnoresConfiguredStartDate(t *testing.T) {
	root := t.TempDir()
	store := NewFileStore(root)
	provider := &fakeProvider{
		history: PriceHistory{
			Records: []PriceRecord{
				price("1980-12-12", "AAPL"),
				price("2026-07-03", "AAPL"),
			},
		},
	}

	runner := NewRunner(RunnerConfig{
		Store:     store,
		Provider:  provider,
		StartDate: mustDate(t, "2026-06-30"),
		Clock:     fixedClock(time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)),
	})

	summary, err := runner.CollectTickers(context.Background(), []Company{{Ticker: "AAPL"}})
	if err != nil {
		t.Fatalf("CollectTickers() error = %v", err)
	}
	if summary.FullRewritten != 1 || summary.Appended != 2 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	records := mustLoadPrices(t, store, "AAPL")
	if len(records) != 2 || records[0].Date != "1980-12-12" || records[1].Date != "2026-07-03" {
		t.Fatalf("full backfill was capped unexpectedly: %+v", records)
	}
}

func TestRunnerBackfillsMissingHistoricalDatesWhenMetaIsMissing(t *testing.T) {
	root := t.TempDir()
	store := NewFileStore(root)
	if err := store.AppendPrices("AAPL", []PriceRecord{
		priceWithClose("2026-07-01", "AAPL", 100),
		priceWithClose("2026-07-02", "AAPL", 101),
	}, time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("AppendPrices() error = %v", err)
	}
	if err := os.Remove(store.metaPath("AAPL")); err != nil {
		t.Fatalf("remove meta: %v", err)
	}
	provider := &fakeProvider{
		history: PriceHistory{
			Records: []PriceRecord{
				priceWithClose("1980-12-12", "AAPL", 1),
				priceWithClose("1980-12-15", "AAPL", 2),
				priceWithClose("2026-07-01", "AAPL", 200),
				priceWithClose("2026-07-02", "AAPL", 201),
				priceWithClose("2026-07-03", "AAPL", 202),
			},
		},
	}

	runner := NewRunner(RunnerConfig{
		Store:    store,
		Provider: provider,
		Clock:    fixedClock(time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)),
	})

	summary, err := runner.CollectTickers(context.Background(), []Company{{Ticker: "AAPL"}})
	if err != nil {
		t.Fatalf("CollectTickers() error = %v", err)
	}

	if len(provider.calls) != 1 || provider.calls[0] != (providerCall{ticker: "AAPL", start: "", end: "2026-07-03"}) {
		t.Fatalf("unexpected provider calls: %+v", provider.calls)
	}
	if summary.Backfilled != 1 || summary.IncrementalUpdated != 0 || summary.Appended != 3 || summary.Failed != 0 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	records := mustLoadPrices(t, store, "AAPL")
	gotDates := make([]string, 0, len(records))
	for _, record := range records {
		gotDates = append(gotDates, record.Date)
	}
	wantDates := []string{"1980-12-12", "1980-12-15", "2026-07-01", "2026-07-02", "2026-07-03"}
	if strings.Join(gotDates, ",") != strings.Join(wantDates, ",") {
		t.Fatalf("dates = %+v, want %+v", gotDates, wantDates)
	}
	if records[2].Close != 200 {
		t.Fatalf("expected Yahoo record to replace existing date, got %+v", records[2])
	}
	meta, ok, err := store.LoadMeta("AAPL")
	if err != nil {
		t.Fatalf("LoadMeta() error = %v", err)
	}
	if !ok || meta.Ticker != "AAPL" || meta.FirstDate != "1980-12-12" || meta.LastDate != "2026-07-03" || meta.Records != 5 || !meta.BackfillCompleted {
		t.Fatalf("unexpected meta: ok=%v meta=%+v", ok, meta)
	}
}

func TestRunnerRepairsMetaWithoutCallingProvider(t *testing.T) {
	root := t.TempDir()
	store := NewFileStore(root)
	if err := store.AppendPrices("MSFT", []PriceRecord{
		price("2026-07-01", "MSFT"),
		price("2026-07-03", "MSFT"),
	}, time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("AppendPrices() error = %v", err)
	}
	if err := os.Remove(store.metaPath("MSFT")); err != nil {
		t.Fatalf("remove meta: %v", err)
	}
	provider := &fakeProvider{}

	runner := NewRunner(RunnerConfig{
		Store:      store,
		Provider:   provider,
		RepairMeta: true,
		Clock:      fixedClock(time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)),
	})

	summary, err := runner.CollectTickers(context.Background(), []Company{{Ticker: "MSFT"}})
	if err != nil {
		t.Fatalf("CollectTickers() error = %v", err)
	}
	if len(provider.calls) != 0 {
		t.Fatalf("expected provider not to be called, got %+v", provider.calls)
	}
	if summary.Skipped != 1 || summary.Backfilled != 0 || summary.IncrementalUpdated != 0 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	meta, ok, err := store.LoadMeta("MSFT")
	if err != nil {
		t.Fatalf("LoadMeta() error = %v", err)
	}
	if !ok || meta.FirstDate != "2026-07-01" || meta.LastDate != "2026-07-03" || meta.Records != 2 || meta.BackfillCompleted {
		t.Fatalf("unexpected repaired meta: ok=%v meta=%+v", ok, meta)
	}
}

func TestRunnerForceBackfillIgnoresCompletedMeta(t *testing.T) {
	root := t.TempDir()
	store := NewFileStore(root)
	if err := store.AppendPrices("AAPL", []PriceRecord{price("2026-07-02", "AAPL")}, time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("AppendPrices() error = %v", err)
	}
	if err := store.WriteMeta("AAPL", Meta{
		Ticker:            "AAPL",
		FirstDate:         "2026-07-02",
		LastDate:          "2026-07-02",
		Records:           1,
		BackfillCompleted: true,
		UpdatedAt:         "2026-07-02T00:00:00Z",
		Source:            SourceYahoo,
	}); err != nil {
		t.Fatalf("WriteMeta() error = %v", err)
	}
	provider := &fakeProvider{
		history: PriceHistory{Records: []PriceRecord{
			price("2026-07-01", "AAPL"),
			price("2026-07-02", "AAPL"),
			price("2026-07-03", "AAPL"),
		}},
	}

	runner := NewRunner(RunnerConfig{
		Store:         store,
		Provider:      provider,
		ForceBackfill: true,
		Clock:         fixedClock(time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)),
	})

	summary, err := runner.CollectTickers(context.Background(), []Company{{Ticker: "AAPL"}})
	if err != nil {
		t.Fatalf("CollectTickers() error = %v", err)
	}
	if len(provider.calls) != 1 || provider.calls[0].start != "" {
		t.Fatalf("expected full-history provider call, got %+v", provider.calls)
	}
	if summary.Backfilled != 1 || summary.Appended != 2 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
}

func TestRunnerNewTickerRewritesPricesAndActionsFromProviderTruth(t *testing.T) {
	root := t.TempDir()
	store := NewFileStore(root)
	provider := &fakeProvider{
		history: PriceHistory{
			Records: []PriceRecord{
				priceWithAdjusted("2026-07-01", "AAPL", 100, 50),
				priceWithAdjusted("2026-07-02", "AAPL", 25, 50),
			},
			Splits:    []Split{{Date: "2026-07-02", Numerator: 4, Denominator: 1, Ratio: 4}},
			Dividends: []Dividend{{Date: "2026-06-15", Amount: 0.26}},
		},
	}

	runner := NewRunner(RunnerConfig{
		Store:    store,
		Provider: provider,
		Clock:    fixedClock(time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)),
	})

	summary, err := runner.CollectTickers(context.Background(), []Company{{Ticker: "AAPL"}})
	if err != nil {
		t.Fatalf("CollectTickers() error = %v", err)
	}
	if summary.FullRewritten != 1 || summary.AdjustedValidated != 1 || summary.RowsAdjustedRecalculated != 2 || summary.ActionsWritten != 2 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	meta, ok, err := store.LoadMeta("AAPL")
	if err != nil {
		t.Fatalf("LoadMeta() error = %v", err)
	}
	if !ok || !meta.BackfillCompleted || !meta.AdjustedSeriesValidated || meta.LastSplitDate != "2026-07-02" || meta.LastCorporateActionDate != "2026-07-02" || meta.CorporateActionHash == "" || meta.PriceDataHash == "" {
		t.Fatalf("unexpected meta: ok=%v meta=%+v", ok, meta)
	}
	actions := mustLoadActions(t, store, "AAPL")
	if len(actions) != 2 || actions[0].Type != ActionTypeDividend || actions[1].Type != ActionTypeSplit {
		t.Fatalf("unexpected actions: %+v", actions)
	}
}

func TestRunnerDetectsNewSplitAndRewritesFullHistory(t *testing.T) {
	root := t.TempDir()
	store := NewFileStore(root)
	updatedAt := time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)
	if _, _, err := store.RewriteTickerData("AAPL", []PriceRecord{
		priceWithAdjusted("2026-07-01", "AAPL", 100, 50),
		priceWithAdjusted("2026-07-02", "AAPL", 100, 50),
	}, nil, updatedAt, RewriteOptions{
		BackfillCompleted:       true,
		AdjustedSeriesValidated: true,
		FullValidationAt:        updatedAt,
	}); err != nil {
		t.Fatalf("RewriteTickerData() error = %v", err)
	}

	var calls []providerCall
	provider := fakeProviderFunc(func(ctx context.Context, ticker string, start time.Time, end time.Time) (PriceHistory, error) {
		calls = append(calls, providerCall{ticker: ticker, start: formatCallDate(start), end: FormatDate(end)})
		if start.IsZero() {
			return PriceHistory{
				Records: []PriceRecord{
					priceWithAdjusted("2026-07-01", "AAPL", 25, 50),
					priceWithAdjusted("2026-07-02", "AAPL", 25, 50),
					priceWithAdjusted("2026-07-03", "AAPL", 25, 50),
				},
				Splits: []Split{{Date: "2026-07-03", Numerator: 4, Denominator: 1, Ratio: 4}},
			}, nil
		}
		return PriceHistory{
			Records: []PriceRecord{priceWithAdjusted("2026-07-03", "AAPL", 25, 50)},
			Splits:  []Split{{Date: "2026-07-03", Numerator: 4, Denominator: 1, Ratio: 4}},
		}, nil
	})

	runner := NewRunner(RunnerConfig{
		Store:    store,
		Provider: provider,
		Clock:    fixedClock(time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)),
	})

	summary, err := runner.CollectTickers(context.Background(), []Company{{Ticker: "AAPL"}})
	if err != nil {
		t.Fatalf("CollectTickers() error = %v", err)
	}
	if len(calls) != 2 || calls[0].start != "2026-07-03" || calls[1].start != "" {
		t.Fatalf("expected incremental then full calls, got %+v", calls)
	}
	if summary.SplitDetected != 1 || summary.FullRewritten != 1 || summary.IncrementalUpdated != 0 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	meta, _, err := store.LoadMeta("AAPL")
	if err != nil {
		t.Fatalf("LoadMeta() error = %v", err)
	}
	if meta.LastSplitDate != "2026-07-03" || !meta.AdjustedSeriesValidated {
		t.Fatalf("unexpected meta after split rewrite: %+v", meta)
	}
}

func TestRunnerDetectsPriceDiscontinuityAndRewritesFullHistory(t *testing.T) {
	root := t.TempDir()
	store := NewFileStore(root)
	updatedAt := time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)
	if _, _, err := store.RewriteTickerData("AAPL", []PriceRecord{
		priceWithAdjusted("2026-07-01", "AAPL", 100, 50),
		priceWithAdjusted("2026-07-02", "AAPL", 100, 50),
	}, nil, updatedAt, RewriteOptions{
		BackfillCompleted:       true,
		AdjustedSeriesValidated: true,
		FullValidationAt:        updatedAt,
	}); err != nil {
		t.Fatalf("RewriteTickerData() error = %v", err)
	}

	var calls []providerCall
	provider := fakeProviderFunc(func(ctx context.Context, ticker string, start time.Time, end time.Time) (PriceHistory, error) {
		calls = append(calls, providerCall{ticker: ticker, start: formatCallDate(start), end: FormatDate(end)})
		if start.IsZero() {
			return PriceHistory{Records: []PriceRecord{
				priceWithAdjusted("2026-07-01", "AAPL", 25, 50),
				priceWithAdjusted("2026-07-02", "AAPL", 25, 50),
				priceWithAdjusted("2026-07-03", "AAPL", 25, 50),
			}}, nil
		}
		return PriceHistory{Records: []PriceRecord{priceWithAdjusted("2026-07-03", "AAPL", 25, 50)}}, nil
	})

	runner := NewRunner(RunnerConfig{
		Store:    store,
		Provider: provider,
		Clock:    fixedClock(time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)),
	})

	summary, err := runner.CollectTickers(context.Background(), []Company{{Ticker: "AAPL"}})
	if err != nil {
		t.Fatalf("CollectTickers() error = %v", err)
	}
	if len(calls) != 2 || calls[0].start != "2026-07-03" || calls[1].start != "" {
		t.Fatalf("expected incremental then full calls, got %+v", calls)
	}
	if summary.DiscontinuityDetected != 1 || summary.FullRewritten != 1 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
}

func TestRunnerPeriodicValidationRewritesEvenWhenLatestDataIsCurrent(t *testing.T) {
	root := t.TempDir()
	store := NewFileStore(root)
	oldValidation := time.Date(2026, 6, 26, 0, 0, 0, 0, time.UTC)
	if _, _, err := store.RewriteTickerData("AAPL", []PriceRecord{price("2026-07-03", "AAPL")}, nil, oldValidation, RewriteOptions{
		BackfillCompleted:       true,
		AdjustedSeriesValidated: true,
		FullValidationAt:        oldValidation,
	}); err != nil {
		t.Fatalf("RewriteTickerData() error = %v", err)
	}
	provider := &fakeProvider{history: PriceHistory{Records: []PriceRecord{price("2026-07-03", "AAPL")}}}

	runner := NewRunner(RunnerConfig{
		Store:              store,
		Provider:           provider,
		FullValidationDays: 7,
		Clock:              fixedClock(time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)),
	})

	summary, err := runner.CollectTickers(context.Background(), []Company{{Ticker: "AAPL"}})
	if err != nil {
		t.Fatalf("CollectTickers() error = %v", err)
	}
	if len(provider.calls) != 1 || provider.calls[0].start != "" {
		t.Fatalf("expected full validation call, got %+v", provider.calls)
	}
	if summary.FullRewritten != 1 || summary.AdjustedValidated != 1 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
}

func TestRunnerLogsProviderFailureAndContinuesWithNextTicker(t *testing.T) {
	root := t.TempDir()
	store := NewFileStore(root)
	var logBuffer bytes.Buffer
	provider := fakeProviderFunc(func(ctx context.Context, ticker string, start time.Time, end time.Time) (PriceHistory, error) {
		if ticker == "AAC" {
			return PriceHistory{}, errors.New("Yahoo request for AAC failed: 404 Not Found; Stooq request for AAC failed: no data")
		}
		return PriceHistory{Records: []PriceRecord{price("2026-07-03", ticker)}}, nil
	})

	runner := NewRunner(RunnerConfig{
		Store:     store,
		Provider:  provider,
		StartDate: mustDate(t, "2026-07-03"),
		Clock:     fixedClock(time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)),
		LogWriter: &logBuffer,
	})

	summary, err := runner.CollectTickers(context.Background(), []Company{{Ticker: "AAC"}, {Ticker: "AAPL"}})
	if err != nil {
		t.Fatalf("CollectTickers() error = %v", err)
	}
	if summary.Processed != 2 || summary.Failed != 1 || summary.Appended != 1 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	if !strings.Contains(logBuffer.String(), "AAC fetch history failed") || !strings.Contains(logBuffer.String(), "404 Not Found") {
		t.Fatalf("expected AAC provider failure in log, got %q", logBuffer.String())
	}

	meta, ok, err := store.LoadMeta("AAPL")
	if err != nil {
		t.Fatalf("LoadMeta(AAPL) error = %v", err)
	}
	if !ok || meta.LastDate != "2026-07-03" || meta.Records != 1 {
		t.Fatalf("expected AAPL to continue and append, got ok=%v meta=%+v", ok, meta)
	}
}

func price(date string, ticker string) PriceRecord {
	return priceWithClose(date, ticker, 2)
}

func priceWithClose(date string, ticker string, close float64) PriceRecord {
	return priceWithAdjusted(date, ticker, close, close)
}

func priceWithAdjusted(date string, ticker string, close float64, adjClose float64) PriceRecord {
	ratio := 1.0
	if close != 0 {
		ratio = adjClose / close
	}
	return PriceRecord{
		Date:              date,
		Ticker:            ticker,
		Open:              close,
		High:              close * 1.1,
		Low:               close * 0.9,
		Close:             close,
		AdjOpen:           close * ratio,
		AdjHigh:           close * 1.1 * ratio,
		AdjLow:            close * 0.9 * ratio,
		AdjClose:          adjClose,
		Volume:            100,
		Source:            SourceYahoo,
		AdjustmentVersion: AdjustmentVersionYahooChartV1,
	}
}

func mustLoadPrices(t *testing.T, store *FileStore, ticker string) []PriceRecord {
	t.Helper()
	records, ok, err := store.LoadPrices(ticker)
	if err != nil {
		t.Fatalf("LoadPrices(%s) error = %v", ticker, err)
	}
	if !ok {
		t.Fatalf("expected %s price file to exist", ticker)
	}
	return records
}

func mustLoadActions(t *testing.T, store *FileStore, ticker string) []CorporateAction {
	t.Helper()
	actions, ok, err := store.LoadActions(ticker)
	if err != nil {
		t.Fatalf("LoadActions(%s) error = %v", ticker, err)
	}
	if !ok {
		t.Fatalf("expected %s actions file to exist", ticker)
	}
	return actions
}

func fixedClock(now time.Time) func() time.Time {
	return func() time.Time {
		return now
	}
}

func formatCallDate(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return FormatDate(value)
}

func mustDate(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := ParseDate(value)
	if err != nil {
		t.Fatalf("ParseDate(%q) error = %v", value, err)
	}
	return parsed
}
