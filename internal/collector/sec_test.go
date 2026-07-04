package collector

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"
)

func TestSECClientFetchesCompanyTickersWithUserAgentAndSortedUniqueTickers(t *testing.T) {
	var gotUserAgent string
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotUserAgent = r.Header.Get("User-Agent")
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body: io.NopCloser(bytes.NewBufferString(`{
			"1":{"cik_str":320193,"ticker":"AAPL","title":"Apple Inc."},
			"2":{"cik_str":789019,"ticker":"msft","title":"Microsoft Corp."},
			"3":{"cik_str":320193,"ticker":"AAPL","title":"Apple Inc. Duplicate"}
		}`)),
			Request: r,
		}, nil
	})}

	client := NewSECClient(SECClientConfig{
		URL:       "https://www.sec.gov/files/company_tickers.json",
		UserAgent: "github-stock-collector test@example.com",
		Client:    httpClient,
	})

	companies, err := client.FetchCompanies(context.Background())
	if err != nil {
		t.Fatalf("FetchCompanies() error = %v", err)
	}

	if gotUserAgent != "github-stock-collector test@example.com" {
		t.Fatalf("User-Agent = %q", gotUserAgent)
	}
	if len(companies) != 2 {
		t.Fatalf("expected 2 unique companies, got %+v", companies)
	}
	if companies[0].Ticker != "AAPL" || companies[0].CIK != 320193 || companies[1].Ticker != "MSFT" {
		t.Fatalf("unexpected companies: %+v", companies)
	}
}
