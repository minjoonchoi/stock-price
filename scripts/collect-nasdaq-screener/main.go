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
	limit                 int
	marketCap             string
	recommendation        string
	country               string
	tableOnly             bool
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
	summary, err := nasdaq.CollectScreener(ctx, client, nasdaq.ScreenerOptions{
		Limit:          options.limit,
		MarketCap:      options.marketCap,
		Recommendation: options.recommendation,
		Country:        options.country,
		TableOnly:      options.tableOnly,
	}, nasdaq.CollectOptions{
		OutputDir:   options.outputDir,
		DryRun:      options.dryRun,
		GitHubRunID: options.githubRunID,
		Logger:      log.Default(),
	})
	if err != nil {
		return err
	}
	fmt.Printf("Nasdaq Screener Status: %s\n", summary.Status)
	fmt.Printf("Rows Fetched: %d\n", summary.RowsFetched)
	fmt.Printf("Canonical Rows: %d\n", summary.CanonicalRows)
	if summary.Error != "" {
		fmt.Printf("Last Error: %s\n", summary.Error)
	}
	return nil
}

func parseOptions(args []string) (options, error) {
	var opts options
	flags := flag.NewFlagSet("collect-nasdaq-screener", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&opts.outputDir, "output-dir", "data/nasdaq/screener", "directory where Nasdaq screener JSONL files are stored")
	flags.IntVar(&opts.requestTimeoutSeconds, "request-timeout-seconds", 30, "HTTP request timeout in seconds")
	flags.IntVar(&opts.maxRuntimeMinutes, "max-runtime-minutes", 30, "maximum script runtime in minutes")
	flags.BoolVar(&opts.dryRun, "dry-run", false, "collect and print a summary without writing files")
	flags.StringVar(&opts.githubRunID, "github-run-id", "", "GitHub Actions run id for metadata")
	flags.IntVar(&opts.limit, "limit", 1500, "Nasdaq screener limit")
	flags.StringVar(&opts.marketCap, "marketcap", "mega|large|mid", "market cap filter")
	flags.StringVar(&opts.recommendation, "recommendation", "strong_buy|buy", "recommendation filter")
	flags.StringVar(&opts.country, "country", "united_states", "country filter")
	flags.BoolVar(&opts.tableOnly, "tableonly", false, "Nasdaq tableonly query parameter")
	if err := flags.Parse(args); err != nil {
		return options{}, err
	}
	opts.outputDir = strings.TrimSpace(opts.outputDir)
	opts.githubRunID = strings.TrimSpace(opts.githubRunID)
	opts.marketCap = strings.TrimSpace(opts.marketCap)
	opts.recommendation = strings.TrimSpace(opts.recommendation)
	opts.country = strings.TrimSpace(opts.country)
	if opts.outputDir == "" {
		return options{}, fmt.Errorf("--output-dir is required")
	}
	if opts.requestTimeoutSeconds <= 0 {
		return options{}, fmt.Errorf("--request-timeout-seconds must be greater than 0")
	}
	if opts.maxRuntimeMinutes <= 0 {
		return options{}, fmt.Errorf("--max-runtime-minutes must be greater than 0")
	}
	if opts.limit <= 0 {
		return options{}, fmt.Errorf("--limit must be greater than 0")
	}
	if opts.marketCap == "" {
		return options{}, fmt.Errorf("--marketcap is required")
	}
	if opts.recommendation == "" {
		return options{}, fmt.Errorf("--recommendation is required")
	}
	if opts.country == "" {
		return options{}, fmt.Errorf("--country is required")
	}
	return opts, nil
}
