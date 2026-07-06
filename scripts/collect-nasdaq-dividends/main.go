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
	date                  string
	fromDate              string
	toDate                string
	lookbackDays          int
	lookaheadDays         int
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
	dates, err := collectionDates(options, time.Now().UTC())
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(options.maxRuntimeMinutes)*time.Minute)
	defer cancel()

	client := nasdaq.NewClient(nasdaq.ClientConfig{
		HTTPClient:   nasdaq.NewHTTPClient(time.Duration(options.requestTimeoutSeconds) * time.Second),
		CurlFallback: nasdaq.DefaultCurlFallback,
	})
	summary, err := nasdaq.CollectDividends(ctx, client, dates, nasdaq.CollectOptions{
		OutputDir:   options.outputDir,
		DryRun:      options.dryRun,
		GitHubRunID: options.githubRunID,
		Logger:      log.Default(),
	})
	if err != nil {
		return err
	}
	fmt.Printf("Nasdaq Dividends Status: %s\n", summary.Status)
	fmt.Printf("Dates Requested: %d\n", len(dates))
	fmt.Printf("Rows Fetched: %d\n", summary.RowsFetched)
	fmt.Printf("Canonical Rows: %d\n", summary.CanonicalRows)
	if summary.Error != "" {
		fmt.Printf("Last Error: %s\n", summary.Error)
	}
	return nil
}

func parseOptions(args []string) (options, error) {
	var opts options
	flags := flag.NewFlagSet("collect-nasdaq-dividends", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&opts.outputDir, "output-dir", "data/nasdaq/dividends", "directory where Nasdaq dividend JSONL files are stored")
	flags.IntVar(&opts.requestTimeoutSeconds, "request-timeout-seconds", 30, "HTTP request timeout in seconds")
	flags.IntVar(&opts.maxRuntimeMinutes, "max-runtime-minutes", 30, "maximum script runtime in minutes")
	flags.BoolVar(&opts.dryRun, "dry-run", false, "collect and print a summary without writing files")
	flags.StringVar(&opts.githubRunID, "github-run-id", "", "GitHub Actions run id for metadata")
	flags.StringVar(&opts.date, "date", "", "single dividend calendar date, YYYY-MM-DD")
	flags.StringVar(&opts.fromDate, "from-date", "", "first dividend calendar date, YYYY-MM-DD")
	flags.StringVar(&opts.toDate, "to-date", "", "last dividend calendar date, YYYY-MM-DD")
	flags.IntVar(&opts.lookbackDays, "lookback-days", 7, "days to recollect in the past")
	flags.IntVar(&opts.lookaheadDays, "lookahead-days", 30, "days to collect into the future")
	if err := flags.Parse(args); err != nil {
		return options{}, err
	}
	opts.outputDir = strings.TrimSpace(opts.outputDir)
	opts.githubRunID = strings.TrimSpace(opts.githubRunID)
	opts.date = strings.TrimSpace(opts.date)
	opts.fromDate = strings.TrimSpace(opts.fromDate)
	opts.toDate = strings.TrimSpace(opts.toDate)
	if opts.outputDir == "" {
		return options{}, fmt.Errorf("--output-dir is required")
	}
	if opts.requestTimeoutSeconds <= 0 {
		return options{}, fmt.Errorf("--request-timeout-seconds must be greater than 0")
	}
	if opts.maxRuntimeMinutes <= 0 {
		return options{}, fmt.Errorf("--max-runtime-minutes must be greater than 0")
	}
	if opts.lookbackDays < 0 {
		return options{}, fmt.Errorf("--lookback-days must be 0 or greater")
	}
	if opts.lookaheadDays < 0 {
		return options{}, fmt.Errorf("--lookahead-days must be 0 or greater")
	}
	return opts, nil
}

func collectionDates(options options, now time.Time) ([]string, error) {
	if options.date != "" {
		date, err := parseDate(options.date)
		if err != nil {
			return nil, fmt.Errorf("--date must be YYYY-MM-DD: %w", err)
		}
		return []string{date.Format("2006-01-02")}, nil
	}
	if options.fromDate != "" || options.toDate != "" {
		if options.fromDate == "" || options.toDate == "" {
			return nil, fmt.Errorf("--from-date and --to-date must be provided together")
		}
		from, err := parseDate(options.fromDate)
		if err != nil {
			return nil, fmt.Errorf("--from-date must be YYYY-MM-DD: %w", err)
		}
		to, err := parseDate(options.toDate)
		if err != nil {
			return nil, fmt.Errorf("--to-date must be YYYY-MM-DD: %w", err)
		}
		return datesBetween(from, to)
	}
	today := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day(), 0, 0, 0, 0, time.UTC)
	from := today.AddDate(0, 0, -options.lookbackDays)
	to := today.AddDate(0, 0, options.lookaheadDays)
	return datesBetween(from, to)
}

func parseDate(value string) (time.Time, error) {
	return time.ParseInLocation("2006-01-02", value, time.UTC)
}

func datesBetween(from time.Time, to time.Time) ([]string, error) {
	if to.Before(from) {
		return nil, fmt.Errorf("--to-date must be on or after --from-date")
	}
	dates := make([]string, 0)
	for date := from; !date.After(to); date = date.AddDate(0, 0, 1) {
		dates = append(dates, date.Format("2006-01-02"))
	}
	return dates, nil
}
