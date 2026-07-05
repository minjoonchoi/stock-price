package collector

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestYahooMarketCapProviderFetchesQuoteAndConvertsYahooSymbol(t *testing.T) {
	var gotPath string
	var gotQuery string
	var gotUserAgent string
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotUserAgent = r.Header.Get("User-Agent")
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body: io.NopCloser(bytes.NewBufferString(`{
				"quoteResponse":{
					"result":[{
						"symbol":"BRK-B",
						"marketCap":1024000000000,
						"currency":"USD",
						"quoteType":"EQUITY"
					}],
					"error":null
				}
			}`)),
			Request: r,
		}, nil
	})}

	provider := NewYahooMarketCapProvider(YahooMarketCapProviderConfig{
		BaseURL:   "https://query1.finance.yahoo.com",
		UserAgent: "github-stock-collector test@example.com",
		Client:    httpClient,
	})

	quote, err := provider.FetchMarketCap(context.Background(), "brk.b")
	if err != nil {
		t.Fatalf("FetchMarketCap() error = %v", err)
	}

	if gotPath != "/v7/finance/quote" {
		t.Fatalf("path = %q", gotPath)
	}
	assertQueryContains(t, gotQuery, "symbols=BRK-B")
	if gotUserAgent != "github-stock-collector test@example.com" {
		t.Fatalf("User-Agent = %q", gotUserAgent)
	}
	if quote.Ticker != "BRK.B" || quote.YahooSymbol != "BRK-B" || quote.MarketCap != 1_024_000_000_000 || !quote.HasMarketCap || quote.Currency != "USD" || quote.QuoteType != "EQUITY" || quote.Source != SourceYahoo {
		t.Fatalf("unexpected quote: %+v", quote)
	}
}

func TestYahooMarketCapProviderFallsBackToQuoteSummaryWhenQuoteEndpointIsRateLimited(t *testing.T) {
	var gotPaths []string
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotPaths = append(gotPaths, r.URL.Path)
		if r.URL.Path == "/v7/finance/quote" {
			return &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Status:     "429 Too Many Requests",
				Header:     make(http.Header),
				Body:       io.NopCloser(bytes.NewBufferString(`Edge: Too Many Requests`)),
				Request:    r,
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body: io.NopCloser(bytes.NewBufferString(`{
				"quoteSummary":{
					"result":[{
						"price":{
							"symbol":"AAPL",
							"marketCap":{"raw":3500000000000},
							"currency":"USD",
							"quoteType":"EQUITY"
						}
					}],
					"error":null
				}
			}`)),
			Request: r,
		}, nil
	})}

	provider := NewYahooMarketCapProvider(YahooMarketCapProviderConfig{
		BaseURL: "https://query1.finance.yahoo.com",
		Client:  httpClient,
	})
	provider.sleep = func(time.Duration) {}

	quote, err := provider.FetchMarketCap(context.Background(), "AAPL")
	if err != nil {
		t.Fatalf("FetchMarketCap() error = %v", err)
	}

	if strings.Join(gotPaths, ",") != "/v7/finance/quote,/v7/finance/quote,/v7/finance/quote,/v10/finance/quoteSummary/AAPL" {
		t.Fatalf("unexpected request paths: %+v", gotPaths)
	}
	if quote.MarketCap != 3_500_000_000_000 || quote.Currency != "USD" || quote.QuoteType != "EQUITY" || !quote.HasMarketCap {
		t.Fatalf("unexpected fallback quote: %+v", quote)
	}
}

func TestYahooMarketCapProviderFallsBackToTimeseriesWhenQuoteAPIsRequireCrumb(t *testing.T) {
	var gotPaths []string
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotPaths = append(gotPaths, r.URL.Path)
		switch r.URL.Path {
		case "/v7/finance/quote":
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     make(http.Header),
				Body:       io.NopCloser(bytes.NewBufferString(`{"finance":{"result":null,"error":{"code":"Unauthorized","description":"User is unable to access this feature"}}}`)),
				Request:    r,
			}, nil
		case "/v10/finance/quoteSummary/AAPL":
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     make(http.Header),
				Body:       io.NopCloser(bytes.NewBufferString(`{"finance":{"result":null,"error":{"code":"Unauthorized","description":"Invalid Crumb"}}}`)),
				Request:    r,
			}, nil
		case "/ws/fundamentals-timeseries/v1/finance/timeseries/AAPL":
			assertQueryContains(t, r.URL.RawQuery, "type=quarterlyMarketCap")
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     make(http.Header),
				Body: io.NopCloser(bytes.NewBufferString(`{
					"timeseries":{
						"result":[{
							"meta":{"symbol":["AAPL"],"type":["quarterlyMarketCap"]},
							"quarterlyMarketCap":[{
								"asOfDate":"2025-12-31",
								"currencyCode":"USD",
								"reportedValue":{"raw":3997076837580}
							},{
								"asOfDate":"2026-03-31",
								"currencyCode":"USD",
								"reportedValue":{"raw":3722512537520}
							}]
						}],
						"error":null
					}
				}`)),
				Request: r,
			}, nil
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		return nil, nil
	})}

	provider := NewYahooMarketCapProvider(YahooMarketCapProviderConfig{
		BaseURL: "https://query1.finance.yahoo.com",
		Client:  httpClient,
	})
	provider.sleep = func(time.Duration) {}

	quote, err := provider.FetchMarketCap(context.Background(), "AAPL")
	if err != nil {
		t.Fatalf("FetchMarketCap() error = %v", err)
	}

	if strings.Join(gotPaths, ",") != "/v7/finance/quote,/v10/finance/quoteSummary/AAPL,/ws/fundamentals-timeseries/v1/finance/timeseries/AAPL" {
		t.Fatalf("unexpected request paths: %+v", gotPaths)
	}
	if quote.Ticker != "AAPL" || quote.YahooSymbol != "AAPL" || quote.MarketCap != 3_722_512_537_520 || quote.Currency != "USD" || !quote.HasMarketCap || quote.Source != SourceYahoo {
		t.Fatalf("unexpected timeseries quote: %+v", quote)
	}
}

func TestUniverseUpdaterBuildsCollectableAndExcludedLists(t *testing.T) {
	provider := fakeMarketCapProvider{
		quotes: map[string]MarketCapQuote{
			"AAPL": {Ticker: "AAPL", YahooSymbol: "AAPL", MarketCap: 3_500_000_000_000, HasMarketCap: true, Currency: "USD", QuoteType: "EQUITY", Source: SourceYahoo},
			"TINY": {Ticker: "TINY", YahooSymbol: "TINY", MarketCap: 120_000_000, HasMarketCap: true, Currency: "USD", QuoteType: "EQUITY", Source: SourceYahoo},
			"ZERO": {Ticker: "ZERO", YahooSymbol: "ZERO", MarketCap: 0, HasMarketCap: true, Currency: "USD", QuoteType: "EQUITY", Source: SourceYahoo},
			"FUND": {Ticker: "FUND", YahooSymbol: "FUND", MarketCap: 900_000_000, HasMarketCap: true, Currency: "USD", QuoteType: "ETF", Source: SourceYahoo},
			"MISS": {Ticker: "MISS", YahooSymbol: "MISS", Currency: "USD", QuoteType: "EQUITY", Source: SourceYahoo},
		},
		errs: map[string]error{
			"DEAD": ErrYahooSymbolNotFound,
		},
	}
	updater := NewUniverseUpdater(UniverseUpdaterConfig{
		MarketCapProvider: provider,
		MinMarketCap:      300_000_000,
		Workers:           2,
		Clock:             fixedClock(time.Date(2026, 7, 5, 22, 0, 0, 0, time.UTC)),
	})

	result := updater.Build(context.Background(), []Company{
		{Ticker: "TINY", CIK: 1, Title: "Tiny Corp"},
		{Ticker: "AAPL", CIK: 320193, Title: "Apple Inc."},
		{Ticker: "ZERO", CIK: 2, Title: "Zero Corp"},
		{Ticker: "FUND", CIK: 3, Title: "Fund Trust"},
		{Ticker: "MISS", CIK: 4, Title: "Missing Cap"},
		{Ticker: "DEAD", CIK: 5, Title: "Dead Symbol"},
	})

	if result.Summary.SECTickersTotal != 6 || result.Summary.YahooMarketCapRequests != 6 {
		t.Fatalf("unexpected request summary: %+v", result.Summary)
	}
	if len(result.Collectable) != 1 || result.Collectable[0].Ticker != "AAPL" || result.Collectable[0].CIK != "0000320193" {
		t.Fatalf("unexpected collectable: %+v", result.Collectable)
	}
	if result.Summary.CollectableTickers != 1 || result.Summary.ExcludedTickers != 5 || result.Summary.BelowThreshold != 1 || result.Summary.MissingMarketCap != 1 || result.Summary.YahooErrors != 1 {
		t.Fatalf("unexpected summary: %+v", result.Summary)
	}
	reasons := map[string]string{}
	for _, excluded := range result.Excluded {
		reasons[excluded.Ticker] = excluded.Reason
	}
	wantReasons := map[string]string{
		"DEAD": ReasonYahooSymbolNotFound,
		"FUND": ReasonNotCommonStockLike,
		"MISS": ReasonMarketCapMissing,
		"TINY": ReasonMarketCapBelowThreshold,
		"ZERO": ReasonMarketCapZero,
	}
	for ticker, want := range wantReasons {
		if reasons[ticker] != want {
			t.Fatalf("reason for %s = %q, want %q; all=%+v", ticker, reasons[ticker], want, result.Excluded)
		}
	}
}

func TestUniverseUpdaterLogsEachTickerDecision(t *testing.T) {
	var logBuffer bytes.Buffer
	provider := fakeMarketCapProvider{
		quotes: map[string]MarketCapQuote{
			"AAPL": {Ticker: "AAPL", YahooSymbol: "AAPL", MarketCap: 3_500_000_000_000, HasMarketCap: true, Currency: "USD", QuoteType: "EQUITY", Source: SourceYahoo},
			"TINY": {Ticker: "TINY", YahooSymbol: "TINY", MarketCap: 120_000_000, HasMarketCap: true, Currency: "USD", QuoteType: "EQUITY", Source: SourceYahoo},
		},
	}
	updater := NewUniverseUpdater(UniverseUpdaterConfig{
		MarketCapProvider: provider,
		MinMarketCap:      300_000_000,
		Workers:           1,
		Clock:             fixedClock(time.Date(2026, 7, 5, 22, 0, 0, 0, time.UTC)),
		LogWriter:         &logBuffer,
	})

	result := updater.Build(context.Background(), []Company{
		{Ticker: "AAPL", CIK: 320193, Title: "Apple Inc."},
		{Ticker: "TINY", CIK: 1, Title: "Tiny Corp"},
	})

	if result.Summary.CollectableTickers != 1 || result.Summary.ExcludedTickers != 1 {
		t.Fatalf("unexpected summary: %+v", result.Summary)
	}
	logs := logBuffer.String()
	for _, expected := range []string{
		"AAPL universe collectable",
		"marketCap=3500000000000",
		"TINY universe excluded",
		"reason=market_cap_below_threshold",
	} {
		if !strings.Contains(logs, expected) {
			t.Fatalf("logs %q missing %q", logs, expected)
		}
	}
}

func TestUniverseStoreRewriteWritesSortedJSONLAndMeta(t *testing.T) {
	root := t.TempDir()
	store := NewUniverseStore(root)
	checkedAt := "2026-07-05T22:00:00Z"

	err := store.Rewrite(UniverseUpdateResult{
		Collectable: []CollectableTicker{
			{Ticker: "MSFT", CIK: "0000789019", Title: "Microsoft Corp.", MarketCap: 2_000_000_000_000, Currency: "USD", Source: SourceYahoo, CheckedAt: checkedAt},
			{Ticker: "AAPL", CIK: "0000320193", Title: "Apple Inc.", MarketCap: 3_500_000_000_000, Currency: "USD", Source: SourceYahoo, CheckedAt: checkedAt},
		},
		Excluded: []ExcludedTicker{
			{Ticker: "TINY", CIK: "0000000001", Title: "Tiny Corp", Reason: ReasonMarketCapBelowThreshold, MarketCap: 120_000_000, MinMarketCap: 300_000_000, Source: SourceYahoo, CheckedAt: checkedAt},
			{Ticker: "MISS", CIK: "0000000002", Title: "Missing Cap", Reason: ReasonMarketCapMissing, MinMarketCap: 300_000_000, Source: SourceYahoo, CheckedAt: checkedAt},
		},
		Meta: CollectableTickersMeta{
			Source:             SourceYahoo,
			SECSource:          DefaultSECCompanyTickersURL,
			MinMarketCap:       300_000_000,
			TotalSECTickers:    4,
			CollectableTickers: 2,
			ExcludedTickers:    2,
			LastUpdatedAt:      checkedAt,
		},
	})
	if err != nil {
		t.Fatalf("Rewrite() error = %v", err)
	}

	collectableRaw, err := os.ReadFile(filepath.Join(root, "collectable_tickers.jsonl"))
	if err != nil {
		t.Fatalf("read collectable: %v", err)
	}
	collectableLines := strings.Split(strings.TrimSpace(string(collectableRaw)), "\n")
	if len(collectableLines) != 2 {
		t.Fatalf("collectable lines = %q", string(collectableRaw))
	}
	var first CollectableTicker
	if err := json.Unmarshal([]byte(collectableLines[0]), &first); err != nil {
		t.Fatalf("decode first collectable: %v", err)
	}
	if first.Ticker != "AAPL" || first.MarketCap != 3_500_000_000_000 {
		t.Fatalf("collectable not sorted by market cap desc: %+v", first)
	}

	excludedRaw, err := os.ReadFile(filepath.Join(root, "excluded_tickers.jsonl"))
	if err != nil {
		t.Fatalf("read excluded: %v", err)
	}
	excludedLines := strings.Split(strings.TrimSpace(string(excludedRaw)), "\n")
	var firstExcluded ExcludedTicker
	if err := json.Unmarshal([]byte(excludedLines[0]), &firstExcluded); err != nil {
		t.Fatalf("decode first excluded: %v", err)
	}
	if firstExcluded.Ticker != "MISS" {
		t.Fatalf("excluded not sorted by ticker asc: %+v", firstExcluded)
	}

	meta, err := store.LoadMeta()
	if err != nil {
		t.Fatalf("LoadMeta() error = %v", err)
	}
	if meta.MinMarketCap != 300_000_000 || meta.TotalSECTickers != 4 || meta.CollectableTickers != 2 || meta.ExcludedTickers != 2 || meta.LastUpdatedAt != checkedAt {
		t.Fatalf("unexpected meta: %+v", meta)
	}
}

func TestFilterCompaniesByUniverseUsesSECIntersection(t *testing.T) {
	universe := []CollectableTicker{
		{Ticker: "AAPL", MarketCap: 3_500_000_000_000},
		{Ticker: "MSFT", MarketCap: 2_000_000_000_000},
		{Ticker: "NOTSEC", MarketCap: 1_000_000_000},
	}

	result := FilterCompaniesByUniverse([]Company{
		{Ticker: "AAPL", CIK: 320193, Title: "Apple Inc."},
		{Ticker: "TINY", CIK: 1, Title: "Tiny Corp"},
		{Ticker: "MSFT", CIK: 789019, Title: "Microsoft Corp."},
	}, universe)

	if result.SECTickersTotal != 3 || result.UniverseTickersTotal != 3 || result.FinalTargetTickers != 2 || result.ExcludedByUniverseFilter != 1 {
		t.Fatalf("unexpected filter summary: %+v", result)
	}
	if len(result.Companies) != 2 || result.Companies[0].Ticker != "AAPL" || result.Companies[1].Ticker != "MSFT" {
		t.Fatalf("unexpected final companies: %+v", result.Companies)
	}
}

func TestLoadCollectableTickersMissingFileReturnsActionableError(t *testing.T) {
	_, err := LoadCollectableTickers(filepath.Join(t.TempDir(), "collectable_tickers.jsonl"))
	if !errors.Is(err, ErrCollectableUniverseNotFound) {
		t.Fatalf("error = %v, want ErrCollectableUniverseNotFound", err)
	}
	if err.Error() != "collectable_tickers.jsonl not found. Run update-universe workflow first." {
		t.Fatalf("message = %q", err.Error())
	}
}

type fakeMarketCapProvider struct {
	quotes map[string]MarketCapQuote
	errs   map[string]error
}

func (p fakeMarketCapProvider) FetchMarketCap(ctx context.Context, ticker string) (MarketCapQuote, error) {
	ticker = NormalizeTicker(ticker)
	if err := p.errs[ticker]; err != nil {
		return MarketCapQuote{}, err
	}
	return p.quotes[ticker], nil
}
