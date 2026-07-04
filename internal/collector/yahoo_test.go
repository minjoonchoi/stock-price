package collector

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestYahooProviderFetchHistoryParsesPricesAndCorporateActions(t *testing.T) {
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
			"chart":{
				"result":[{
					"timestamp":[1782912600,1782999000],
					"events":{
						"dividends":{"1782999000":{"amount":0.25,"date":1782999000}},
						"splits":{"1782999000":{"date":1782999000,"numerator":4,"denominator":1,"splitRatio":"4:1"}}
					},
					"indicators":{
						"quote":[{
							"open":[212.11,213.00],
							"high":[214.52,215.00],
							"low":[210.84,212.50],
							"close":[213.71,214.00],
							"volume":[52413300,48000000]
						}],
						"adjclose":[{"adjclose":[213.71,214.00]}]
					}
				}],
				"error":null
			}
		}`)),
			Request: r,
		}, nil
	})}

	provider := NewYahooProvider(YahooProviderConfig{
		BaseURL:   "https://query1.finance.yahoo.com",
		UserAgent: "github-stock-collector test@example.com",
		Client:    httpClient,
	})

	history, err := provider.FetchHistory(context.Background(), "AAPL", mustDate(t, "2026-07-01"), mustDate(t, "2026-07-02"))
	if err != nil {
		t.Fatalf("FetchHistory() error = %v", err)
	}

	if gotPath != "/v8/finance/chart/AAPL" {
		t.Fatalf("path = %q", gotPath)
	}
	assertQueryContains(t, gotQuery, "interval=1d")
	assertQueryContains(t, gotQuery, "includeAdjustedClose=true")
	assertQueryContains(t, gotQuery, "events=div%2Csplits")
	assertQueryContains(t, gotQuery, "period1=1782864000")
	assertQueryContains(t, gotQuery, "period2=1783036800")
	if gotUserAgent != "github-stock-collector test@example.com" {
		t.Fatalf("User-Agent = %q", gotUserAgent)
	}

	if len(history.Records) != 2 {
		t.Fatalf("expected 2 records, got %+v", history.Records)
	}
	if history.Records[0].Date != "2026-07-01" || history.Records[0].Ticker != "AAPL" || history.Records[0].AdjClose != 213.71 || history.Records[0].Source != SourceYahoo {
		t.Fatalf("unexpected first record: %+v", history.Records[0])
	}
	if len(history.Dividends) != 1 || history.Dividends[0].Date != "2026-07-02" || history.Dividends[0].Amount != 0.25 {
		t.Fatalf("unexpected dividends: %+v", history.Dividends)
	}
	if len(history.Splits) != 1 || history.Splits[0].Date != "2026-07-02" || history.Splits[0].Ratio != "4:1" {
		t.Fatalf("unexpected splits: %+v", history.Splits)
	}
}

func assertQueryContains(t *testing.T, query string, fragment string) {
	t.Helper()
	if !strings.Contains(query, fragment) {
		t.Fatalf("query %q does not contain %q", query, fragment)
	}
}
