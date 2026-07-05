package collector

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type FileStore struct {
	root        string
	actionsRoot string
}

func NewFileStore(root string) *FileStore {
	return &FileStore{root: root, actionsRoot: defaultActionsRoot(root)}
}

func (s *FileStore) LoadMeta(ticker string) (Meta, bool, error) {
	path := s.metaPath(ticker)
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return Meta{}, false, nil
	}
	if err != nil {
		return Meta{}, false, err
	}
	defer file.Close()

	var meta Meta
	if err := json.NewDecoder(file).Decode(&meta); err != nil {
		return Meta{}, false, fmt.Errorf("decode meta %s: %w", path, err)
	}
	return meta, true, nil
}

func (s *FileStore) LoadPrices(ticker string) ([]PriceRecord, bool, error) {
	normalizedTicker := NormalizeTicker(ticker)
	path := s.jsonlPath(normalizedTicker)
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	records := make([]PriceRecord, 0)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var record PriceRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			return nil, true, fmt.Errorf("decode price %s:%d: %w", path, lineNumber, err)
		}
		if _, err := ParseDate(record.Date); err != nil {
			return nil, true, fmt.Errorf("decode price %s:%d invalid date %q: %w", path, lineNumber, record.Date, err)
		}
		records = append(records, normalizePriceRecord(record, normalizedTicker))
	}
	if err := scanner.Err(); err != nil {
		return nil, true, err
	}
	return records, true, nil
}

func (s *FileStore) LoadActions(ticker string) ([]CorporateAction, bool, error) {
	normalizedTicker := NormalizeTicker(ticker)
	path := s.actionsPath(normalizedTicker)
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	actions := make([]CorporateAction, 0)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var action CorporateAction
		if err := json.Unmarshal([]byte(line), &action); err != nil {
			return nil, true, fmt.Errorf("decode action %s:%d: %w", path, lineNumber, err)
		}
		if _, err := ParseDate(action.Date); err != nil {
			return nil, true, fmt.Errorf("decode action %s:%d invalid date %q: %w", path, lineNumber, action.Date, err)
		}
		actions = append(actions, normalizeCorporateAction(action, normalizedTicker))
	}
	if err := scanner.Err(); err != nil {
		return nil, true, err
	}
	return canonicalCorporateActions(actions, normalizedTicker), true, nil
}

func (s *FileStore) WriteMeta(ticker string, meta Meta) error {
	path := s.metaPath(ticker)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tempPath := path + ".tmp"
	if err := os.WriteFile(tempPath, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func (s *FileStore) AppendPrices(ticker string, records []PriceRecord, updatedAt time.Time) error {
	if len(records) == 0 {
		return nil
	}

	normalizedTicker := NormalizeTicker(ticker)
	sort.SliceStable(records, func(i, j int) bool {
		return records[i].Date < records[j].Date
	})

	meta, ok, err := s.LoadMeta(normalizedTicker)
	if err != nil {
		return err
	}

	dir := filepath.Dir(s.jsonlPath(normalizedTicker))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	file, err := os.OpenFile(s.jsonlPath(normalizedTicker), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	writer := bufio.NewWriter(file)
	encodedCount := 0
	for index, record := range records {
		record = normalizePriceRecord(record, normalizedTicker)
		records[index] = record
		line, err := json.Marshal(record)
		if err != nil {
			_ = file.Close()
			return err
		}
		if _, err := writer.Write(line); err != nil {
			_ = file.Close()
			return err
		}
		if err := writer.WriteByte('\n'); err != nil {
			_ = file.Close()
			return err
		}
		encodedCount++
	}
	if err := writer.Flush(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}

	lastRecord := records[len(records)-1]
	firstRecord := records[0]
	if !ok {
		meta.Ticker = normalizedTicker
		meta.FirstDate = firstRecord.Date
		meta.Source = lastRecord.Source
	}
	if meta.Ticker == "" {
		meta.Ticker = normalizedTicker
	}
	if meta.FirstDate == "" || firstRecord.Date < meta.FirstDate {
		meta.FirstDate = firstRecord.Date
	}
	if meta.LastDate == "" || lastRecord.Date > meta.LastDate {
		meta.LastDate = lastRecord.Date
	}
	meta.Records += encodedCount
	meta.UpdatedAt = updatedAt.UTC().Format(time.RFC3339)
	meta.Source = lastRecord.Source
	if allPricesAdjusted(records) && meta.AdjustedSeriesValidated {
		meta.PriceDataHash = hashPriceRecordsFromStore(s, normalizedTicker)
	}
	return s.WriteMeta(normalizedTicker, meta)
}

func (s *FileStore) RewritePrices(ticker string, records []PriceRecord, updatedAt time.Time, backfillCompleted bool) error {
	_, _, err := s.RewriteTickerData(ticker, records, nil, updatedAt, RewriteOptions{
		BackfillCompleted: backfillCompleted,
	})
	return err
}

type RewriteOptions struct {
	BackfillCompleted       bool
	AdjustedSeriesValidated bool
	FullValidationAt        time.Time
}

func (s *FileStore) RewriteTickerData(ticker string, records []PriceRecord, actions []CorporateAction, updatedAt time.Time, options RewriteOptions) (Meta, int, error) {
	normalizedTicker := NormalizeTicker(ticker)
	records = canonicalPriceRecords(records, normalizedTicker)
	actions = canonicalCorporateActions(actions, normalizedTicker)

	path := s.jsonlPath(normalizedTicker)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Meta{}, 0, err
	}
	if err := writeJSONL(path, records); err != nil {
		return Meta{}, 0, err
	}

	meta, actionsWritten, err := s.RewriteActionsAndMeta(normalizedTicker, records, actions, updatedAt, options)
	if err != nil {
		return Meta{}, 0, err
	}

	return meta, actionsWritten, nil
}

func (s *FileStore) RewriteActionsAndMeta(ticker string, records []PriceRecord, actions []CorporateAction, updatedAt time.Time, options RewriteOptions) (Meta, int, error) {
	normalizedTicker := NormalizeTicker(ticker)
	records = canonicalPriceRecords(records, normalizedTicker)
	actions = canonicalCorporateActions(actions, normalizedTicker)

	actionsPath := s.actionsPath(normalizedTicker)
	if err := os.MkdirAll(filepath.Dir(actionsPath), 0o755); err != nil {
		return Meta{}, 0, err
	}
	if err := writeJSONL(actionsPath, actions); err != nil {
		return Meta{}, 0, err
	}

	meta := metaFromRecordsAndActions(normalizedTicker, records, actions, updatedAt, options)
	if err := s.WriteMeta(normalizedTicker, meta); err != nil {
		return Meta{}, 0, err
	}

	return meta, len(actions), nil
}

func writeJSONL[T any](path string, values []T) error {
	tempPath := path + ".tmp"
	file, err := os.OpenFile(tempPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	writer := bufio.NewWriter(file)
	for _, value := range values {
		line, err := json.Marshal(value)
		if err != nil {
			_ = file.Close()
			_ = os.Remove(tempPath)
			return err
		}
		if _, err := writer.Write(line); err != nil {
			_ = file.Close()
			_ = os.Remove(tempPath)
			return err
		}
		if err := writer.WriteByte('\n'); err != nil {
			_ = file.Close()
			_ = os.Remove(tempPath)
			return err
		}
	}
	if err := writer.Flush(); err != nil {
		_ = file.Close()
		_ = os.Remove(tempPath)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	return nil
}

func (s *FileStore) ListTickers() ([]string, error) {
	entries, err := os.ReadDir(s.root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	seen := make(map[string]struct{})
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		matches, err := filepath.Glob(filepath.Join(s.root, entry.Name(), "*.jsonl"))
		if err != nil {
			return nil, err
		}
		for _, match := range matches {
			ticker := NormalizeTicker(strings.TrimSuffix(filepath.Base(match), filepath.Ext(match)))
			if ticker == "" {
				continue
			}
			seen[ticker] = struct{}{}
		}
	}
	tickers := make([]string, 0, len(seen))
	for ticker := range seen {
		tickers = append(tickers, ticker)
	}
	sort.Strings(tickers)
	return tickers, nil
}

func (s *FileStore) jsonlPath(ticker string) string {
	ticker = NormalizeTicker(ticker)
	return filepath.Join(s.root, tickerDirectory(ticker), ticker+".jsonl")
}

func (s *FileStore) metaPath(ticker string) string {
	ticker = NormalizeTicker(ticker)
	return filepath.Join(s.root, tickerDirectory(ticker), ticker+".meta.json")
}

func (s *FileStore) actionsPath(ticker string) string {
	ticker = NormalizeTicker(ticker)
	if ticker == "" {
		return filepath.Join(s.actionsRoot, "_", "_.actions.jsonl")
	}
	return filepath.Join(s.actionsRoot, ticker[:1], ticker+".actions.jsonl")
}

func defaultActionsRoot(root string) string {
	if filepath.Base(filepath.Clean(root)) == "prices" {
		return filepath.Join(filepath.Dir(filepath.Clean(root)), "actions")
	}
	return filepath.Join(root, "actions")
}

func tickerDirectory(ticker string) string {
	ticker = NormalizeTicker(ticker)
	if ticker == "" {
		return "_"
	}
	return strings.NewReplacer("/", "-", "\\", "-", ":", "-").Replace(ticker)
}

func NormalizeTicker(ticker string) string {
	return strings.ToUpper(strings.TrimSpace(ticker))
}

func normalizePriceRecord(record PriceRecord, fallbackTicker string) PriceRecord {
	record.Ticker = NormalizeTicker(record.Ticker)
	if record.Ticker == "" {
		record.Ticker = NormalizeTicker(fallbackTicker)
	}
	if record.Source == "" {
		record.Source = SourceYahoo
	}
	if record.AdjClose == 0 {
		record.AdjClose = record.Close
	}
	if record.AdjOpen == 0 {
		record.AdjOpen = record.Open
	}
	if record.AdjHigh == 0 {
		record.AdjHigh = record.High
	}
	if record.AdjLow == 0 {
		record.AdjLow = record.Low
	}
	if record.AdjustmentVersion == "" {
		if record.Source == SourceStooq {
			record.AdjustmentVersion = AdjustmentVersionStooqRawV1
		} else {
			record.AdjustmentVersion = AdjustmentVersionYahooChartV1
		}
	}
	return record
}

func canonicalPriceRecords(records []PriceRecord, ticker string) []PriceRecord {
	byDate := make(map[string]PriceRecord, len(records))
	for _, record := range records {
		record = normalizePriceRecord(record, ticker)
		if _, err := ParseDate(record.Date); err != nil {
			continue
		}
		byDate[record.Date] = record
	}

	dates := make([]string, 0, len(byDate))
	for date := range byDate {
		dates = append(dates, date)
	}
	sort.Strings(dates)

	canonical := make([]PriceRecord, 0, len(dates))
	for _, date := range dates {
		canonical = append(canonical, byDate[date])
	}
	return canonical
}

func metaFromRecords(ticker string, records []PriceRecord, updatedAt time.Time, backfillCompleted bool) Meta {
	return metaFromRecordsAndActions(ticker, records, nil, updatedAt, RewriteOptions{
		BackfillCompleted: backfillCompleted,
	})
}

func metaFromRecordsAndActions(ticker string, records []PriceRecord, actions []CorporateAction, updatedAt time.Time, options RewriteOptions) Meta {
	normalizedTicker := NormalizeTicker(ticker)
	meta := Meta{
		Ticker:                  normalizedTicker,
		Source:                  SourceYahoo,
		Records:                 len(records),
		BackfillCompleted:       options.BackfillCompleted,
		AdjustedSeriesValidated: options.AdjustedSeriesValidated,
		CorporateActionHash:     hashCorporateActions(actions),
		PriceDataHash:           hashPriceRecords(records),
		UpdatedAt:               updatedAt.UTC().Format(time.RFC3339),
	}
	if !options.FullValidationAt.IsZero() {
		meta.LastFullValidationAt = options.FullValidationAt.UTC().Format(time.RFC3339)
	}
	if len(records) == 0 {
		return meta
	}

	sortedRecords := append([]PriceRecord(nil), records...)
	sort.SliceStable(sortedRecords, func(i, j int) bool {
		return sortedRecords[i].Date < sortedRecords[j].Date
	})
	meta.FirstDate = sortedRecords[0].Date
	meta.LastDate = sortedRecords[len(sortedRecords)-1].Date
	meta.Source = sortedRecords[len(sortedRecords)-1].Source
	if meta.Source == "" {
		meta.Source = SourceYahoo
	}
	for _, action := range actions {
		if action.Date > meta.LastCorporateActionDate {
			meta.LastCorporateActionDate = action.Date
		}
		if action.Type == ActionTypeSplit && action.Date > meta.LastSplitDate {
			meta.LastSplitDate = action.Date
		}
	}
	return meta
}

func normalizeCorporateAction(action CorporateAction, ticker string) CorporateAction {
	action.Ticker = NormalizeTicker(action.Ticker)
	if action.Ticker == "" {
		action.Ticker = NormalizeTicker(ticker)
	}
	if action.Source == "" {
		action.Source = SourceYahoo
	}
	if action.Type == ActionTypeSplit && action.Ratio == 0 && action.Denominator != 0 {
		action.Ratio = action.Numerator / action.Denominator
	}
	return action
}

func canonicalCorporateActions(actions []CorporateAction, ticker string) []CorporateAction {
	byKey := make(map[string]CorporateAction, len(actions))
	for _, action := range actions {
		action = normalizeCorporateAction(action, ticker)
		if _, err := ParseDate(action.Date); err != nil {
			continue
		}
		if action.Type != ActionTypeSplit && action.Type != ActionTypeDividend {
			continue
		}
		key := action.Date + "|" + action.Type
		byKey[key] = action
	}

	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	canonical := make([]CorporateAction, 0, len(keys))
	for _, key := range keys {
		canonical = append(canonical, byKey[key])
	}
	return canonical
}

func corporateActionsFromHistory(ticker string, history PriceHistory) []CorporateAction {
	actions := make([]CorporateAction, 0, len(history.Splits)+len(history.Dividends))
	for _, split := range history.Splits {
		actions = append(actions, CorporateAction{
			Date:        split.Date,
			Ticker:      NormalizeTicker(ticker),
			Type:        ActionTypeSplit,
			Numerator:   split.Numerator,
			Denominator: split.Denominator,
			Ratio:       split.Ratio,
			Source:      SourceYahoo,
		})
	}
	for _, dividend := range history.Dividends {
		actions = append(actions, CorporateAction{
			Date:   dividend.Date,
			Ticker: NormalizeTicker(ticker),
			Type:   ActionTypeDividend,
			Amount: dividend.Amount,
			Source: SourceYahoo,
		})
	}
	return canonicalCorporateActions(actions, ticker)
}

func hashPriceRecords(records []PriceRecord) string {
	return hashJSONLines(canonicalPriceRecords(records, ""))
}

func hashCorporateActions(actions []CorporateAction) string {
	return hashJSONLines(canonicalCorporateActions(actions, ""))
}

func hashJSONLines[T any](values []T) string {
	hash := sha256.New()
	for _, value := range values {
		line, err := json.Marshal(value)
		if err != nil {
			continue
		}
		hash.Write(line)
		hash.Write([]byte{'\n'})
	}
	return "sha256:" + fmt.Sprintf("%x", hash.Sum(nil))
}

func hashPriceRecordsFromStore(store *FileStore, ticker string) string {
	records, ok, err := store.LoadPrices(ticker)
	if err != nil || !ok {
		return ""
	}
	return hashPriceRecords(records)
}

func allPricesAdjusted(records []PriceRecord) bool {
	for _, record := range records {
		if record.AdjOpen == 0 || record.AdjHigh == 0 || record.AdjLow == 0 || record.AdjClose == 0 || record.AdjustmentVersion == "" {
			return false
		}
	}
	return true
}
