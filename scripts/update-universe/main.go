package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/minjoon/stock-price/internal/collector"
)

type options struct {
	minMarketCap int64
	maxTickers   int
	workers      int
	sleepMS      int
	maxRuntime   time.Duration
	gracefulStop time.Duration
	batchSize    int
	stateFile    string
	shardIndex   int
	shardCount   int
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
	companies = collector.SelectCompanyShard(companies, options.shardIndex, options.shardCount)
	secTickerHash := collector.HashCompanies(companies)
	state, cursor, continuing, err := universeProgressState(options, secTickerHash, len(companies), startedAt)
	if err != nil {
		return err
	}
	partialDir := partialUniverseDir(options.stateFile)
	accumulated, err := loadAccumulatedUniverse(partialDir, continuing)
	if err != nil {
		return err
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
	budget := collector.RuntimeBudget{
		StartedAt:    startedAt,
		MaxRuntime:   options.maxRuntime,
		GracefulStop: options.gracefulStop,
	}
	var runSummary collector.UniverseUpdateSummary
	processedThisRun := 0
	stopReason := ""
	for cursor < len(companies) && processedThisRun < options.batchSize {
		if budget.ShouldStopBeforeNext() {
			stopReason = "max runtime reached"
			break
		}
		company := companies[cursor]
		result := updater.Build(ctx, []collector.Company{company})
		accumulated.merge(result)
		runSummary = mergeUniverseSummaries(runSummary, result.Summary)
		cursor++
		processedThisRun++
		state.CursorIndex = cursor
		state.ProcessedTickers = cursor
		state.LastProcessedTicker = collector.NormalizeTicker(company.Ticker)
		state.Completed = cursor >= len(companies)
		state.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		if err := collector.NewUniverseStore(partialDir).Rewrite(accumulated.result(options.minMarketCap, len(companies), startedAt)); err != nil {
			return err
		}
		if err := collector.WriteProgressState(options.stateFile, state); err != nil {
			return err
		}
	}
	if stopReason == "" && cursor < len(companies) && processedThisRun >= options.batchSize {
		stopReason = "batch size reached"
	}
	state.CursorIndex = cursor
	state.ProcessedTickers = cursor
	state.Completed = cursor >= len(companies)
	state.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := collector.NewUniverseStore(partialDir).Rewrite(accumulated.result(options.minMarketCap, len(companies), startedAt)); err != nil {
		return err
	}
	if err := collector.WriteProgressState(options.stateFile, state); err != nil {
		return err
	}
	finalResult := accumulated.result(options.minMarketCap, len(companies), startedAt)
	if state.Completed {
		if err := validateUniverseUpdateResult(finalResult, options); err != nil {
			return err
		}
		store := collector.NewUniverseStore(options.outputDir)
		if err := store.Rewrite(finalResult); err != nil {
			return err
		}
	}

	elapsed := time.Since(startedAt).Round(time.Millisecond)
	fmt.Printf("SEC Tickers Total: %d\n", len(companies))
	fmt.Printf("Yahoo MarketCap Requests: %d\n", runSummary.YahooMarketCapRequests)
	fmt.Printf("Collectable Tickers: %d\n", finalResult.Summary.CollectableTickers)
	fmt.Printf("Excluded Tickers: %d\n", finalResult.Summary.ExcludedTickers)
	fmt.Printf("Missing MarketCap: %d\n", runSummary.MissingMarketCap)
	fmt.Printf("Below Threshold: %d\n", runSummary.BelowThreshold)
	fmt.Printf("Yahoo Errors: %d\n", runSummary.YahooErrors)
	fmt.Printf("Partial completion: %t\n", !state.Completed)
	if !state.Completed {
		fmt.Printf("Reason: %s\n", stopReason)
	}
	fmt.Printf("Next cursor index: %d\n", state.CursorIndex)
	fmt.Printf("Elapsed Time: %s\n", elapsed)
	return nil
}

func parseOptions(args []string) (options, error) {
	var opts options
	maxRuntimeMinutes := 330
	gracefulStopMinutes := 10

	flags := flag.NewFlagSet("update-universe", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Int64Var(&opts.minMarketCap, "min-market-cap", collector.DefaultMinMarketCap, "minimum Yahoo market cap required for price collection")
	flags.IntVar(&opts.maxTickers, "max-tickers", 0, "maximum number of SEC tickers to evaluate; 0 means all")
	flags.IntVar(&opts.workers, "workers", 4, "number of market cap workers")
	flags.IntVar(&opts.sleepMS, "sleep-ms", 2000, "minimum delay between outbound HTTP requests in milliseconds")
	flags.IntVar(&maxRuntimeMinutes, "max-runtime-minutes", 330, "maximum script runtime in minutes before graceful completion")
	flags.IntVar(&gracefulStopMinutes, "graceful-stop-minutes", 10, "stop starting new tickers when this many runtime minutes remain")
	flags.IntVar(&opts.batchSize, "batch-size", 1000, "maximum number of tickers to process in this run")
	flags.StringVar(&opts.stateFile, "state-file", "data/state/update-universe.state.json", "progress state JSON file")
	flags.IntVar(&opts.shardIndex, "shard-index", 0, "shard index to process")
	flags.IntVar(&opts.shardCount, "shard-count", 1, "number of shards")
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
	opts.maxRuntime = time.Duration(maxRuntimeMinutes) * time.Minute
	opts.gracefulStop = time.Duration(gracefulStopMinutes) * time.Minute
	if opts.maxRuntime <= 0 {
		return options{}, errors.New("--max-runtime-minutes must be greater than 0")
	}
	if opts.gracefulStop < 0 {
		return options{}, errors.New("--graceful-stop-minutes must be 0 or greater")
	}
	if opts.gracefulStop >= opts.maxRuntime {
		return options{}, errors.New("--graceful-stop-minutes must be less than --max-runtime-minutes")
	}
	if opts.batchSize <= 0 {
		return options{}, errors.New("--batch-size must be greater than 0")
	}
	if strings.TrimSpace(opts.stateFile) == "" {
		return options{}, errors.New("--state-file is required")
	}
	if opts.shardCount <= 0 {
		return options{}, errors.New("--shard-count must be greater than 0")
	}
	if opts.shardIndex < 0 || opts.shardIndex >= opts.shardCount {
		return options{}, errors.New("--shard-index must be between 0 and --shard-count - 1")
	}
	if strings.TrimSpace(opts.outputDir) == "" {
		return options{}, errors.New("--output-dir is required")
	}
	return opts, nil
}

func validateUniverseUpdateResult(result collector.UniverseUpdateResult, opts options) error {
	if opts.maxTickers == 0 && result.Summary.SECTickersTotal > 0 && result.Summary.CollectableTickers == 0 {
		return fmt.Errorf(
			"universe update produced zero collectable tickers; refusing to rewrite full collectable universe (secTickers=%d yahooRequests=%d excluded=%d missingMarketCap=%d belowThreshold=%d yahooErrors=%d)",
			result.Summary.SECTickersTotal,
			result.Summary.YahooMarketCapRequests,
			result.Summary.ExcludedTickers,
			result.Summary.MissingMarketCap,
			result.Summary.BelowThreshold,
			result.Summary.YahooErrors,
		)
	}
	return nil
}

func universeProgressState(opts options, secTickerHash string, totalTickers int, startedAt time.Time) (collector.ProgressState, int, bool, error) {
	now := startedAt.UTC().Format(time.RFC3339)
	existing, ok, err := collector.LoadProgressState(opts.stateFile)
	if err != nil {
		return collector.ProgressState{}, 0, false, err
	}
	if ok && !existing.Completed &&
		existing.JobName == "update-universe" &&
		existing.SECTickerHash == secTickerHash &&
		existing.MinMarketCap == opts.minMarketCap &&
		existing.ShardIndex == opts.shardIndex &&
		existing.ShardCount == opts.shardCount {
		if existing.CursorIndex < 0 {
			existing.CursorIndex = 0
		}
		if existing.CursorIndex > totalTickers {
			existing.CursorIndex = totalTickers
		}
		existing.TotalTickers = totalTickers
		existing.UpdatedAt = now
		return existing, existing.CursorIndex, true, nil
	}
	state := collector.ProgressState{
		JobName:       "update-universe",
		RunID:         collector.UTCDateRunID(startedAt),
		MinMarketCap:  opts.minMarketCap,
		SECTickerHash: secTickerHash,
		TotalTickers:  totalTickers,
		CursorIndex:   0,
		Completed:     totalTickers == 0,
		ShardIndex:    opts.shardIndex,
		ShardCount:    opts.shardCount,
		StartedAt:     now,
		UpdatedAt:     now,
	}
	return state, 0, false, nil
}

type accumulatedUniverse struct {
	collectable map[string]collector.CollectableTicker
	excluded    map[string]collector.ExcludedTicker
}

func loadAccumulatedUniverse(dir string, continuing bool) (accumulatedUniverse, error) {
	accumulated := accumulatedUniverse{
		collectable: map[string]collector.CollectableTicker{},
		excluded:    map[string]collector.ExcludedTicker{},
	}
	if !continuing {
		return accumulated, nil
	}
	collectable, err := loadCollectableIfExists(filepath.Join(dir, "collectable_tickers.jsonl"))
	if err != nil {
		return accumulated, err
	}
	for _, item := range collectable {
		accumulated.collectable[item.Ticker] = item
	}
	excluded, err := loadExcludedIfExists(filepath.Join(dir, "excluded_tickers.jsonl"))
	if err != nil {
		return accumulated, err
	}
	for _, item := range excluded {
		accumulated.excluded[item.Ticker] = item
	}
	return accumulated, nil
}

func (a accumulatedUniverse) merge(result collector.UniverseUpdateResult) {
	for _, item := range result.Collectable {
		ticker := collector.NormalizeTicker(item.Ticker)
		if ticker == "" {
			continue
		}
		a.collectable[ticker] = item
		delete(a.excluded, ticker)
	}
	for _, item := range result.Excluded {
		ticker := collector.NormalizeTicker(item.Ticker)
		if ticker == "" {
			continue
		}
		a.excluded[ticker] = item
		delete(a.collectable, ticker)
	}
}

func (a accumulatedUniverse) result(minMarketCap int64, totalTickers int, startedAt time.Time) collector.UniverseUpdateResult {
	collectable := make([]collector.CollectableTicker, 0, len(a.collectable))
	for _, item := range a.collectable {
		collectable = append(collectable, item)
	}
	excluded := make([]collector.ExcludedTicker, 0, len(a.excluded))
	for _, item := range a.excluded {
		excluded = append(excluded, item)
	}
	result := collector.UniverseUpdateResult{
		Collectable: collectable,
		Excluded:    excluded,
		Summary: collector.UniverseUpdateSummary{
			SECTickersTotal:    totalTickers,
			CollectableTickers: len(collectable),
			ExcludedTickers:    len(excluded),
		},
		Meta: collector.CollectableTickersMeta{
			Source:             collector.SourceYahoo,
			SECSource:          collector.DefaultSECCompanyTickersURL,
			MinMarketCap:       minMarketCap,
			TotalSECTickers:    totalTickers,
			CollectableTickers: len(collectable),
			ExcludedTickers:    len(excluded),
			LastUpdatedAt:      startedAt.UTC().Format(time.RFC3339),
		},
	}
	return result
}

func partialUniverseDir(stateFile string) string {
	name := strings.TrimSuffix(filepath.Base(stateFile), ".state.json")
	if name == filepath.Base(stateFile) {
		name = strings.TrimSuffix(name, filepath.Ext(name))
	}
	if name == "" {
		name = "update-universe"
	}
	return filepath.Join(filepath.Dir(stateFile), name)
}

func loadCollectableIfExists(path string) ([]collector.CollectableTicker, error) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return collector.LoadCollectableTickers(path)
}

func loadExcludedIfExists(path string) ([]collector.ExcludedTicker, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var excluded []collector.ExcludedTicker
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var item collector.ExcludedTicker
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			return nil, err
		}
		item.Ticker = collector.NormalizeTicker(item.Ticker)
		if item.Ticker == "" {
			continue
		}
		excluded = append(excluded, item)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return excluded, nil
}

func mergeUniverseSummaries(left collector.UniverseUpdateSummary, right collector.UniverseUpdateSummary) collector.UniverseUpdateSummary {
	left.SECTickersTotal += right.SECTickersTotal
	left.YahooMarketCapRequests += right.YahooMarketCapRequests
	left.CollectableTickers += right.CollectableTickers
	left.ExcludedTickers += right.ExcludedTickers
	left.MissingMarketCap += right.MissingMarketCap
	left.BelowThreshold += right.BelowThreshold
	left.YahooErrors += right.YahooErrors
	return left
}
