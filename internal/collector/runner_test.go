package collector

import (
	"bytes"
	"context"
	"errors"
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
	if err := store.WriteMeta("AAPL", Meta{
		LastDate:  "2026-07-03",
		Records:   10,
		UpdatedAt: "2026-07-04T00:00:00Z",
		Source:    SourceYahoo,
	}); err != nil {
		t.Fatalf("WriteMeta() error = %v", err)
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

func TestRunnerFetchesFromDayAfterLastDateAndAppendsOnlyNewRows(t *testing.T) {
	root := t.TempDir()
	store := NewFileStore(root)
	if err := store.WriteMeta("AAPL", Meta{
		LastDate:  "2026-07-01",
		Records:   5,
		UpdatedAt: "2026-07-02T00:00:00Z",
		Source:    SourceYahoo,
	}); err != nil {
		t.Fatalf("WriteMeta() error = %v", err)
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
	if provider.calls[0] != (providerCall{ticker: "AAPL", start: "2026-07-02", end: "2026-07-03"}) {
		t.Fatalf("unexpected provider call: %+v", provider.calls[0])
	}
	if summary.Appended != 2 || summary.Skipped != 0 {
		t.Fatalf("unexpected summary: %+v", summary)
	}

	meta, ok, err := store.LoadMeta("AAPL")
	if err != nil {
		t.Fatalf("LoadMeta() error = %v", err)
	}
	if !ok {
		t.Fatal("expected meta to exist")
	}
	if meta.LastDate != "2026-07-03" || meta.Records != 7 {
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
	return PriceRecord{
		Date:     date,
		Ticker:   ticker,
		Open:     1,
		High:     2,
		Low:      1,
		Close:    2,
		AdjClose: 2,
		Volume:   100,
		Source:   SourceYahoo,
	}
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
