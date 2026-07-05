package collector

import (
	"bufio"
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
	root string
}

func NewFileStore(root string) *FileStore {
	return &FileStore{root: root}
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
	return s.WriteMeta(normalizedTicker, meta)
}

func (s *FileStore) RewritePrices(ticker string, records []PriceRecord, updatedAt time.Time, backfillCompleted bool) error {
	normalizedTicker := NormalizeTicker(ticker)
	records = canonicalPriceRecords(records, normalizedTicker)
	path := s.jsonlPath(normalizedTicker)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	tempPath := path + ".tmp"
	file, err := os.OpenFile(tempPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	writer := bufio.NewWriter(file)
	for _, record := range records {
		line, err := json.Marshal(record)
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

	return s.WriteMeta(normalizedTicker, metaFromRecords(normalizedTicker, records, updatedAt, backfillCompleted))
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
	normalizedTicker := NormalizeTicker(ticker)
	meta := Meta{
		Ticker:            normalizedTicker,
		Source:            SourceYahoo,
		Records:           len(records),
		BackfillCompleted: backfillCompleted,
		UpdatedAt:         updatedAt.UTC().Format(time.RFC3339),
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
	return meta
}
