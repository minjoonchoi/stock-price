package nasdaq

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseSplitCalendarRowsKeepsUnsupportedRatioWithParseError(t *testing.T) {
	raw := []byte(`{
		"data": {
			"calendar": {
				"rows": [
					{"symbol":" aapl ","name":" Apple Inc. ","ratio":"4 : 1","executionDate":"8/31/2020"},
					{"symbol":"SNFCA","name":"Security National Financial Corporation","ratio":"5%","executionDate":"2026-07-10"},
					{"symbol":"","name":"Missing Symbol","ratio":"3:1","executionDate":"2026-07-10"}
				]
			}
		}
	}`)
	records, err := ParseSplits(raw, time.Date(2026, 7, 6, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("ParseSplits() error = %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("len(records) = %d, want 2", len(records))
	}
	first := records[0]
	if first.Symbol != "AAPL" || first.Name != "Apple Inc." || first.RatioRaw != "4 : 1" || value(first.Numerator) != 4 || value(first.Denominator) != 1 || value(first.Ratio) != 4 {
		t.Fatalf("unexpected parsed split: %+v", first)
	}
	if first.ExecutionDate == nil || *first.ExecutionDate != "2020-08-31" {
		t.Fatalf("executionDate = %v, want 2020-08-31", first.ExecutionDate)
	}
	second := records[1]
	if second.ParseError == nil || *second.ParseError != "unsupported_percent_ratio" {
		t.Fatalf("parseError = %v, want unsupported_percent_ratio", second.ParseError)
	}
	if second.Numerator != nil || second.Denominator != nil || second.Ratio != nil {
		t.Fatalf("percent ratio should keep numeric fields null: %+v", second)
	}
}

func TestParseDividendsNormalizesDatesAndNumbers(t *testing.T) {
	raw := []byte(`{
		"data": {
			"calendar": {
				"rows": [
					{
						"symbol":"csco",
						"companyName":" Cisco Systems, Inc. Common Stock (DE) ",
						"dividend_Ex_Date":"7/06/2026",
						"paymentDate":"07/22/2026",
						"recordDate":"7/6/2026",
						"dividend_Rate":"$0.42",
						"indicated_Annual_Dividend":"1.68",
						"announcementDate":"2026-05-13"
					},
					{"symbol":"MISS","companyName":"Missing Ex Date","dividend_Rate":"--"}
				]
			}
		}
	}`)
	records, err := ParseDividends(raw, "2026-07-06", time.Date(2026, 7, 6, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("ParseDividends() error = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("len(records) = %d, want 1", len(records))
	}
	record := records[0]
	if record.Symbol != "CSCO" || record.CompanyName != "Cisco Systems, Inc. Common Stock (DE)" || record.ExDividendDate != "2026-07-06" {
		t.Fatalf("unexpected dividend record: %+v", record)
	}
	if record.PaymentDate == nil || *record.PaymentDate != "2026-07-22" || record.RecordDate == nil || *record.RecordDate != "2026-07-06" {
		t.Fatalf("dates not normalized: %+v", record)
	}
	if value(record.DividendRate) != 0.42 || value(record.IndicatedAnnualDividend) != 1.68 {
		t.Fatalf("numbers not normalized: %+v", record)
	}
}

func TestParseDividendsTreatsMissingRowsAsEmptyCalendar(t *testing.T) {
	records, err := ParseDividends([]byte(`{"data":null,"message":null,"status":{"rCode":200}}`), "2026-07-05", time.Date(2026, 7, 6, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("ParseDividends() error = %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("len(records) = %d, want 0", len(records))
	}
}

func TestParseScreenerNormalizesNumbersAndSortsByMarketCap(t *testing.T) {
	raw := []byte(`{
		"data": {
			"table": {
				"rows": [
					{"symbol":"msft","name":"Microsoft Corporation Common Stock","lastsale":"$410.00","netchange":"--","pctchange":"1.25%","marketCap":"3,100,000,000,000","url":"/market-activity/stocks/msft"},
					{"symbol":"aapl","name":"Apple Inc. Common Stock","lastsale":"$308.63","netchange":"14.25","pctchange":"4.841%","marketCap":"4,532,958,682,280","url":"/market-activity/stocks/aapl"}
				]
			}
		}
	}`)
	records, err := ParseScreener(raw, ScreenerOptions{
		Limit:          1500,
		MarketCap:      "mega|large|mid",
		Recommendation: "strong_buy|buy",
		Country:        "united_states",
		TableOnly:      false,
	}, time.Date(2026, 7, 6, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("ParseScreener() error = %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("len(records) = %d, want 2", len(records))
	}
	if records[0].Symbol != "AAPL" || value(records[0].MarketCap) != 4532958682280 {
		t.Fatalf("records not sorted by market cap desc: %+v", records)
	}
	if records[1].NetChange != nil {
		t.Fatalf("-- netchange should be null: %+v", records[1])
	}
}

func TestScreenerRequestPathPreservesNasdaqQueryOrder(t *testing.T) {
	path := ScreenerRequestPath(ScreenerOptions{
		Limit:          5,
		MarketCap:      "mega|large|mid",
		Recommendation: "strong_buy|buy",
		Country:        "united_states",
		TableOnly:      false,
	})
	expected := "/api/screener/stocks?tableonly=false&limit=5&marketcap=mega%7Clarge%7Cmid&recommendation=strong_buy%7Cbuy&country=united_states"
	if path != expected {
		t.Fatalf("path = %q, want %q", path, expected)
	}
}

func TestMergeWriteJSONLDedupesAndPrefersLatestRecord(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "splits.jsonl")
	oldDate := "2020-08-31"
	newDate := "2020-08-31"
	records := []SplitRecord{
		{Symbol: "AAPL", Name: "Old", RatioRaw: "4 : 1", ExecutionDate: &oldDate, Source: SourceNasdaq, API: APISplits, AsOf: "2026-07-05", CollectedAt: "2026-07-05T00:00:00Z"},
	}
	if _, err := WriteMergedJSONL(path, records, nil, SplitKey, SortSplits); err != nil {
		t.Fatalf("WriteMergedJSONL initial error = %v", err)
	}
	latest := []SplitRecord{
		{Symbol: "AAPL", Name: "Apple Inc.", RatioRaw: "4 : 1", ExecutionDate: &newDate, Source: SourceNasdaq, API: APISplits, AsOf: "2026-07-06", CollectedAt: "2026-07-06T00:00:00Z"},
		{Symbol: "MSFT", Name: "Microsoft", RatioRaw: "2:1", ExecutionDate: &newDate, Source: SourceNasdaq, API: APISplits, AsOf: "2026-07-06", CollectedAt: "2026-07-06T00:00:00Z"},
	}
	merged, err := WriteMergedJSONL(path, latest, nil, SplitKey, SortSplits)
	if err != nil {
		t.Fatalf("WriteMergedJSONL merge error = %v", err)
	}
	if len(merged) != 2 || merged[0].Name != "Apple Inc." {
		t.Fatalf("latest record did not replace existing duplicate: %+v", merged)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		t.Fatalf("jsonl lines = %d, want 2\n%s", len(lines), raw)
	}
	for _, line := range lines {
		var decoded map[string]any
		if err := json.Unmarshal([]byte(line), &decoded); err != nil {
			t.Fatalf("invalid JSONL line %q: %v", line, err)
		}
	}
}

func TestClientAddsNasdaqHeadersAndRetriesRetriableStatus(t *testing.T) {
	attempts := 0
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		attempts++
		if r.Header.Get("Origin") != "https://www.nasdaq.com" || r.Header.Get("Referer") != "https://www.nasdaq.com/" {
			t.Fatalf("missing Nasdaq headers: %#v", r.Header)
		}
		if !strings.Contains(r.Header.Get("User-Agent"), "Chrome/") {
			t.Fatalf("unexpected user agent: %q", r.Header.Get("User-Agent"))
		}
		if r.Header.Get("Sec-Fetch-Site") != "same-site" || r.Header.Get("Cache-Control") != "no-cache" {
			t.Fatalf("missing browser-style headers: %#v", r.Header)
		}
		if attempts == 1 {
			return &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Status:     "429 Too Many Requests",
				Body:       io.NopCloser(strings.NewReader("slow down")),
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body:       io.NopCloser(strings.NewReader(`{"data":{"calendar":{"rows":[]}}}`)),
		}, nil
	})}

	client := NewClient(ClientConfig{
		BaseURL:    "https://example.test",
		HTTPClient: httpClient,
		Sleep:      func(time.Duration) {},
	})
	body, err := client.Get(context.Background(), "/api/calendar/splits", nil)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	if !strings.Contains(string(body), `"rows":[]`) {
		t.Fatalf("body = %s", body)
	}
}

func TestNewHTTPClientUsesDefaultTransportForHTTP2(t *testing.T) {
	client := NewHTTPClient(7 * time.Second)
	if client.Timeout != 7*time.Second {
		t.Fatalf("timeout = %s, want 7s", client.Timeout)
	}
	if client.Transport != nil {
		t.Fatalf("transport = %T, want nil default transport", client.Transport)
	}
}

func TestClientFallsBackToCurlOnNasdaqTransportInternalError(t *testing.T) {
	fallbackCalled := false
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, io.ErrUnexpectedEOF
	})}
	client := NewClient(ClientConfig{
		BaseURL:    "https://example.test",
		HTTPClient: httpClient,
		Sleep:      func(time.Duration) {},
		CurlFallback: func(ctx context.Context, requestURL string) ([]byte, error) {
			fallbackCalled = true
			if !strings.Contains(requestURL, "/api/screener/stocks?") || !strings.Contains(requestURL, "limit=5") {
				t.Fatalf("fallback URL = %q", requestURL)
			}
			return []byte(`{"data":{"table":{"rows":[]}}}`), nil
		},
	})
	body, err := client.Get(context.Background(), "/api/screener/stocks", url.Values{"limit": []string{"5"}})
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !fallbackCalled {
		t.Fatal("expected curl fallback to be called")
	}
	if !strings.Contains(string(body), `"rows":[]`) {
		t.Fatalf("body = %s", body)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func value[T ~float64 | ~int64](pointer *T) T {
	if pointer == nil {
		var zero T
		return zero
	}
	return *pointer
}
