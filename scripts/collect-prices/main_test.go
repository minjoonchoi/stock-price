package main

import "testing"

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
	if len(options.tickers) != 2 || options.tickers[0] != "AAPL" || options.tickers[1] != "MSFT" {
		t.Fatalf("tickers = %+v", options.tickers)
	}
}
