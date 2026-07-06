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
	startDate                      string
	dataDir                        string
	userAgent                      string
	universeFile                   string
	allowAllSECTickers             bool
	tickers                        []string
	limit                          int
	workers                        int
	timeout                        time.Duration
	requestDelay                   time.Duration
	maxRuntime                     time.Duration
	gracefulStop                   time.Duration
	batchSize                      int
	stateFile                      string
	shardIndex                     int
	shardCount                     int
	forceBackfill                  bool
	repairMeta                     bool
	forceValidateAdjusted          bool
	fullValidationDays             int
	disablePriceDiscontinuityCheck bool
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

	var startDate time.Time
	if options.startDate != "" {
		parsedStartDate, err := collector.ParseDate(options.startDate)
		if err != nil {
			return fmt.Errorf("parse start date: %w", err)
		}
		startDate = parsedStartDate
	}

	httpClient := &http.Client{
		Timeout: options.timeout,
		Transport: &collector.RateLimitedTransport{
			Delay: options.requestDelay,
		},
	}
	store := collector.NewFileStore(options.dataDir)
	provider := collector.NewYahooProvider(collector.YahooProviderConfig{
		UserAgent: options.userAgent,
		Client:    httpClient,
	})

	selection, err := companiesForRun(ctx, options, httpClient, store)
	if err != nil {
		return err
	}
	companies := selection.companies
	if options.limit > 0 && len(companies) > options.limit {
		companies = companies[:options.limit]
		selection.filter.FinalTargetTickers = len(companies)
	}
	companies = collector.SelectCompanyShard(companies, options.shardIndex, options.shardCount)
	selection.filter.FinalTargetTickers = len(companies)

	runner := collector.NewRunner(collector.RunnerConfig{
		Store:                          store,
		Provider:                       provider,
		StartDate:                      startDate,
		ForceBackfill:                  options.forceBackfill,
		RepairMeta:                     options.repairMeta,
		ForceValidateAdjusted:          options.forceValidateAdjusted,
		FullValidationDays:             options.fullValidationDays,
		DisablePriceDiscontinuityCheck: options.disablePriceDiscontinuityCheck,
	})
	state, cursor, err := collectProgressState(options, selection, len(companies), startedAt)
	if err != nil {
		return err
	}
	budget := collector.RuntimeBudget{
		StartedAt:    startedAt,
		MaxRuntime:   options.maxRuntime,
		GracefulStop: options.gracefulStop,
	}
	var summary collector.Summary
	processedThisRun := 0
	stopReason := ""
	for cursor < len(companies) && processedThisRun < options.batchSize {
		if budget.ShouldStopBeforeNext() {
			stopReason = "max runtime reached"
			break
		}
		ticker := companies[cursor].Ticker
		tickerSummary, err := runner.CollectTickers(ctx, []collector.Company{companies[cursor]})
		if err != nil {
			return err
		}
		summary = mergeSummaries(summary, tickerSummary)
		cursor++
		processedThisRun++
		state.CursorIndex = cursor
		state.ProcessedTargets = cursor
		state.LastProcessedTicker = ticker
		state.Completed = cursor >= len(companies)
		state.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		if err := collector.WriteProgressState(options.stateFile, state); err != nil {
			return err
		}
	}
	if stopReason == "" && cursor < len(companies) && processedThisRun >= options.batchSize {
		stopReason = "batch size reached"
	}
	state.CursorIndex = cursor
	state.ProcessedTargets = cursor
	state.Completed = cursor >= len(companies)
	state.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := collector.WriteProgressState(options.stateFile, state); err != nil {
		return err
	}

	fmt.Printf("SEC Tickers Total: %d\n", selection.filter.SECTickersTotal)
	fmt.Printf("Universe Tickers Total: %d\n", selection.filter.UniverseTickersTotal)
	fmt.Printf("Final Target Tickers: %d\n", selection.filter.FinalTargetTickers)
	fmt.Printf("Excluded By Universe Filter: %d\n", selection.filter.ExcludedByUniverseFilter)
	fmt.Printf("processed=%d skipped=%d appended=%d failed=%d\n", summary.Processed, summary.Skipped, summary.Appended, summary.Failed)
	fmt.Printf("Tickers Backfilled: %d\n", summary.Backfilled)
	fmt.Printf("Tickers Incremental Updated: %d\n", summary.IncrementalUpdated)
	fmt.Printf("Tickers Full Rewritten: %d\n", summary.FullRewritten)
	fmt.Printf("Tickers Split Detected: %d\n", summary.SplitDetected)
	fmt.Printf("Tickers Corporate Actions Changed: %d\n", summary.CorporateActionsChanged)
	fmt.Printf("Tickers Discontinuity Detected: %d\n", summary.DiscontinuityDetected)
	fmt.Printf("Tickers Adjusted Validated: %d\n", summary.AdjustedValidated)
	fmt.Printf("Rows Adjusted Recalculated: %d\n", summary.RowsAdjustedRecalculated)
	fmt.Printf("Actions Written: %d\n", summary.ActionsWritten)
	fmt.Printf("Partial completion: %t\n", !state.Completed)
	if !state.Completed {
		fmt.Printf("Reason: %s\n", stopReason)
	}
	fmt.Printf("Next cursor index: %d\n", state.CursorIndex)
	return nil
}

func parseOptions(args []string) (options, error) {
	var opts options
	var tickerValues []string
	sleepMS := -1
	maxRuntimeMinutes := 330
	gracefulStopMinutes := 10

	flags := flag.NewFlagSet("collect-prices", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&opts.startDate, "start-date", os.Getenv("STOCK_PRICE_START_DATE"), "initial collection date in YYYY-MM-DD format; empty means provider max history for new tickers")
	flags.StringVar(&opts.dataDir, "data-dir", "data/prices", "directory where per-ticker JSONL and meta files are stored")
	flags.StringVar(&opts.userAgent, "user-agent", os.Getenv("SEC_USER_AGENT"), "User-Agent header for SEC and Yahoo requests")
	flags.StringVar(&opts.userAgent, "sec-user-agent", os.Getenv("SEC_USER_AGENT"), "User-Agent header for SEC and Yahoo requests")
	flags.StringVar(&opts.universeFile, "universe-file", "data/universe/collectable_tickers.jsonl", "JSONL file containing market-cap filtered collectable tickers")
	flags.BoolVar(&opts.allowAllSECTickers, "allow-all-sec-tickers", false, "allow collecting all SEC tickers when the universe file is missing or intentionally bypassed")
	flags.IntVar(&opts.limit, "limit", 0, "maximum number of tickers to process; 0 means all")
	flags.IntVar(&opts.limit, "max-tickers", 0, "maximum number of tickers to process; 0 means all")
	flags.IntVar(&opts.workers, "workers", 4, "number of ticker workers")
	flags.DurationVar(&opts.timeout, "timeout", 30*time.Second, "HTTP request timeout")
	flags.DurationVar(&opts.requestDelay, "request-delay", defaultRequestDelay(), "minimum delay between outbound HTTP requests")
	flags.IntVar(&sleepMS, "sleep-ms", -1, "minimum delay between outbound HTTP requests in milliseconds")
	flags.IntVar(&maxRuntimeMinutes, "max-runtime-minutes", 330, "maximum script runtime in minutes before graceful completion")
	flags.IntVar(&gracefulStopMinutes, "graceful-stop-minutes", 10, "stop starting new tickers when this many runtime minutes remain")
	flags.IntVar(&opts.batchSize, "batch-size", 500, "maximum number of tickers to process in this run")
	flags.StringVar(&opts.stateFile, "state-file", "data/state/collect-prices.state.json", "progress state JSON file")
	flags.IntVar(&opts.shardIndex, "shard-index", 0, "shard index to process")
	flags.IntVar(&opts.shardCount, "shard-count", 1, "number of shards")
	flags.BoolVar(&opts.forceBackfill, "force-backfill", false, "force full-history merge for every ticker")
	flags.BoolVar(&opts.repairMeta, "repair-meta", false, "rebuild per-ticker meta from local JSONL without fetching price history")
	flags.BoolVar(&opts.forceValidateAdjusted, "force-validate-adjusted", false, "force full-history adjusted price validation and rewrite for every ticker")
	flags.IntVar(&opts.fullValidationDays, "full-validation-days", 7, "days between full adjusted price validations")
	flags.BoolVar(&opts.disablePriceDiscontinuityCheck, "disable-price-discontinuity-check", false, "disable split-like raw price discontinuity detection")
	flags.Func("ticker", "ticker or comma-separated tickers to collect instead of fetching the SEC list; can be repeated", func(value string) error {
		tickerValues = append(tickerValues, splitTickers(value)...)
		return nil
	})

	if err := flags.Parse(args); err != nil {
		return options{}, err
	}
	opts.userAgent = strings.TrimSpace(opts.userAgent)
	if opts.userAgent == "" && !opts.repairMeta {
		return options{}, errors.New("missing --user-agent or SEC_USER_AGENT")
	}
	if opts.startDate != "" {
		if _, err := collector.ParseDate(opts.startDate); err != nil {
			return options{}, fmt.Errorf("invalid --start-date %q: %w", opts.startDate, err)
		}
	}
	if opts.limit < 0 {
		return options{}, errors.New("--limit must be 0 or greater")
	}
	if opts.workers <= 0 {
		return options{}, errors.New("--workers must be greater than 0")
	}
	if sleepMS >= 0 {
		opts.requestDelay = time.Duration(sleepMS) * time.Millisecond
	}
	opts.maxRuntime = time.Duration(maxRuntimeMinutes) * time.Minute
	opts.gracefulStop = time.Duration(gracefulStopMinutes) * time.Minute
	if opts.requestDelay < 0 {
		return options{}, errors.New("--request-delay must be 0 or greater")
	}
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
	if opts.fullValidationDays <= 0 {
		return options{}, errors.New("--full-validation-days must be greater than 0")
	}
	opts.tickers = uniqueTickers(tickerValues)
	return opts, nil
}

func defaultRequestDelay() time.Duration {
	value := strings.TrimSpace(os.Getenv("PRICE_REQUEST_DELAY"))
	if value == "" {
		return 2 * time.Second
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration < 0 {
		return 2 * time.Second
	}
	return duration
}

type companySelection struct {
	companies     []collector.Company
	filter        collector.UniverseFilterResult
	secTickerHash string
	universeHash  string
}

func companiesForRun(ctx context.Context, opts options, httpClient *http.Client, store *collector.FileStore) (companySelection, error) {
	if opts.repairMeta {
		tickers, err := store.ListTickers()
		if err != nil {
			return companySelection{}, err
		}
		companies := make([]collector.Company, 0, len(tickers))
		for _, ticker := range tickers {
			companies = append(companies, collector.Company{Ticker: ticker})
		}
		companies = collector.SortCompaniesByTicker(companies)
		return companySelection{
			companies:     companies,
			secTickerHash: collector.HashCompanies(companies),
			filter: collector.UniverseFilterResult{
				Companies:          companies,
				SECTickersTotal:    len(companies),
				FinalTargetTickers: len(companies),
			},
		}, nil
	}

	secClient := collector.NewSECClient(collector.SECClientConfig{
		UserAgent: opts.userAgent,
		Client:    httpClient,
	})
	secCompanies, err := secClient.FetchCompanies(ctx)
	if err != nil {
		return companySelection{}, err
	}
	secTickerHash := collector.HashCompanies(secCompanies)

	filter := collector.UniverseFilterResult{
		Companies:            secCompanies,
		SECTickersTotal:      len(secCompanies),
		UniverseTickersTotal: len(secCompanies),
		FinalTargetTickers:   len(secCompanies),
	}
	universeHash := ""
	if !opts.allowAllSECTickers {
		universe, err := collector.LoadCollectableTickers(opts.universeFile)
		if err != nil {
			return companySelection{}, err
		}
		universeHash = collector.HashCollectableTickers(universe)
		filter = collector.FilterCompaniesByUniverse(secCompanies, universe)
	}

	companies := filter.Companies
	if len(opts.tickers) > 0 {
		companies = subsetCompanies(companies, opts.tickers)
		filter.Companies = companies
		filter.FinalTargetTickers = len(companies)
	}
	return companySelection{companies: collector.SortCompaniesByTicker(companies), filter: filter, secTickerHash: secTickerHash, universeHash: universeHash}, nil
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

func subsetCompanies(companies []collector.Company, tickers []string) []collector.Company {
	requested := make(map[string]struct{}, len(tickers))
	for _, ticker := range tickers {
		requested[collector.NormalizeTicker(ticker)] = struct{}{}
	}
	filtered := make([]collector.Company, 0, len(companies))
	for _, company := range companies {
		if _, ok := requested[collector.NormalizeTicker(company.Ticker)]; ok {
			filtered = append(filtered, company)
		}
	}
	return filtered
}

func collectProgressState(opts options, selection companySelection, totalTargets int, startedAt time.Time) (collector.ProgressState, int, error) {
	now := startedAt.UTC().Format(time.RFC3339)
	existing, ok, err := collector.LoadProgressState(opts.stateFile)
	if err != nil {
		return collector.ProgressState{}, 0, err
	}
	if ok && !existing.Completed &&
		existing.JobName == "collect-prices" &&
		existing.SECTickerHash == selection.secTickerHash &&
		existing.UniverseHash == selection.universeHash &&
		existing.ShardIndex == opts.shardIndex &&
		existing.ShardCount == opts.shardCount {
		if existing.CursorIndex < 0 {
			existing.CursorIndex = 0
		}
		if existing.CursorIndex > totalTargets {
			existing.CursorIndex = totalTargets
		}
		existing.TotalTargets = totalTargets
		existing.UpdatedAt = now
		return existing, existing.CursorIndex, nil
	}
	state := collector.ProgressState{
		JobName:       "collect-prices",
		RunID:         collector.UTCDateRunID(startedAt),
		SECTickerHash: selection.secTickerHash,
		UniverseHash:  selection.universeHash,
		TotalTargets:  totalTargets,
		CursorIndex:   0,
		Completed:     totalTargets == 0,
		ShardIndex:    opts.shardIndex,
		ShardCount:    opts.shardCount,
		StartedAt:     now,
		UpdatedAt:     now,
	}
	return state, 0, nil
}

func mergeSummaries(left collector.Summary, right collector.Summary) collector.Summary {
	left.Processed += right.Processed
	left.Skipped += right.Skipped
	left.Appended += right.Appended
	left.Failed += right.Failed
	left.Backfilled += right.Backfilled
	left.IncrementalUpdated += right.IncrementalUpdated
	left.FullRewritten += right.FullRewritten
	left.SplitDetected += right.SplitDetected
	left.CorporateActionsChanged += right.CorporateActionsChanged
	left.DiscontinuityDetected += right.DiscontinuityDetected
	left.AdjustedValidated += right.AdjustedValidated
	left.RowsAdjustedRecalculated += right.RowsAdjustedRecalculated
	left.ActionsWritten += right.ActionsWritten
	return left
}
