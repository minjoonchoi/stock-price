package collector

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const DefaultStooqBaseURL = "https://stooq.com"

type StooqProviderConfig struct {
	BaseURL   string
	UserAgent string
	Client    *http.Client
}

type StooqProvider struct {
	baseURL   string
	userAgent string
	client    *http.Client
}

func NewStooqProvider(config StooqProviderConfig) *StooqProvider {
	baseURL := strings.TrimRight(config.BaseURL, "/")
	if baseURL == "" {
		baseURL = DefaultStooqBaseURL
	}
	client := config.Client
	if client == nil {
		client = http.DefaultClient
	}
	return &StooqProvider{
		baseURL:   baseURL,
		userAgent: config.UserAgent,
		client:    client,
	}
}

func (p *StooqProvider) FetchHistory(ctx context.Context, ticker string, start time.Time, end time.Time) (PriceHistory, error) {
	requestURL, err := url.Parse(p.baseURL + "/q/d/l/")
	if err != nil {
		return PriceHistory{}, err
	}

	params := requestURL.Query()
	params.Set("s", StooqSymbol(ticker))
	params.Set("i", "d")
	if !start.IsZero() {
		params.Set("d1", formatStooqDate(start))
	}
	params.Set("d2", formatStooqDate(end))
	requestURL.RawQuery = params.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return PriceHistory{}, err
	}
	if p.userAgent != "" {
		request.Header.Set("User-Agent", p.userAgent)
	}

	response, err := p.client.Do(request)
	if err != nil {
		return PriceHistory{}, err
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return PriceHistory{}, fmt.Errorf("Stooq request for %s failed: %s", ticker, response.Status)
	}

	records, err := parseStooqCSV(response.Body, NormalizeTicker(ticker))
	if err != nil {
		return PriceHistory{}, fmt.Errorf("Stooq request for %s failed: %w", ticker, err)
	}
	if len(records) == 0 {
		return PriceHistory{}, fmt.Errorf("Stooq request for %s failed: no data", ticker)
	}
	return PriceHistory{Records: records}, nil
}

func StooqSymbol(ticker string) string {
	return strings.ToLower(strings.ReplaceAll(NormalizeTicker(ticker), ".", "-")) + ".us"
}

func formatStooqDate(value time.Time) string {
	return value.UTC().Format("20060102")
}

func parseStooqCSV(reader io.Reader, ticker string) ([]PriceRecord, error) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(string(data))
	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "<!doctype") || strings.HasPrefix(lower, "<html") {
		return nil, errors.New("unexpected non-csv response")
	}

	csvReader := csv.NewReader(strings.NewReader(trimmed))
	csvReader.FieldsPerRecord = -1

	rows, err := csvReader.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	if !isStooqHeader(rows[0]) {
		return nil, fmt.Errorf("unexpected csv header %q", strings.Join(rows[0], ","))
	}

	records := make([]PriceRecord, 0, len(rows)-1)
	for _, row := range rows[1:] {
		if len(row) < 6 {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(row[1]), "N/D") {
			continue
		}

		open, err := strconv.ParseFloat(strings.TrimSpace(row[1]), 64)
		if err != nil {
			return nil, fmt.Errorf("parse open for %s: %w", row[0], err)
		}
		high, err := strconv.ParseFloat(strings.TrimSpace(row[2]), 64)
		if err != nil {
			return nil, fmt.Errorf("parse high for %s: %w", row[0], err)
		}
		low, err := strconv.ParseFloat(strings.TrimSpace(row[3]), 64)
		if err != nil {
			return nil, fmt.Errorf("parse low for %s: %w", row[0], err)
		}
		closePrice, err := strconv.ParseFloat(strings.TrimSpace(row[4]), 64)
		if err != nil {
			return nil, fmt.Errorf("parse close for %s: %w", row[0], err)
		}
		volume, err := strconv.ParseInt(strings.TrimSpace(row[5]), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse volume for %s: %w", row[0], err)
		}

		date, err := ParseDate(strings.TrimSpace(row[0]))
		if err != nil {
			return nil, fmt.Errorf("parse date %q: %w", row[0], err)
		}

		records = append(records, PriceRecord{
			Date:     FormatDate(date),
			Ticker:   ticker,
			Open:     open,
			High:     high,
			Low:      low,
			Close:    closePrice,
			AdjClose: closePrice,
			Volume:   volume,
			Source:   SourceStooq,
		})
	}

	sort.SliceStable(records, func(i, j int) bool {
		return records[i].Date < records[j].Date
	})
	return records, nil
}

func isStooqHeader(row []string) bool {
	if len(row) < 6 {
		return false
	}
	expected := []string{"date", "open", "high", "low", "close", "volume"}
	for i, value := range expected {
		if strings.ToLower(strings.TrimSpace(row[i])) != value {
			return false
		}
	}
	return true
}
