package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const DefaultYahooBaseURL = "https://query1.finance.yahoo.com"

type YahooProviderConfig struct {
	BaseURL   string
	UserAgent string
	Client    *http.Client
}

type YahooProvider struct {
	baseURL   string
	userAgent string
	client    *http.Client
}

func NewYahooProvider(config YahooProviderConfig) *YahooProvider {
	baseURL := strings.TrimRight(config.BaseURL, "/")
	if baseURL == "" {
		baseURL = DefaultYahooBaseURL
	}
	client := config.Client
	if client == nil {
		client = http.DefaultClient
	}
	return &YahooProvider{
		baseURL:   baseURL,
		userAgent: config.UserAgent,
		client:    client,
	}
}

func (p *YahooProvider) FetchHistory(ctx context.Context, ticker string, start time.Time, end time.Time) (PriceHistory, error) {
	symbol := YahooSymbol(ticker)
	if start.IsZero() {
		discovery, err := p.fetchChart(ctx, ticker, symbol, yahooQuery{
			Range: "max",
		})
		if err != nil {
			return PriceHistory{}, err
		}
		if len(discovery.Chart.Result) == 0 {
			return PriceHistory{}, nil
		}
		firstTradeDate := discovery.Chart.Result[0].Meta.FirstTradeDate
		if firstTradeDate == 0 {
			return parseYahooResult(NormalizeTicker(ticker), discovery.Chart.Result[0]), nil
		}
		start = time.Unix(firstTradeDate, 0)
	}

	payload, err := p.fetchChart(ctx, ticker, symbol, yahooQuery{
		Period1: startOfUTCDate(start).Unix(),
		Period2: startOfUTCDate(end).AddDate(0, 0, 1).Unix(),
	})
	if err != nil {
		return PriceHistory{}, err
	}
	if len(payload.Chart.Result) == 0 {
		return PriceHistory{}, nil
	}

	return parseYahooResult(NormalizeTicker(ticker), payload.Chart.Result[0]), nil
}

type yahooQuery struct {
	Range   string
	Period1 int64
	Period2 int64
}

func (p *YahooProvider) fetchChart(ctx context.Context, ticker string, symbol string, query yahooQuery) (yahooChartResponse, error) {
	requestURL, err := url.Parse(p.baseURL + "/v8/finance/chart/" + url.PathEscape(symbol))
	if err != nil {
		return yahooChartResponse{}, err
	}

	params := requestURL.Query()
	params.Set("interval", "1d")
	if query.Range != "" {
		params.Set("range", query.Range)
	} else {
		params.Set("period1", strconv.FormatInt(query.Period1, 10))
		params.Set("period2", strconv.FormatInt(query.Period2, 10))
	}
	params.Set("includeAdjustedClose", "true")
	params.Set("events", "div,splits")
	requestURL.RawQuery = params.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return yahooChartResponse{}, err
	}
	if p.userAgent != "" {
		request.Header.Set("User-Agent", p.userAgent)
	}

	response, err := p.client.Do(request)
	if err != nil {
		return yahooChartResponse{}, err
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return yahooChartResponse{}, fmt.Errorf("Yahoo request for %s failed: %s", ticker, response.Status)
	}

	var payload yahooChartResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return yahooChartResponse{}, err
	}
	if payload.Chart.Error != nil {
		return yahooChartResponse{}, fmt.Errorf("Yahoo request for %s failed: %s", ticker, payload.Chart.Error.Description)
	}
	return payload, nil
}

func YahooSymbol(ticker string) string {
	normalized := strings.Join(strings.Fields(NormalizeTicker(ticker)), "")
	return strings.ReplaceAll(normalized, ".", "-")
}

type yahooChartResponse struct {
	Chart struct {
		Result []yahooResult `json:"result"`
		Error  *struct {
			Code        string `json:"code"`
			Description string `json:"description"`
		} `json:"error"`
	} `json:"chart"`
}

type yahooResult struct {
	Meta struct {
		FirstTradeDate int64 `json:"firstTradeDate"`
	} `json:"meta"`
	Timestamps []int64 `json:"timestamp"`
	Events     struct {
		Dividends map[string]struct {
			Amount float64 `json:"amount"`
			Date   int64   `json:"date"`
		} `json:"dividends"`
		Splits map[string]struct {
			Date        int64   `json:"date"`
			Numerator   float64 `json:"numerator"`
			Denominator float64 `json:"denominator"`
			SplitRatio  string  `json:"splitRatio"`
		} `json:"splits"`
	} `json:"events"`
	Indicators struct {
		Quote []struct {
			Open   []*float64 `json:"open"`
			High   []*float64 `json:"high"`
			Low    []*float64 `json:"low"`
			Close  []*float64 `json:"close"`
			Volume []*int64   `json:"volume"`
		} `json:"quote"`
		AdjClose []struct {
			AdjClose []*float64 `json:"adjclose"`
		} `json:"adjclose"`
	} `json:"indicators"`
}

func parseYahooResult(ticker string, result yahooResult) PriceHistory {
	history := PriceHistory{
		Records:   parseYahooRecords(ticker, result),
		Dividends: parseYahooDividends(result),
		Splits:    parseYahooSplits(result),
	}
	return history
}

func parseYahooRecords(ticker string, result yahooResult) []PriceRecord {
	if len(result.Indicators.Quote) == 0 || len(result.Indicators.AdjClose) == 0 {
		return nil
	}

	quote := result.Indicators.Quote[0]
	adjClose := result.Indicators.AdjClose[0]
	records := make([]PriceRecord, 0, len(result.Timestamps))
	for i, timestamp := range result.Timestamps {
		if !hasYahooRecordAt(i, quote.Open, quote.High, quote.Low, quote.Close, adjClose.AdjClose, quote.Volume) {
			continue
		}
		closePrice := *quote.Close[i]
		if closePrice == 0 {
			continue
		}
		adjustRatio := *adjClose.AdjClose[i] / closePrice
		record := PriceRecord{
			Date:              FormatDate(time.Unix(timestamp, 0)),
			Ticker:            ticker,
			Open:              *quote.Open[i],
			High:              *quote.High[i],
			Low:               *quote.Low[i],
			Close:             closePrice,
			AdjOpen:           *quote.Open[i] * adjustRatio,
			AdjHigh:           *quote.High[i] * adjustRatio,
			AdjLow:            *quote.Low[i] * adjustRatio,
			AdjClose:          *adjClose.AdjClose[i],
			Volume:            *quote.Volume[i],
			Source:            SourceYahoo,
			AdjustmentVersion: AdjustmentVersionYahooChartV1,
		}
		if !isValidAdjustedRecord(record) {
			continue
		}
		records = append(records, record)
	}

	sort.SliceStable(records, func(i, j int) bool {
		return records[i].Date < records[j].Date
	})
	return records
}

func isValidAdjustedRecord(record PriceRecord) bool {
	if record.AdjOpen == 0 || record.AdjHigh == 0 || record.AdjLow == 0 || record.AdjClose == 0 {
		return false
	}
	if record.AdjHigh < record.AdjLow {
		return false
	}
	if record.AdjOpen < record.AdjLow || record.AdjOpen > record.AdjHigh {
		return false
	}
	if record.AdjClose < record.AdjLow || record.AdjClose > record.AdjHigh {
		return false
	}
	return true
}

func hasYahooRecordAt(index int, fields ...interface{}) bool {
	for _, field := range fields {
		switch values := field.(type) {
		case []*float64:
			if index >= len(values) || values[index] == nil {
				return false
			}
		case []*int64:
			if index >= len(values) || values[index] == nil {
				return false
			}
		}
	}
	return true
}

func parseYahooDividends(result yahooResult) []Dividend {
	dividends := make([]Dividend, 0, len(result.Events.Dividends))
	for _, item := range result.Events.Dividends {
		dividends = append(dividends, Dividend{
			Date:   FormatDate(time.Unix(item.Date, 0)),
			Amount: item.Amount,
		})
	}
	sort.SliceStable(dividends, func(i, j int) bool {
		return dividends[i].Date < dividends[j].Date
	})
	return dividends
}

func parseYahooSplits(result yahooResult) []Split {
	splits := make([]Split, 0, len(result.Events.Splits))
	for _, item := range result.Events.Splits {
		splits = append(splits, Split{
			Date:        FormatDate(time.Unix(item.Date, 0)),
			Numerator:   item.Numerator,
			Denominator: item.Denominator,
			Ratio:       splitRatio(item.Numerator, item.Denominator),
		})
	}
	sort.SliceStable(splits, func(i, j int) bool {
		return splits[i].Date < splits[j].Date
	})
	return splits
}

func splitRatio(numerator float64, denominator float64) float64 {
	if denominator == 0 {
		return 0
	}
	return numerator / denominator
}
