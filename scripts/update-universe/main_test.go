package main

import (
	"os"
	"strings"
	"testing"
	"time"

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

func TestParseOptionsDefaultsLongRunningControls(t *testing.T) {
	t.Setenv("SEC_USER_AGENT", "github-stock-collector test@example.com")

	options, err := parseOptions(nil)
	if err != nil {
		t.Fatalf("parseOptions() error = %v", err)
	}

	if options.maxRuntime != 330*time.Minute {
		t.Fatalf("maxRuntime = %s", options.maxRuntime)
	}
	if options.gracefulStop != 10*time.Minute {
		t.Fatalf("gracefulStop = %s", options.gracefulStop)
	}
	if options.batchSize != 1000 {
		t.Fatalf("batchSize = %d", options.batchSize)
	}
	if options.stateFile != "data/state/update-universe.state.json" {
		t.Fatalf("stateFile = %q", options.stateFile)
	}
	if options.shardIndex != 0 || options.shardCount != 1 {
		t.Fatalf("shard = %d/%d", options.shardIndex, options.shardCount)
	}
}

func TestParseOptionsReadsLongRunningControls(t *testing.T) {
	t.Setenv("SEC_USER_AGENT", "github-stock-collector test@example.com")

	options, err := parseOptions([]string{
		"--max-runtime-minutes", "120",
		"--graceful-stop-minutes", "5",
		"--batch-size", "25",
		"--state-file", "tmp/update.state.json",
		"--shard-index", "2",
		"--shard-count", "10",
	})
	if err != nil {
		t.Fatalf("parseOptions() error = %v", err)
	}

	if options.maxRuntime != 120*time.Minute || options.gracefulStop != 5*time.Minute || options.batchSize != 25 || options.stateFile != "tmp/update.state.json" || options.shardIndex != 2 || options.shardCount != 10 {
		t.Fatalf("unexpected options: %+v", options)
	}
}

func TestParseOptionsRejectsInvalidShard(t *testing.T) {
	t.Setenv("SEC_USER_AGENT", "github-stock-collector test@example.com")

	_, err := parseOptions([]string{"--shard-index", "2", "--shard-count", "2"})
	if err == nil {
		t.Fatal("expected invalid shard error")
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
		"timeout-minutes: 350",
		"shard_count:",
		"default: \"1\"",
		"shard_index:",
		"batch_size:",
		"default: \"1000\"",
		"max_runtime_minutes:",
		"default: \"330\"",
		"graceful_stop_minutes:",
		"default: \"10\"",
		"data/universe data/state",
		`cron: "0 21,23 * * 0"`,
		`cron: "0 1-15/2 * * 1"`,
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
