package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
)

const DefaultSECCompanyTickersURL = "https://www.sec.gov/files/company_tickers.json"

type SECClientConfig struct {
	URL       string
	UserAgent string
	Client    *http.Client
}

type SECClient struct {
	url       string
	userAgent string
	client    *http.Client
}

func NewSECClient(config SECClientConfig) *SECClient {
	url := config.URL
	if url == "" {
		url = DefaultSECCompanyTickersURL
	}
	client := config.Client
	if client == nil {
		client = http.DefaultClient
	}
	return &SECClient{
		url:       url,
		userAgent: config.UserAgent,
		client:    client,
	}
}

func (c *SECClient) FetchCompanies(ctx context.Context) ([]Company, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return nil, err
	}
	if c.userAgent != "" {
		request.Header.Set("User-Agent", c.userAgent)
	}
	request.Header.Set("Accept", "application/json,text/plain,*/*")
	request.Header.Set("Accept-Encoding", "identity")

	response, err := c.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("SEC tickers request failed: %s", response.Status)
	}

	var payload map[string]struct {
		CIK    int    `json:"cik_str"`
		Ticker string `json:"ticker"`
		Title  string `json:"title"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, err
	}

	byTicker := make(map[string]Company, len(payload))
	for _, item := range payload {
		ticker := NormalizeTicker(item.Ticker)
		if ticker == "" {
			continue
		}
		if _, exists := byTicker[ticker]; exists {
			continue
		}
		byTicker[ticker] = Company{
			CIK:    item.CIK,
			Ticker: ticker,
			Title:  item.Title,
		}
	}

	companies := make([]Company, 0, len(byTicker))
	for _, company := range byTicker {
		companies = append(companies, company)
	}
	sort.Slice(companies, func(i, j int) bool {
		return companies[i].Ticker < companies[j].Ticker
	})
	return companies, nil
}
