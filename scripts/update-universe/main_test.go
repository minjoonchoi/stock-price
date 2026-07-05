package main

import (
	"os"
	"strings"
	"testing"

	"github.com/minjoon/stock-price/internal/collector"
)

func TestParseOptionsReadsUniverseFlags(t *testing.T) {
	t.Setenv("SEC_USER_AGENT", "")

	options, err := parseOptions([]string{
		"--min-market-cap", "300000000",
		"--max-tickers", "10",
		"--workers", "4",
		"--sleep-ms", "150",
		"--sec-user-agent", "github-stock-collector test@example.com",
		"--output-dir", "tmp/universe",
	})
	if err != nil {
		t.Fatalf("parseOptions() error = %v", err)
	}

	if options.minMarketCap != 300_000_000 {
		t.Fatalf("minMarketCap = %d", options.minMarketCap)
	}
	if options.maxTickers != 10 {
		t.Fatalf("maxTickers = %d", options.maxTickers)
	}
	if options.workers != 4 {
		t.Fatalf("workers = %d", options.workers)
	}
	if options.sleepMS != 150 {
		t.Fatalf("sleepMS = %d", options.sleepMS)
	}
	if options.secUserAgent != "github-stock-collector test@example.com" {
		t.Fatalf("secUserAgent = %q", options.secUserAgent)
	}
	if options.outputDir != "tmp/universe" {
		t.Fatalf("outputDir = %q", options.outputDir)
	}
}

func TestParseOptionsDefaultsUniverseRateLimit(t *testing.T) {
	t.Setenv("SEC_USER_AGENT", "github-stock-collector test@example.com")

	options, err := parseOptions(nil)
	if err != nil {
		t.Fatalf("parseOptions() error = %v", err)
	}

	if options.minMarketCap != collector.DefaultMinMarketCap {
		t.Fatalf("minMarketCap = %d", options.minMarketCap)
	}
	if options.maxTickers != 0 {
		t.Fatalf("maxTickers = %d", options.maxTickers)
	}
	if options.workers != 4 {
		t.Fatalf("workers = %d", options.workers)
	}
	if options.sleepMS != 2000 {
		t.Fatalf("sleepMS = %d", options.sleepMS)
	}
}

func TestUpdateUniverseWorkflowDeclaresManualInputDefaults(t *testing.T) {
	raw, err := os.ReadFile("../../.github/workflows/update-universe.yml")
	if err != nil {
		t.Fatalf("ReadFile(update-universe.yml) error = %v", err)
	}
	workflow := string(raw)
	for _, expected := range []string{
		"default: \"300000000\"",
		"default: \"0\"",
		"default: \"4\"",
		"default: \"2000\"",
	} {
		if !strings.Contains(workflow, expected) {
			t.Fatalf("workflow missing %s", expected)
		}
	}
}

func TestParseOptionsRequiresSECUserAgent(t *testing.T) {
	t.Setenv("SEC_USER_AGENT", "")

	_, err := parseOptions(nil)
	if err == nil {
		t.Fatal("expected missing sec user agent error")
	}
}

func TestValidateUniverseUpdateResultRejectsEmptyFullUniverse(t *testing.T) {
	err := validateUniverseUpdateResult(collector.UniverseUpdateResult{
		Summary: collector.UniverseUpdateSummary{
			SECTickersTotal:        9000,
			YahooMarketCapRequests: 9000,
			CollectableTickers:     0,
			ExcludedTickers:        9000,
			MissingMarketCap:       0,
			BelowThreshold:         0,
			YahooErrors:            9000,
		},
	}, options{maxTickers: 0})
	if err == nil {
		t.Fatal("expected empty full universe error")
	}
	for _, expected := range []string{
		"zero collectable tickers",
		"secTickers=9000",
		"yahooRequests=9000",
		"excluded=9000",
		"yahooErrors=9000",
	} {
		if !strings.Contains(err.Error(), expected) {
			t.Fatalf("error %q missing %q", err.Error(), expected)
		}
	}
}

func TestValidateUniverseUpdateResultAllowsEmptyLimitedSmokeRun(t *testing.T) {
	err := validateUniverseUpdateResult(collector.UniverseUpdateResult{
		Summary: collector.UniverseUpdateSummary{
			SECTickersTotal:    1,
			CollectableTickers: 0,
		},
	}, options{maxTickers: 1})
	if err != nil {
		t.Fatalf("validateUniverseUpdateResult() error = %v", err)
	}
}
