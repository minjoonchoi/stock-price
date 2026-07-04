package collector

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestFallbackProviderUsesSecondaryWhenPrimaryFails(t *testing.T) {
	primary := &fakeProvider{err: errors.New("Yahoo request for AAC failed: 404 Not Found")}
	secondary := &fakeProvider{history: PriceHistory{Records: []PriceRecord{{
		Date:     "2026-07-03",
		Ticker:   "AAC",
		Open:     10,
		High:     11,
		Low:      9,
		Close:    10.5,
		AdjClose: 10.5,
		Volume:   1000,
		Source:   SourceStooq,
	}}}}
	provider := NewFallbackProvider(primary, secondary)

	history, err := provider.FetchHistory(context.Background(), "AAC", mustDate(t, "2026-07-03"), mustDate(t, "2026-07-03"))
	if err != nil {
		t.Fatalf("FetchHistory() error = %v", err)
	}
	if len(primary.calls) != 1 || len(secondary.calls) != 1 {
		t.Fatalf("expected both providers to be called, primary=%+v secondary=%+v", primary.calls, secondary.calls)
	}
	if len(history.Records) != 1 || history.Records[0].Source != SourceStooq {
		t.Fatalf("unexpected history: %+v", history)
	}
}

func TestFallbackProviderUsesSecondaryWhenPrimaryReturnsNoRecords(t *testing.T) {
	primary := &fakeProvider{history: PriceHistory{}}
	secondary := &fakeProvider{history: PriceHistory{Records: []PriceRecord{price("2026-07-03", "AAC")}}}
	provider := NewFallbackProvider(primary, secondary)

	history, err := provider.FetchHistory(context.Background(), "AAC", mustDate(t, "2026-07-03"), mustDate(t, "2026-07-03"))
	if err != nil {
		t.Fatalf("FetchHistory() error = %v", err)
	}
	if len(primary.calls) != 1 || len(secondary.calls) != 1 {
		t.Fatalf("expected both providers to be called, primary=%+v secondary=%+v", primary.calls, secondary.calls)
	}
	if len(history.Records) != 1 {
		t.Fatalf("unexpected history: %+v", history)
	}
}

func TestFallbackProviderReturnsCombinedErrorWhenAllProvidersFail(t *testing.T) {
	provider := NewFallbackProvider(
		&fakeProvider{err: errors.New("Yahoo request for AAC failed: 404 Not Found")},
		&fakeProvider{err: errors.New("Stooq request for AAC failed: no data")},
	)

	_, err := provider.FetchHistory(context.Background(), "AAC", time.Time{}, time.Time{})
	if err == nil {
		t.Fatal("expected combined provider error")
	}
	if got := err.Error(); !strings.Contains(got, "Yahoo request for AAC failed") || !strings.Contains(got, "Stooq request for AAC failed") {
		t.Fatalf("combined error = %q", got)
	}
}
