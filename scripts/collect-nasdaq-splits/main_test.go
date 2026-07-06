package main

import "testing"

func TestParseOptionsDefaultsForSplits(t *testing.T) {
	options, err := parseOptions([]string{})
	if err != nil {
		t.Fatalf("parseOptions() error = %v", err)
	}
	if options.outputDir != "data/nasdaq/splits" {
		t.Fatalf("outputDir = %q, want data/nasdaq/splits", options.outputDir)
	}
	if options.requestTimeoutSeconds != 30 || options.maxRuntimeMinutes != 30 {
		t.Fatalf("unexpected timeout defaults: %+v", options)
	}
	if options.dryRun {
		t.Fatal("dryRun default should be false")
	}
}

func TestParseOptionsReadsSplitsFlags(t *testing.T) {
	options, err := parseOptions([]string{
		"--output-dir", "/tmp/splits",
		"--request-timeout-seconds", "5",
		"--max-runtime-minutes", "7",
		"--dry-run=true",
		"--github-run-id", "12345",
	})
	if err != nil {
		t.Fatalf("parseOptions() error = %v", err)
	}
	if options.outputDir != "/tmp/splits" || options.requestTimeoutSeconds != 5 || options.maxRuntimeMinutes != 7 || !options.dryRun || options.githubRunID != "12345" {
		t.Fatalf("unexpected options: %+v", options)
	}
}
