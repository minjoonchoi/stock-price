package collector

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	DefaultMinMarketCap = int64(300_000_000)

	ReasonMarketCapBelowThreshold = "market_cap_below_threshold"
	ReasonMarketCapMissing        = "market_cap_missing"
	ReasonMarketCapZero           = "market_cap_zero"
	ReasonYahooSymbolNotFound     = "yahoo_symbol_not_found"
	ReasonYahooRequestFailed      = "yahoo_request_failed"
	ReasonNotCommonStockLike      = "not_common_stock_like"
	ReasonInvalidTicker           = "invalid_ticker"
)

var (
	ErrYahooSymbolNotFound         = errors.New("yahoo symbol not found")
	ErrCollectableUniverseNotFound = errors.New("collectable_tickers.jsonl not found. Run update-universe workflow first.")
)

type CollectableTicker struct {
	Ticker    string `json:"ticker"`
	CIK       string `json:"cik"`
	Title     string `json:"title"`
	MarketCap int64  `json:"marketCap"`
	Currency  string `json:"currency"`
	Source    string `json:"source"`
	CheckedAt string `json:"checkedAt"`
}

type ExcludedTicker struct {
	Ticker       string `json:"ticker"`
	CIK          string `json:"cik"`
	Title        string `json:"title"`
	Reason       string `json:"reason"`
	MarketCap    int64  `json:"marketCap"`
	MinMarketCap int64  `json:"minMarketCap"`
	Source       string `json:"source"`
	CheckedAt    string `json:"checkedAt"`
}

type CollectableTickersMeta struct {
	Source             string `json:"source"`
	SECSource          string `json:"secSource"`
	MinMarketCap       int64  `json:"minMarketCap"`
	TotalSECTickers    int    `json:"totalSecTickers"`
	CollectableTickers int    `json:"collectableTickers"`
	ExcludedTickers    int    `json:"excludedTickers"`
	LastUpdatedAt      string `json:"lastUpdatedAt"`
}

type MarketCapQuote struct {
	Ticker       string
	YahooSymbol  string
	MarketCap    int64
	HasMarketCap bool
	Currency     string
	QuoteType    string
	Source       string
}

type MarketCapProvider interface {
	FetchMarketCap(ctx context.Context, ticker string) (MarketCapQuote, error)
}

type YahooMarketCapProviderConfig struct {
	BaseURL   string
	UserAgent string
	Client    *http.Client
}

type YahooMarketCapProvider struct {
	baseURL   string
	userAgent string
	client    *http.Client
	sleep     func(time.Duration)
}

func NewYahooMarketCapProvider(config YahooMarketCapProviderConfig) *YahooMarketCapProvider {
	baseURL := strings.TrimRight(config.BaseURL, "/")
	if baseURL == "" {
		baseURL = DefaultYahooBaseURL
	}
	client := config.Client
	if client == nil {
		client = http.DefaultClient
	}
	return &YahooMarketCapProvider{
		baseURL:   baseURL,
		userAgent: config.UserAgent,
		client:    client,
		sleep:     time.Sleep,
	}
}

func (p *YahooMarketCapProvider) FetchMarketCap(ctx context.Context, ticker string) (MarketCapQuote, error) {
	normalizedTicker := NormalizeTicker(ticker)
	symbol := YahooSymbol(normalizedTicker)
	quote := MarketCapQuote{
		Ticker:      normalizedTicker,
		YahooSymbol: symbol,
		Source:      SourceYahoo,
	}
	if symbol == "" {
		return quote, ErrYahooSymbolNotFound
	}

	quoteResult, err := p.fetchQuote(ctx, ticker, symbol, quote)
	if err == nil {
		return quoteResult, nil
	}
	summaryResult, summaryErr := p.fetchQuoteSummary(ctx, ticker, symbol, quote)
	if summaryErr == nil {
		return summaryResult, nil
	}
	return quote, err
}

func (p *YahooMarketCapProvider) fetchQuote(ctx context.Context, ticker string, symbol string, quote MarketCapQuote) (MarketCapQuote, error) {
	requestURL, err := url.Parse(p.baseURL + "/v7/finance/quote")
	if err != nil {
		return quote, err
	}
	params := requestURL.Query()
	params.Set("symbols", symbol)
	requestURL.RawQuery = params.Encode()

	var lastStatus string
	for attempt := 0; attempt < 3; attempt++ {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
		if err != nil {
			return quote, err
		}
		if p.userAgent != "" {
			request.Header.Set("User-Agent", p.userAgent)
		}
		request.Header.Set("Accept", "application/json,text/plain,*/*")
		request.Header.Set("Accept-Encoding", "identity")

		response, err := p.client.Do(request)
		if err != nil {
			return quote, err
		}
		if response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
			lastStatus = response.Status
			_ = response.Body.Close()
			if attempt < 2 {
				p.sleep(time.Duration(1<<attempt) * time.Second)
				continue
			}
			return quote, fmt.Errorf("Yahoo quote request for %s failed: %s", ticker, lastStatus)
		}
		defer response.Body.Close()

		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return quote, fmt.Errorf("Yahoo quote request for %s failed: %s", ticker, response.Status)
		}

		var payload yahooQuoteResponse
		if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
			return quote, err
		}
		if payload.QuoteResponse.Error != nil {
			return quote, fmt.Errorf("Yahoo quote request for %s failed: %s", ticker, payload.QuoteResponse.Error.Description)
		}
		if len(payload.QuoteResponse.Result) == 0 {
			return quote, ErrYahooSymbolNotFound
		}

		item := payload.QuoteResponse.Result[0]
		if item.Symbol != "" {
			quote.YahooSymbol = item.Symbol
		}
		if item.MarketCap != nil {
			quote.MarketCap = *item.MarketCap
			quote.HasMarketCap = true
		}
		quote.Currency = item.Currency
		quote.QuoteType = strings.ToUpper(strings.TrimSpace(item.QuoteType))
		return quote, nil
	}

	return quote, fmt.Errorf("Yahoo quote request for %s failed: %s", ticker, lastStatus)
}

func (p *YahooMarketCapProvider) fetchQuoteSummary(ctx context.Context, ticker string, symbol string, quote MarketCapQuote) (MarketCapQuote, error) {
	requestURL, err := url.Parse(p.baseURL + "/v10/finance/quoteSummary/" + url.PathEscape(symbol))
	if err != nil {
		return quote, err
	}
	params := requestURL.Query()
	params.Set("modules", "price")
	requestURL.RawQuery = params.Encode()

	var lastStatus string
	for attempt := 0; attempt < 3; attempt++ {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
		if err != nil {
			return quote, err
		}
		if p.userAgent != "" {
			request.Header.Set("User-Agent", p.userAgent)
		}
		request.Header.Set("Accept", "application/json,text/plain,*/*")
		request.Header.Set("Accept-Encoding", "identity")

		response, err := p.client.Do(request)
		if err != nil {
			return quote, err
		}
		if response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
			lastStatus = response.Status
			_ = response.Body.Close()
			if attempt < 2 {
				p.sleep(time.Duration(1<<attempt) * time.Second)
				continue
			}
			return quote, fmt.Errorf("Yahoo quoteSummary request for %s failed: %s", ticker, lastStatus)
		}
		defer response.Body.Close()

		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return quote, fmt.Errorf("Yahoo quoteSummary request for %s failed: %s", ticker, response.Status)
		}

		var payload yahooQuoteSummaryResponse
		if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
			return quote, err
		}
		if payload.QuoteSummary.Error != nil {
			return quote, fmt.Errorf("Yahoo quoteSummary request for %s failed: %s", ticker, payload.QuoteSummary.Error.Description)
		}
		if len(payload.QuoteSummary.Result) == 0 {
			return quote, ErrYahooSymbolNotFound
		}

		item := payload.QuoteSummary.Result[0].Price
		if item.Symbol != "" {
			quote.YahooSymbol = item.Symbol
		}
		if item.MarketCap != nil {
			quote.MarketCap = item.MarketCap.Raw
			quote.HasMarketCap = true
		}
		quote.Currency = item.Currency
		quote.QuoteType = strings.ToUpper(strings.TrimSpace(item.QuoteType))
		return quote, nil
	}

	return quote, fmt.Errorf("Yahoo quoteSummary request for %s failed: %s", ticker, lastStatus)
}

type yahooQuoteResponse struct {
	QuoteResponse struct {
		Result []struct {
			Symbol    string `json:"symbol"`
			MarketCap *int64 `json:"marketCap"`
			Currency  string `json:"currency"`
			QuoteType string `json:"quoteType"`
		} `json:"result"`
		Error *struct {
			Code        string `json:"code"`
			Description string `json:"description"`
		} `json:"error"`
	} `json:"quoteResponse"`
}

type yahooQuoteSummaryResponse struct {
	QuoteSummary struct {
		Result []struct {
			Price struct {
				Symbol    string `json:"symbol"`
				MarketCap *struct {
					Raw int64 `json:"raw"`
				} `json:"marketCap"`
				Currency  string `json:"currency"`
				QuoteType string `json:"quoteType"`
			} `json:"price"`
		} `json:"result"`
		Error *struct {
			Code        string `json:"code"`
			Description string `json:"description"`
		} `json:"error"`
	} `json:"quoteSummary"`
}

type UniverseUpdaterConfig struct {
	MarketCapProvider MarketCapProvider
	MinMarketCap      int64
	Workers           int
	Clock             func() time.Time
	LogWriter         io.Writer
}

type UniverseUpdater struct {
	provider     MarketCapProvider
	minMarketCap int64
	workers      int
	clock        func() time.Time
	logger       *log.Logger
}

type UniverseUpdateSummary struct {
	SECTickersTotal        int
	YahooMarketCapRequests int
	CollectableTickers     int
	ExcludedTickers        int
	MissingMarketCap       int
	BelowThreshold         int
	YahooErrors            int
}

type UniverseUpdateResult struct {
	Collectable []CollectableTicker
	Excluded    []ExcludedTicker
	Meta        CollectableTickersMeta
	Summary     UniverseUpdateSummary
}

func NewUniverseUpdater(config UniverseUpdaterConfig) *UniverseUpdater {
	minMarketCap := config.MinMarketCap
	if minMarketCap <= 0 {
		minMarketCap = DefaultMinMarketCap
	}
	workers := config.Workers
	if workers <= 0 {
		workers = 4
	}
	clock := config.Clock
	if clock == nil {
		clock = time.Now
	}
	logWriter := config.LogWriter
	if logWriter == nil {
		logWriter = os.Stderr
	}
	return &UniverseUpdater{
		provider:     config.MarketCapProvider,
		minMarketCap: minMarketCap,
		workers:      workers,
		clock:        clock,
		logger:       log.New(logWriter, "", log.LstdFlags),
	}
}

func (u *UniverseUpdater) Build(ctx context.Context, companies []Company) UniverseUpdateResult {
	checkedAt := u.clock().UTC().Format(time.RFC3339)
	jobs := make(chan Company)
	results := make(chan universeDecision)

	var wg sync.WaitGroup
	workers := u.workers
	if workers > len(companies) && len(companies) > 0 {
		workers = len(companies)
	}
	if workers <= 0 {
		workers = 1
	}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for company := range jobs {
				results <- u.evaluateCompany(ctx, company, checkedAt)
			}
		}()
	}

	go func() {
		for _, company := range companies {
			jobs <- company
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()

	result := UniverseUpdateResult{
		Summary: UniverseUpdateSummary{
			SECTickersTotal: len(companies),
		},
	}
	for decision := range results {
		u.logDecision(decision)
		if decision.requested {
			result.Summary.YahooMarketCapRequests++
		}
		if decision.collectable != nil {
			result.Collectable = append(result.Collectable, *decision.collectable)
			continue
		}
		if decision.excluded != nil {
			result.Excluded = append(result.Excluded, *decision.excluded)
			switch decision.excluded.Reason {
			case ReasonMarketCapMissing:
				result.Summary.MissingMarketCap++
			case ReasonMarketCapBelowThreshold:
				result.Summary.BelowThreshold++
			case ReasonYahooRequestFailed, ReasonYahooSymbolNotFound:
				result.Summary.YahooErrors++
			}
		}
	}

	sortUniverse(result.Collectable, result.Excluded)
	result.Summary.CollectableTickers = len(result.Collectable)
	result.Summary.ExcludedTickers = len(result.Excluded)
	result.Meta = CollectableTickersMeta{
		Source:             SourceYahoo,
		SECSource:          DefaultSECCompanyTickersURL,
		MinMarketCap:       u.minMarketCap,
		TotalSECTickers:    result.Summary.SECTickersTotal,
		CollectableTickers: result.Summary.CollectableTickers,
		ExcludedTickers:    result.Summary.ExcludedTickers,
		LastUpdatedAt:      checkedAt,
	}
	return result
}

func (u *UniverseUpdater) logDecision(decision universeDecision) {
	if u.logger == nil {
		return
	}
	if decision.collectable != nil {
		item := decision.collectable
		u.logger.Printf("%s universe collectable: marketCap=%d currency=%s source=%s", item.Ticker, item.MarketCap, item.Currency, item.Source)
		return
	}
	if decision.excluded != nil {
		item := decision.excluded
		u.logger.Printf("%s universe excluded: reason=%s marketCap=%d source=%s", item.Ticker, item.Reason, item.MarketCap, item.Source)
	}
}

type universeDecision struct {
	collectable *CollectableTicker
	excluded    *ExcludedTicker
	requested   bool
}

func (u *UniverseUpdater) evaluateCompany(ctx context.Context, company Company, checkedAt string) universeDecision {
	company.Ticker = NormalizeTicker(company.Ticker)
	excluded := ExcludedTicker{
		Ticker:       company.Ticker,
		CIK:          formatCIK(company.CIK),
		Title:        company.Title,
		MinMarketCap: u.minMarketCap,
		Source:       SourceYahoo,
		CheckedAt:    checkedAt,
	}

	symbol := YahooSymbol(company.Ticker)
	if symbol == "" || !isValidYahooSymbol(symbol) {
		excluded.Reason = ReasonInvalidTicker
		return universeDecision{excluded: &excluded}
	}
	if u.provider == nil {
		excluded.Reason = ReasonYahooRequestFailed
		return universeDecision{excluded: &excluded}
	}

	quote, err := u.provider.FetchMarketCap(ctx, company.Ticker)
	if err != nil {
		if errors.Is(err, ErrYahooSymbolNotFound) {
			excluded.Reason = ReasonYahooSymbolNotFound
		} else {
			excluded.Reason = ReasonYahooRequestFailed
		}
		return universeDecision{excluded: &excluded, requested: true}
	}
	excluded.MarketCap = quote.MarketCap
	if quote.Source != "" {
		excluded.Source = quote.Source
	}

	if !isCommonStockLikeQuote(quote) {
		excluded.Reason = ReasonNotCommonStockLike
		return universeDecision{excluded: &excluded, requested: true}
	}
	if !quote.HasMarketCap {
		excluded.Reason = ReasonMarketCapMissing
		return universeDecision{excluded: &excluded, requested: true}
	}
	if quote.MarketCap == 0 {
		excluded.Reason = ReasonMarketCapZero
		return universeDecision{excluded: &excluded, requested: true}
	}
	if quote.MarketCap < u.minMarketCap {
		excluded.Reason = ReasonMarketCapBelowThreshold
		return universeDecision{excluded: &excluded, requested: true}
	}

	source := quote.Source
	if source == "" {
		source = SourceYahoo
	}
	return universeDecision{
		collectable: &CollectableTicker{
			Ticker:    company.Ticker,
			CIK:       formatCIK(company.CIK),
			Title:     company.Title,
			MarketCap: quote.MarketCap,
			Currency:  quote.Currency,
			Source:    source,
			CheckedAt: checkedAt,
		},
		requested: true,
	}
}

func isValidYahooSymbol(symbol string) bool {
	for _, char := range symbol {
		if char >= 'A' && char <= 'Z' {
			continue
		}
		if char >= '0' && char <= '9' {
			continue
		}
		if char == '-' {
			continue
		}
		return false
	}
	return symbol != ""
}

func isCommonStockLikeQuote(quote MarketCapQuote) bool {
	quoteType := strings.ToUpper(strings.TrimSpace(quote.QuoteType))
	return quoteType == "" || quoteType == "EQUITY"
}

func formatCIK(cik int) string {
	if cik < 0 {
		cik = 0
	}
	return fmt.Sprintf("%010d", cik)
}

type UniverseStore struct {
	outputDir string
}

func NewUniverseStore(outputDir string) *UniverseStore {
	if strings.TrimSpace(outputDir) == "" {
		outputDir = filepath.Join("data", "universe")
	}
	return &UniverseStore{outputDir: outputDir}
}

func (s *UniverseStore) Rewrite(result UniverseUpdateResult) error {
	if err := os.MkdirAll(s.outputDir, 0o755); err != nil {
		return err
	}
	collectable := append([]CollectableTicker(nil), result.Collectable...)
	excluded := append([]ExcludedTicker(nil), result.Excluded...)
	sortUniverse(collectable, excluded)

	if err := writeJSONL(filepath.Join(s.outputDir, "collectable_tickers.jsonl"), collectable); err != nil {
		return err
	}
	if err := writeJSONL(filepath.Join(s.outputDir, "excluded_tickers.jsonl"), excluded); err != nil {
		return err
	}
	return writeJSONFile(filepath.Join(s.outputDir, "collectable_tickers.meta.json"), result.Meta)
}

func (s *UniverseStore) LoadMeta() (CollectableTickersMeta, error) {
	file, err := os.Open(filepath.Join(s.outputDir, "collectable_tickers.meta.json"))
	if err != nil {
		return CollectableTickersMeta{}, err
	}
	defer file.Close()

	var meta CollectableTickersMeta
	if err := json.NewDecoder(file).Decode(&meta); err != nil {
		return CollectableTickersMeta{}, err
	}
	return meta, nil
}

func LoadCollectableTickers(path string) ([]CollectableTicker, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrCollectableUniverseNotFound
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	var tickers []CollectableTicker
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var ticker CollectableTicker
		if err := json.Unmarshal([]byte(line), &ticker); err != nil {
			return nil, fmt.Errorf("decode universe %s:%d: %w", path, lineNumber, err)
		}
		ticker.Ticker = NormalizeTicker(ticker.Ticker)
		if ticker.Ticker == "" {
			continue
		}
		tickers = append(tickers, ticker)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return tickers, nil
}

type UniverseFilterResult struct {
	Companies                []Company
	SECTickersTotal          int
	UniverseTickersTotal     int
	FinalTargetTickers       int
	ExcludedByUniverseFilter int
}

func FilterCompaniesByUniverse(companies []Company, universe []CollectableTicker) UniverseFilterResult {
	universeSet := make(map[string]struct{}, len(universe))
	for _, ticker := range universe {
		normalizedTicker := NormalizeTicker(ticker.Ticker)
		if normalizedTicker == "" {
			continue
		}
		universeSet[normalizedTicker] = struct{}{}
	}

	filtered := make([]Company, 0, len(companies))
	for _, company := range companies {
		company.Ticker = NormalizeTicker(company.Ticker)
		if company.Ticker == "" {
			continue
		}
		if _, ok := universeSet[company.Ticker]; !ok {
			continue
		}
		filtered = append(filtered, company)
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		return filtered[i].Ticker < filtered[j].Ticker
	})

	return UniverseFilterResult{
		Companies:                filtered,
		SECTickersTotal:          len(companies),
		UniverseTickersTotal:     len(universeSet),
		FinalTargetTickers:       len(filtered),
		ExcludedByUniverseFilter: len(companies) - len(filtered),
	}
}

func sortUniverse(collectable []CollectableTicker, excluded []ExcludedTicker) {
	sort.SliceStable(collectable, func(i, j int) bool {
		if collectable[i].MarketCap == collectable[j].MarketCap {
			return collectable[i].Ticker < collectable[j].Ticker
		}
		return collectable[i].MarketCap > collectable[j].MarketCap
	})
	sort.SliceStable(excluded, func(i, j int) bool {
		return excluded[i].Ticker < excluded[j].Ticker
	})
}

func writeJSONFile(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tempPath := path + ".tmp"
	if err := os.WriteFile(tempPath, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	return nil
}
