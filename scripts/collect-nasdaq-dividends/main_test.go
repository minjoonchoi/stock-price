package main

import "testing"

func TestParseOptionsDefaultsForDividends(t *testing.T) {
	options, err := parseOptions([]string{})
	if err != nil {
		t.Fatalf("parseOptions() error = %v", err)
	}
	if options.outputDir != "data/nasdaq/dividends" {
		t.Fatalf("outputDir = %q, want data/nasdaq/dividends", options.outputDir)
	}
	if options.lookbackDays != 7 || options.lookaheadDays != 30 {
		t.Fatalf("unexpected date window defaults: %+v", options)
	}
	if options.requestTimeoutSeconds != 30 || options.maxRuntimeMinutes != 30 {
		t.Fatalf("unexpected timeout defaults: %+v", options)
	}
}

func TestParseOptionsReadsDividendsDateFlags(t *testing.T) {
	options, err := parseOptions([]string{
		"--date", "2026-07-06",
		"--from-date", "2026-07-01",
		"--to-date", "2026-07-10",
		"--lookback-days", "2",
		"--lookahead-days", "4",
		"--output-dir", "/tmp/dividends",
		"--dry-run=true",
		"--github-run-id", "67890",
	})
	if err != nil {
		t.Fatalf("parseOptions() error = %v", err)
	}
	if options.date != "2026-07-06" || options.fromDate != "2026-07-01" || options.toDate != "2026-07-10" || options.lookbackDays != 2 || options.lookaheadDays != 4 || options.outputDir != "/tmp/dividends" || !options.dryRun || options.githubRunID != "67890" {
		t.Fatalf("unexpected options: %+v", options)
	}
}
