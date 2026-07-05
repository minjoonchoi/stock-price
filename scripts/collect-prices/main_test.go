package main

import (
	"testing"
	"time"
)

func TestParseOptionsRequiresUserAgent(t *testing.T) {
	t.Setenv("SEC_USER_AGENT", "")

	_, err := parseOptions([]string{"--start-date", "2026-01-01"})
	if err == nil {
		t.Fatal("expected missing user-agent error")
	}
}

func TestParseOptionsReadsFlagsAndTickerList(t *testing.T) {
	t.Setenv("SEC_USER_AGENT", "")

	options, err := parseOptions([]string{
		"--start-date", "2026-01-01",
		"--data-dir", "tmp/prices",
		"--user-agent", "github-stock-collector test@example.com",
		"--ticker", "aapl, msft",
		"--limit", "2",
		"--request-delay", "250ms",
		"--force-backfill",
		"--repair-meta",
	})
	if err != nil {
		t.Fatalf("parseOptions() error = %v", err)
	}

	if options.startDate != "2026-01-01" {
		t.Fatalf("startDate = %q", options.startDate)
	}
	if options.dataDir != "tmp/prices" {
		t.Fatalf("dataDir = %q", options.dataDir)
	}
	if options.userAgent != "github-stock-collector test@example.com" {
		t.Fatalf("userAgent = %q", options.userAgent)
	}
	if options.limit != 2 {
		t.Fatalf("limit = %d", options.limit)
	}
	if options.requestDelay != 250*time.Millisecond {
		t.Fatalf("requestDelay = %s", options.requestDelay)
	}
	if !options.forceBackfill {
		t.Fatal("expected forceBackfill to be true")
	}
	if !options.repairMeta {
		t.Fatal("expected repairMeta to be true")
	}
	if len(options.tickers) != 2 || options.tickers[0] != "AAPL" || options.tickers[1] != "MSFT" {
		t.Fatalf("tickers = %+v", options.tickers)
	}
}

func TestParseOptionsDefaultsRequestDelayFromEnvironment(t *testing.T) {
	t.Setenv("SEC_USER_AGENT", "github-stock-collector test@example.com")
	t.Setenv("PRICE_REQUEST_DELAY", "1500ms")

	options, err := parseOptions(nil)
	if err != nil {
		t.Fatalf("parseOptions() error = %v", err)
	}
	if options.requestDelay != 1500*time.Millisecond {
		t.Fatalf("requestDelay = %s", options.requestDelay)
	}
}

func TestParseOptionsDefaultsToDynamicStartDate(t *testing.T) {
	t.Setenv("SEC_USER_AGENT", "github-stock-collector test@example.com")

	options, err := parseOptions(nil)
	if err != nil {
		t.Fatalf("parseOptions() error = %v", err)
	}
	if options.startDate != "" {
		t.Fatalf("expected dynamic start date by default, got %q", options.startDate)
	}
}

func TestParseOptionsAllowsRepairMetaWithoutUserAgent(t *testing.T) {
	t.Setenv("SEC_USER_AGENT", "")

	options, err := parseOptions([]string{"--repair-meta", "--data-dir", "tmp/prices"})
	if err != nil {
		t.Fatalf("parseOptions() error = %v", err)
	}
	if !options.repairMeta {
		t.Fatal("expected repairMeta to be true")
	}
}
