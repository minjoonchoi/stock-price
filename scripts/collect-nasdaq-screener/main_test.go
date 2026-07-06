package main

import "testing"

func TestParseOptionsDefaultsForScreener(t *testing.T) {
	options, err := parseOptions([]string{})
	if err != nil {
		t.Fatalf("parseOptions() error = %v", err)
	}
	if options.outputDir != "data/nasdaq/screener" {
		t.Fatalf("outputDir = %q, want data/nasdaq/screener", options.outputDir)
	}
	if options.limit != 1500 || options.marketCap != "mega|large|mid" || options.recommendation != "strong_buy|buy" || options.country != "united_states" || options.tableOnly {
		t.Fatalf("unexpected screener defaults: %+v", options)
	}
	if options.requestTimeoutSeconds != 30 || options.maxRuntimeMinutes != 30 {
		t.Fatalf("unexpected timeout defaults: %+v", options)
	}
}

func TestParseOptionsReadsScreenerFlags(t *testing.T) {
	options, err := parseOptions([]string{
		"--limit", "25",
		"--marketcap", "mega",
		"--recommendation", "buy",
		"--country", "united_states",
		"--tableonly=true",
		"--output-dir", "/tmp/screener",
		"--dry-run=true",
		"--github-run-id", "abc",
	})
	if err != nil {
		t.Fatalf("parseOptions() error = %v", err)
	}
	if options.limit != 25 || options.marketCap != "mega" || options.recommendation != "buy" || options.country != "united_states" || !options.tableOnly || options.outputDir != "/tmp/screener" || !options.dryRun || options.githubRunID != "abc" {
		t.Fatalf("unexpected options: %+v", options)
	}
}
