package main

import (
	"os"
	"strings"
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
		"--sec-user-agent", "github-stock-collector test@example.com",
		"--ticker", "aapl, msft",
		"--max-tickers", "2",
		"--workers", "4",
		"--sleep-ms", "250",
		"--universe-file", "tmp/universe/collectable_tickers.jsonl",
		"--allow-all-sec-tickers",
		"--force-backfill",
		"--repair-meta",
		"--force-validate-adjusted",
		"--full-validation-days", "3",
		"--disable-price-discontinuity-check",
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
	if options.workers != 4 {
		t.Fatalf("workers = %d", options.workers)
	}
	if options.universeFile != "tmp/universe/collectable_tickers.jsonl" {
		t.Fatalf("universeFile = %q", options.universeFile)
	}
	if !options.allowAllSECTickers {
		t.Fatal("expected allowAllSECTickers to be true")
	}
	if !options.forceBackfill {
		t.Fatal("expected forceBackfill to be true")
	}
	if !options.repairMeta {
		t.Fatal("expected repairMeta to be true")
	}
	if !options.forceValidateAdjusted {
		t.Fatal("expected forceValidateAdjusted to be true")
	}
	if options.fullValidationDays != 3 {
		t.Fatalf("fullValidationDays = %d", options.fullValidationDays)
	}
	if !options.disablePriceDiscontinuityCheck {
		t.Fatal("expected disablePriceDiscontinuityCheck to be true")
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

func TestMainDoesNotWireRemovedProvider(t *testing.T) {
	raw, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("ReadFile(main.go) error = %v", err)
	}
	source := string(raw)
	removedProvider := strings.Join([]string{"Sto", "oq"}, "")
	removedFallback := "Fallback" + "Provider"
	for _, forbidden := range []string{"New" + removedFallback, "New" + removedProvider + "Provider", removedProvider + "ProviderConfig"} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("main.go still references %s", forbidden)
		}
	}
}

func TestCollectPricesWorkflowRunsRepeatedDailyCursorWindow(t *testing.T) {
	raw, err := os.ReadFile("../../.github/workflows/collect-prices.yml")
	if err != nil {
		t.Fatalf("ReadFile(collect-prices.yml) error = %v", err)
	}
	workflow := string(raw)
	if !strings.Contains(workflow, `cron: "0 1-17/2 * * 2-6"`) {
		t.Fatalf("workflow missing repeated daily cursor schedule")
	}
}

func TestCollectPricesWorkflowDeclaresLongRunningControls(t *testing.T) {
	raw, err := os.ReadFile("../../.github/workflows/collect-prices.yml")
	if err != nil {
		t.Fatalf("ReadFile(collect-prices.yml) error = %v", err)
	}
	workflow := string(raw)
	for _, expected := range []string{
		"timeout-minutes: 350",
		"shard_count:",
		"default: \"1\"",
		"shard_index:",
		"default: \"0\"",
		"batch_size:",
		"default: \"500\"",
		"max_runtime_minutes:",
		"default: \"330\"",
		"graceful_stop_minutes:",
		"default: \"10\"",
		"data/prices data/actions data/state",
	} {
		if !strings.Contains(workflow, expected) {
			t.Fatalf("workflow missing %s", expected)
		}
	}
}

func TestParseOptionsDefaultsUniverseFilterToRequiredFile(t *testing.T) {
	t.Setenv("SEC_USER_AGENT", "github-stock-collector test@example.com")

	options, err := parseOptions(nil)
	if err != nil {
		t.Fatalf("parseOptions() error = %v", err)
	}
	if options.universeFile != "data/universe/collectable_tickers.jsonl" {
		t.Fatalf("universeFile = %q", options.universeFile)
	}
	if options.allowAllSECTickers {
		t.Fatal("allowAllSECTickers should default false")
	}
	if options.workers != 4 {
		t.Fatalf("workers = %d", options.workers)
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
	if options.batchSize != 500 {
		t.Fatalf("batchSize = %d", options.batchSize)
	}
	if options.stateFile != "data/state/collect-prices.state.json" {
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
		"--state-file", "tmp/collect.state.json",
		"--shard-index", "2",
		"--shard-count", "10",
	})
	if err != nil {
		t.Fatalf("parseOptions() error = %v", err)
	}

	if options.maxRuntime != 120*time.Minute || options.gracefulStop != 5*time.Minute || options.batchSize != 25 || options.stateFile != "tmp/collect.state.json" || options.shardIndex != 2 || options.shardCount != 10 {
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
