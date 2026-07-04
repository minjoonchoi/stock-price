package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/minjoon/stock-price/internal/collector"
)

type options struct {
	startDate string
	dataDir   string
	userAgent string
	tickers   []string
	limit     int
	timeout   time.Duration
}

func main() {
	ctx := context.Background()
	if err := run(ctx, os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, args []string) error {
	options, err := parseOptions(args)
	if err != nil {
		return err
	}

	startDate, err := collector.ParseDate(options.startDate)
	if err != nil {
		return fmt.Errorf("parse start date: %w", err)
	}

	httpClient := &http.Client{Timeout: options.timeout}
	store := collector.NewFileStore(options.dataDir)
	provider := collector.NewYahooProvider(collector.YahooProviderConfig{
		UserAgent: options.userAgent,
		Client:    httpClient,
	})

	companies, err := companiesForRun(ctx, options, httpClient)
	if err != nil {
		return err
	}
	if options.limit > 0 && len(companies) > options.limit {
		companies = companies[:options.limit]
	}

	runner := collector.NewRunner(collector.RunnerConfig{
		Store:     store,
		Provider:  provider,
		StartDate: startDate,
	})
	summary, err := runner.CollectTickers(ctx, companies)
	if err != nil {
		return err
	}

	fmt.Printf("processed=%d skipped=%d appended=%d failed=%d\n", summary.Processed, summary.Skipped, summary.Appended, summary.Failed)
	return nil
}

func parseOptions(args []string) (options, error) {
	var opts options
	var tickerValues []string

	flags := flag.NewFlagSet("collect-prices", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&opts.startDate, "start-date", "1970-01-01", "initial collection date in YYYY-MM-DD format")
	flags.StringVar(&opts.dataDir, "data-dir", "data/prices", "directory where per-ticker JSONL and meta files are stored")
	flags.StringVar(&opts.userAgent, "user-agent", os.Getenv("SEC_USER_AGENT"), "User-Agent header for SEC and Yahoo requests")
	flags.IntVar(&opts.limit, "limit", 0, "maximum number of tickers to process; 0 means all")
	flags.DurationVar(&opts.timeout, "timeout", 30*time.Second, "HTTP request timeout")
	flags.Func("ticker", "ticker or comma-separated tickers to collect instead of fetching the SEC list; can be repeated", func(value string) error {
		tickerValues = append(tickerValues, splitTickers(value)...)
		return nil
	})

	if err := flags.Parse(args); err != nil {
		return options{}, err
	}
	opts.userAgent = strings.TrimSpace(opts.userAgent)
	if opts.userAgent == "" {
		return options{}, errors.New("missing --user-agent or SEC_USER_AGENT")
	}
	if _, err := collector.ParseDate(opts.startDate); err != nil {
		return options{}, fmt.Errorf("invalid --start-date %q: %w", opts.startDate, err)
	}
	if opts.limit < 0 {
		return options{}, errors.New("--limit must be 0 or greater")
	}
	opts.tickers = uniqueTickers(tickerValues)
	return opts, nil
}

func companiesForRun(ctx context.Context, opts options, httpClient *http.Client) ([]collector.Company, error) {
	if len(opts.tickers) > 0 {
		companies := make([]collector.Company, 0, len(opts.tickers))
		for _, ticker := range opts.tickers {
			companies = append(companies, collector.Company{Ticker: ticker})
		}
		return companies, nil
	}

	secClient := collector.NewSECClient(collector.SECClientConfig{
		UserAgent: opts.userAgent,
		Client:    httpClient,
	})
	return secClient.FetchCompanies(ctx)
}

func splitTickers(value string) []string {
	parts := strings.Split(value, ",")
	tickers := make([]string, 0, len(parts))
	for _, part := range parts {
		ticker := collector.NormalizeTicker(part)
		if ticker == "" {
			continue
		}
		tickers = append(tickers, ticker)
	}
	return tickers
}

func uniqueTickers(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	tickers := make([]string, 0, len(values))
	for _, value := range values {
		ticker := collector.NormalizeTicker(value)
		if ticker == "" {
			continue
		}
		if _, ok := seen[ticker]; ok {
			continue
		}
		seen[ticker] = struct{}{}
		tickers = append(tickers, ticker)
	}
	return tickers
}
