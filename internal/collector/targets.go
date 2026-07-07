package collector

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

const (
	TargetSourceNasdaqScreener      = "nasdaq-screener"
	TargetSourceCollectableUniverse = "collectable-universe"
	SourceNasdaqScreener            = "nasdaq"
	DefaultNasdaqScreenerStocksFile = "data/nasdaq/screener/stocks.jsonl"
)

var ErrNasdaqScreenerStocksNotFound = errors.New("nasdaq screener stocks.jsonl not found. Run collect-nasdaq-screener workflow first.")

type PriceTarget struct {
	SECTicker      string   `json:"secTicker"`
	NormalizedKey  string   `json:"normalizedKey"`
	CIK            string   `json:"cik"`
	Title          string   `json:"title"`
	ScreenerSymbol string   `json:"screenerSymbol"`
	ScreenerName   string   `json:"screenerName"`
	MarketCap      *float64 `json:"marketCap"`
	Recommendation string   `json:"recommendation"`
	Source         string   `json:"source"`
}

type nasdaqScreenerTargetRecord struct {
	Symbol         string   `json:"symbol"`
	Name           string   `json:"name"`
	MarketCap      *float64 `json:"marketCap"`
	Recommendation string   `json:"recommendationFilter"`
	Source         string   `json:"source"`
}

func LoadNasdaqScreenerPriceTargets(path string, companies []Company) ([]PriceTarget, UniverseFilterResult, error) {
	records, err := loadNasdaqScreenerTargetRecords(path)
	if err != nil {
		return nil, UniverseFilterResult{}, err
	}
	targets, filter := nasdaqScreenerPriceTargets(companies, records)
	return targets, filter, nil
}

func loadNasdaqScreenerTargetRecords(path string) ([]nasdaqScreenerTargetRecord, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNasdaqScreenerStocksNotFound
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var records []nasdaqScreenerTargetRecord
	scanner := bufio.NewScanner(file)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var record nasdaqScreenerTargetRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			return nil, fmt.Errorf("decode nasdaq screener %s:%d: %w", path, lineNumber, err)
		}
		record.Symbol = strings.TrimSpace(record.Symbol)
		record.Name = strings.TrimSpace(record.Name)
		record.Recommendation = strings.TrimSpace(record.Recommendation)
		record.Source = strings.TrimSpace(record.Source)
		if record.Source == "" {
			record.Source = SourceNasdaqScreener
		}
		if normalizePriceTargetRaw(record.Symbol) == "" {
			continue
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

func nasdaqScreenerPriceTargets(companies []Company, records []nasdaqScreenerTargetRecord) ([]PriceTarget, UniverseFilterResult) {
	screenerByKey := make(map[string]nasdaqScreenerTargetRecord, len(records))
	uniqueScreenerKeys := make(map[string]struct{}, len(records))
	for _, record := range records {
		canonicalKey := NormalizePriceTargetKey(record.Symbol)
		if canonicalKey == "" {
			continue
		}
		uniqueScreenerKeys[canonicalKey] = struct{}{}
		for _, key := range PriceTargetKeyCandidates(record.Symbol) {
			if _, exists := screenerByKey[key]; exists {
				continue
			}
			screenerByKey[key] = record
		}
	}

	secCompanies := SortCompaniesByTicker(companies)
	targets := make([]PriceTarget, 0, len(secCompanies))
	for _, company := range secCompanies {
		matchedKey, record, ok := findScreenerTargetRecord(company.Ticker, screenerByKey)
		if !ok {
			continue
		}
		targets = append(targets, PriceTarget{
			SECTicker:      company.Ticker,
			NormalizedKey:  matchedKey,
			CIK:            formatCIK(company.CIK),
			Title:          strings.TrimSpace(company.Title),
			ScreenerSymbol: record.Symbol,
			ScreenerName:   record.Name,
			MarketCap:      cloneFloat64(record.MarketCap),
			Recommendation: record.Recommendation,
			Source:         record.Source,
		})
	}

	return targets, UniverseFilterResult{
		Companies:                CompaniesFromPriceTargets(targets),
		SECTickersTotal:          len(secCompanies),
		UniverseTickersTotal:     len(uniqueScreenerKeys),
		FinalTargetTickers:       len(targets),
		ExcludedByUniverseFilter: len(secCompanies) - len(targets),
	}
}

func findScreenerTargetRecord(ticker string, screenerByKey map[string]nasdaqScreenerTargetRecord) (string, nasdaqScreenerTargetRecord, bool) {
	for _, key := range PriceTargetKeyCandidates(ticker) {
		record, ok := screenerByKey[key]
		if ok {
			return key, record, true
		}
	}
	return "", nasdaqScreenerTargetRecord{}, false
}

func CompaniesFromPriceTargets(targets []PriceTarget) []Company {
	companies := make([]Company, 0, len(targets))
	for _, target := range targets {
		cik, _ := strconv.Atoi(strings.TrimLeft(target.CIK, "0"))
		companies = append(companies, Company{
			CIK:    cik,
			Ticker: target.SECTicker,
			Title:  target.Title,
		})
	}
	return SortCompaniesByTicker(companies)
}

func HashPriceTargets(targets []PriceTarget) string {
	lines := make([]string, 0, len(targets))
	for _, target := range targets {
		ticker := NormalizeTicker(target.SECTicker)
		if ticker == "" {
			continue
		}
		marketCap := ""
		if target.MarketCap != nil {
			marketCap = strconv.FormatFloat(*target.MarketCap, 'f', -1, 64)
		}
		lines = append(lines, fmt.Sprintf("%s\t%s\t%s\t%s\t%s\t%s\t%s", ticker, target.NormalizedKey, target.CIK, target.ScreenerSymbol, marketCap, target.Recommendation, target.Source))
	}
	sort.Strings(lines)
	return sha256Lines(lines)
}

func NormalizePriceTargetKey(symbol string) string {
	raw := normalizePriceTargetRaw(symbol)
	if raw == "" {
		return ""
	}
	return strings.NewReplacer(".", "-", "/", "-").Replace(raw)
}

func PriceTargetKeyCandidates(symbol string) []string {
	raw := normalizePriceTargetRaw(symbol)
	if raw == "" {
		return nil
	}
	dashed := strings.NewReplacer(".", "-", "/", "-").Replace(raw)
	if dashed == raw {
		return []string{raw}
	}
	return []string{raw, dashed}
}

func normalizePriceTargetRaw(symbol string) string {
	return strings.ToUpper(strings.Join(strings.Fields(strings.TrimSpace(symbol)), ""))
}

func cloneFloat64(value *float64) *float64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
