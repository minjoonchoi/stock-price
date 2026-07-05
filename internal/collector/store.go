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
	for _, record := range records {
		record.Ticker = NormalizeTicker(record.Ticker)
		if record.Ticker == "" {
			record.Ticker = normalizedTicker
		}
		if record.Source == "" {
			record.Source = SourceYahoo
		}
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
	if !ok {
		meta.Source = lastRecord.Source
	}
	meta.LastDate = lastRecord.Date
	meta.Records += encodedCount
	meta.UpdatedAt = updatedAt.UTC().Format(time.RFC3339)
	meta.Source = lastRecord.Source
	return s.WriteMeta(normalizedTicker, meta)
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
