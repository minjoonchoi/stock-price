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
	minMarketCap int64
	maxTickers   int
	workers      int
	sleepMS      int
	secUserAgent string
	outputDir    string
	timeout      time.Duration
}

func main() {
	ctx := context.Background()
	if err := run(ctx, os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, args []string) error {
	startedAt := time.Now()
	options, err := parseOptions(args)
	if err != nil {
		return err
	}

	httpClient := &http.Client{
		Timeout: options.timeout,
		Transport: &collector.RateLimitedTransport{
			Delay: time.Duration(options.sleepMS) * time.Millisecond,
		},
	}
	secClient := collector.NewSECClient(collector.SECClientConfig{
		UserAgent: options.secUserAgent,
		Client:    httpClient,
	})
	companies, err := secClient.FetchCompanies(ctx)
	if err != nil {
		return err
	}
	if options.maxTickers > 0 && len(companies) > options.maxTickers {
		companies = companies[:options.maxTickers]
	}

	marketCapProvider := collector.NewYahooMarketCapProvider(collector.YahooMarketCapProviderConfig{
		UserAgent: options.secUserAgent,
		Client:    httpClient,
	})
	updater := collector.NewUniverseUpdater(collector.UniverseUpdaterConfig{
		MarketCapProvider: marketCapProvider,
		MinMarketCap:      options.minMarketCap,
		Workers:           options.workers,
	})
	result := updater.Build(ctx, companies)

	store := collector.NewUniverseStore(options.outputDir)
	if err := store.Rewrite(result); err != nil {
		return err
	}

	elapsed := time.Since(startedAt).Round(time.Millisecond)
	fmt.Printf("SEC Tickers Total: %d\n", result.Summary.SECTickersTotal)
	fmt.Printf("Yahoo MarketCap Requests: %d\n", result.Summary.YahooMarketCapRequests)
	fmt.Printf("Collectable Tickers: %d\n", result.Summary.CollectableTickers)
	fmt.Printf("Excluded Tickers: %d\n", result.Summary.ExcludedTickers)
	fmt.Printf("Missing MarketCap: %d\n", result.Summary.MissingMarketCap)
	fmt.Printf("Below Threshold: %d\n", result.Summary.BelowThreshold)
	fmt.Printf("Yahoo Errors: %d\n", result.Summary.YahooErrors)
	fmt.Printf("Elapsed Time: %s\n", elapsed)
	return nil
}

func parseOptions(args []string) (options, error) {
	var opts options

	flags := flag.NewFlagSet("update-universe", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Int64Var(&opts.minMarketCap, "min-market-cap", collector.DefaultMinMarketCap, "minimum Yahoo market cap required for price collection")
	flags.IntVar(&opts.maxTickers, "max-tickers", 0, "maximum number of SEC tickers to evaluate; 0 means all")
	flags.IntVar(&opts.workers, "workers", 4, "number of market cap workers")
	flags.IntVar(&opts.sleepMS, "sleep-ms", 150, "minimum delay between outbound HTTP requests in milliseconds")
	flags.StringVar(&opts.secUserAgent, "sec-user-agent", os.Getenv("SEC_USER_AGENT"), "User-Agent header for SEC and Yahoo requests")
	flags.StringVar(&opts.outputDir, "output-dir", "data/universe", "directory where universe JSONL and meta files are stored")
	flags.DurationVar(&opts.timeout, "timeout", 30*time.Second, "HTTP request timeout")

	if err := flags.Parse(args); err != nil {
		return options{}, err
	}
	opts.secUserAgent = strings.TrimSpace(opts.secUserAgent)
	if opts.secUserAgent == "" {
		return options{}, errors.New("missing --sec-user-agent or SEC_USER_AGENT")
	}
	if opts.minMarketCap < 0 {
		return options{}, errors.New("--min-market-cap must be 0 or greater")
	}
	if opts.maxTickers < 0 {
		return options{}, errors.New("--max-tickers must be 0 or greater")
	}
	if opts.workers <= 0 {
		return options{}, errors.New("--workers must be greater than 0")
	}
	if opts.sleepMS < 0 {
		return options{}, errors.New("--sleep-ms must be 0 or greater")
	}
	if strings.TrimSpace(opts.outputDir) == "" {
		return options{}, errors.New("--output-dir is required")
	}
	return opts, nil
}
