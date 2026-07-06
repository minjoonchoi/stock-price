package nasdaq

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

func ParseSplits(raw []byte, collectedAt time.Time) ([]SplitRecord, error) {
	rows, err := extractRows(raw, "data.calendar.rows", "data.rows")
	if err != nil {
		return nil, fmt.Errorf("nasdaq %s response missing rows: %w", APISplits, err)
	}
	now := collectedAt.UTC()
	records := make([]SplitRecord, 0, len(rows))
	for _, row := range rows {
		symbol := normalizeSymbol(field(row, "symbol"))
		if symbol == "" {
			continue
		}
		ratioRaw := strings.TrimSpace(field(row, "ratio", "ratioRaw", "splitRatio"))
		numerator, denominator, ratio, parseError := parseSplitRatio(ratioRaw)
		records = append(records, SplitRecord{
			Symbol:        symbol,
			Name:          normalizeText(field(row, "name", "companyName")),
			RatioRaw:      ratioRaw,
			Numerator:     numerator,
			Denominator:   denominator,
			Ratio:         ratio,
			ParseError:    parseError,
			ExecutionDate: parseDatePointer(field(row, "executionDate", "execution_Date", "effectiveDate")),
			Source:        SourceNasdaq,
			API:           APISplits,
			AsOf:          formatDate(now),
			CollectedAt:   now.Format(time.RFC3339),
		})
	}
	SortSplits(records)
	return records, nil
}

func ParseDividends(raw []byte, calendarDate string, collectedAt time.Time) ([]DividendRecord, error) {
	rows, err := extractRows(raw, "data.calendar.rows", "data.rows")
	if err != nil {
		return []DividendRecord{}, nil
	}
	now := collectedAt.UTC()
	normalizedCalendarDate := normalizeDate(calendarDate)
	records := make([]DividendRecord, 0, len(rows))
	for _, row := range rows {
		symbol := normalizeSymbol(field(row, "symbol"))
		if symbol == "" {
			continue
		}
		exDividendDate := normalizeDate(field(row, "exDividendDate", "dividendExDate", "dividend_Ex_Date", "exOrEffDate", "exDate", "ex_Dividend_Date", "ex_Eff_Date"))
		if exDividendDate == "" {
			continue
		}
		records = append(records, DividendRecord{
			Symbol:                  symbol,
			CompanyName:             normalizeText(field(row, "companyName", "name")),
			ExDividendDate:          exDividendDate,
			PaymentDate:             parseDatePointer(field(row, "paymentDate", "payment_Date")),
			RecordDate:              parseDatePointer(field(row, "recordDate", "record_Date")),
			DividendRate:            parseFloatPointer(field(row, "dividendRate", "dividend_Rate")),
			IndicatedAnnualDividend: parseFloatPointer(field(row, "indicatedAnnualDividend", "indicated_Annual_Dividend")),
			AnnouncementDate:        parseDatePointer(field(row, "announcementDate", "announcement_Date", "declarationDate")),
			Source:                  SourceNasdaq,
			API:                     APIDividends,
			CalendarDate:            normalizedCalendarDate,
			AsOf:                    formatDate(now),
			CollectedAt:             now.Format(time.RFC3339),
		})
	}
	SortDividends(records)
	return records, nil
}

func ParseScreener(raw []byte, options ScreenerOptions, collectedAt time.Time) ([]ScreenerRecord, error) {
	rows, err := extractRows(raw, "data.table.rows", "data.rows")
	if err != nil {
		return nil, fmt.Errorf("nasdaq %s response missing rows: %w", APIScreener, err)
	}
	now := collectedAt.UTC()
	records := make([]ScreenerRecord, 0, len(rows))
	for _, row := range rows {
		symbol := normalizeSymbol(field(row, "symbol"))
		if symbol == "" {
			continue
		}
		records = append(records, ScreenerRecord{
			Symbol:               symbol,
			Name:                 normalizeText(field(row, "name", "companyName")),
			LastSale:             parseFloatPointer(field(row, "lastSale", "lastsale", "last_sale")),
			NetChange:            parseFloatPointer(field(row, "netChange", "netchange", "net_change")),
			PctChange:            parseFloatPointer(field(row, "pctChange", "pctchange", "pct_change")),
			MarketCap:            parseInt64Pointer(field(row, "marketCap", "marketcap", "market_Cap")),
			URL:                  normalizeText(field(row, "url")),
			Country:              options.Country,
			MarketCapFilter:      options.MarketCap,
			RecommendationFilter: options.Recommendation,
			Source:               SourceNasdaq,
			API:                  APIScreener,
			CollectedAt:          now.Format(time.RFC3339),
		})
	}
	SortScreener(records)
	return records, nil
}

func SplitKey(record SplitRecord) string {
	return record.Symbol + "|" + stringValue(record.ExecutionDate) + "|" + record.RatioRaw
}

func DividendKey(record DividendRecord) string {
	return record.Symbol + "|" + record.ExDividendDate + "|" + stringValue(record.PaymentDate) + "|" + stringValue(record.RecordDate) + "|" + floatValue(record.DividendRate)
}

func ScreenerKey(record ScreenerRecord) string {
	return record.Symbol
}

func SortSplits(records []SplitRecord) {
	sort.SliceStable(records, func(i, j int) bool {
		leftDate := stringValue(records[i].ExecutionDate)
		rightDate := stringValue(records[j].ExecutionDate)
		if leftDate == rightDate {
			return records[i].Symbol < records[j].Symbol
		}
		if leftDate == "" {
			return false
		}
		if rightDate == "" {
			return true
		}
		return leftDate < rightDate
	})
}

func SortDividends(records []DividendRecord) {
	sort.SliceStable(records, func(i, j int) bool {
		if records[i].ExDividendDate == records[j].ExDividendDate {
			return records[i].Symbol < records[j].Symbol
		}
		return records[i].ExDividendDate < records[j].ExDividendDate
	})
}

func SortScreener(records []ScreenerRecord) {
	sort.SliceStable(records, func(i, j int) bool {
		left := int64Value(records[i].MarketCap)
		right := int64Value(records[j].MarketCap)
		if left == right {
			return records[i].Symbol < records[j].Symbol
		}
		return left > right
	})
}

func extractRows(raw []byte, paths ...string) ([]map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var payload any
	if err := decoder.Decode(&payload); err != nil {
		return nil, err
	}
	for _, path := range paths {
		rows, ok := valueAtPath(payload, strings.Split(path, ".")).([]any)
		if !ok {
			continue
		}
		result := make([]map[string]any, 0, len(rows))
		for _, item := range rows {
			row, ok := item.(map[string]any)
			if !ok {
				continue
			}
			result = append(result, row)
		}
		return result, nil
	}
	return nil, fmt.Errorf("tried paths %s", strings.Join(paths, ", "))
}

func valueAtPath(value any, path []string) any {
	current := value
	for _, segment := range path {
		object, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current, ok = object[segment]
		if !ok {
			return nil
		}
	}
	return current
}

func field(row map[string]any, names ...string) string {
	normalized := make(map[string]any, len(row))
	for key, value := range row {
		normalized[normalizeFieldName(key)] = value
	}
	for _, name := range names {
		value, ok := normalized[normalizeFieldName(name)]
		if ok {
			return scalarString(value)
		}
	}
	return ""
}

func normalizeFieldName(value string) string {
	var builder strings.Builder
	for _, r := range strings.ToLower(value) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			builder.WriteRune(r)
		}
	}
	return builder.String()
}

func scalarString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case bool:
		if typed {
			return "true"
		}
		return "false"
	default:
		return ""
	}
}

func normalizeSymbol(value string) string {
	return strings.ToUpper(strings.TrimSpace(value))
}

func normalizeText(value string) string {
	return strings.TrimSpace(value)
}

func normalizeDate(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || isNullish(value) {
		return ""
	}
	layouts := []string{"2006-01-02", "1/2/2006", "1/02/2006", "01/2/2006", "01/02/2006"}
	for _, layout := range layouts {
		parsed, err := time.ParseInLocation(layout, value, time.UTC)
		if err == nil {
			return formatDate(parsed)
		}
	}
	return ""
}

func parseDatePointer(value string) *string {
	normalized := normalizeDate(value)
	if normalized == "" {
		return nil
	}
	return &normalized
}

func parseFloatPointer(value string) *float64 {
	normalized := normalizeNumber(value)
	if normalized == "" {
		return nil
	}
	parsed, err := strconv.ParseFloat(normalized, 64)
	if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		return nil
	}
	return &parsed
}

func parseInt64Pointer(value string) *int64 {
	parsed := parseFloatPointer(value)
	if parsed == nil || *parsed == 0 {
		return nil
	}
	result := int64(*parsed)
	return &result
}

func normalizeNumber(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || isNullish(value) {
		return ""
	}
	value = strings.TrimPrefix(value, "$")
	value = strings.TrimSuffix(value, "%")
	value = strings.ReplaceAll(value, ",", "")
	value = strings.TrimSpace(value)
	if value == "" || isNullish(value) {
		return ""
	}
	return value
}

func isNullish(value string) bool {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "", "N/A", "NA", "--", "-":
		return true
	default:
		return false
	}
}

func parseSplitRatio(value string) (*float64, *float64, *float64, *string) {
	ratioRaw := strings.TrimSpace(value)
	if ratioRaw == "" {
		return nil, nil, nil, stringPointer("missing_ratio")
	}
	if strings.HasSuffix(ratioRaw, "%") {
		return nil, nil, nil, stringPointer("unsupported_percent_ratio")
	}
	parts := strings.Split(ratioRaw, ":")
	if len(parts) != 2 {
		return nil, nil, nil, stringPointer("unsupported_ratio_format")
	}
	numerator := parseFloatPointer(parts[0])
	denominator := parseFloatPointer(parts[1])
	if numerator == nil || denominator == nil || *denominator == 0 {
		return nil, nil, nil, stringPointer("invalid_ratio")
	}
	ratio := *numerator / *denominator
	return numerator, denominator, &ratio, nil
}

func formatDate(value time.Time) string {
	return value.UTC().Format("2006-01-02")
}

func stringPointer(value string) *string {
	return &value
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func floatValue(value *float64) string {
	if value == nil {
		return "null"
	}
	return strconv.FormatFloat(*value, 'f', -1, 64)
}

func int64Value(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}
