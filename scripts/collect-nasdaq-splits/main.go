package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"

	"github.com/minjoon/stock-price/internal/nasdaq"
)

type options struct {
	outputDir             string
	requestTimeoutSeconds int
	maxRuntimeMinutes     int
	dryRun                bool
	githubRunID           string
}

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, args []string) error {
	options, err := parseOptions(args)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(options.maxRuntimeMinutes)*time.Minute)
	defer cancel()

	client := nasdaq.NewClient(nasdaq.ClientConfig{
		HTTPClient:   nasdaq.NewHTTPClient(time.Duration(options.requestTimeoutSeconds) * time.Second),
		CurlFallback: nasdaq.DefaultCurlFallback,
	})
	summary, err := nasdaq.CollectSplits(ctx, client, nasdaq.CollectOptions{
		OutputDir:   options.outputDir,
		DryRun:      options.dryRun,
		GitHubRunID: options.githubRunID,
		Logger:      log.Default(),
	})
	if err != nil {
		return err
	}
	fmt.Printf("Nasdaq Splits Status: %s\n", summary.Status)
	fmt.Printf("Rows Fetched: %d\n", summary.RowsFetched)
	fmt.Printf("Canonical Rows: %d\n", summary.CanonicalRows)
	if summary.Error != "" {
		fmt.Printf("Last Error: %s\n", summary.Error)
	}
	return nil
}

func parseOptions(args []string) (options, error) {
	var opts options
	flags := flag.NewFlagSet("collect-nasdaq-splits", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&opts.outputDir, "output-dir", "data/nasdaq/splits", "directory where Nasdaq split JSONL files are stored")
	flags.IntVar(&opts.requestTimeoutSeconds, "request-timeout-seconds", 30, "HTTP request timeout in seconds")
	flags.IntVar(&opts.maxRuntimeMinutes, "max-runtime-minutes", 30, "maximum script runtime in minutes")
	flags.BoolVar(&opts.dryRun, "dry-run", false, "collect and print a summary without writing files")
	flags.StringVar(&opts.githubRunID, "github-run-id", "", "GitHub Actions run id for metadata")
	if err := flags.Parse(args); err != nil {
		return options{}, err
	}
	opts.outputDir = strings.TrimSpace(opts.outputDir)
	opts.githubRunID = strings.TrimSpace(opts.githubRunID)
	if opts.outputDir == "" {
		return options{}, fmt.Errorf("--output-dir is required")
	}
	if opts.requestTimeoutSeconds <= 0 {
		return options{}, fmt.Errorf("--request-timeout-seconds must be greater than 0")
	}
	if opts.maxRuntimeMinutes <= 0 {
		return options{}, fmt.Errorf("--max-runtime-minutes must be greater than 0")
	}
	return opts, nil
}
