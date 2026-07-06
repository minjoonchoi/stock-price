package collector

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type ProgressState struct {
	JobName             string `json:"jobName"`
	RunID               string `json:"runId"`
	MinMarketCap        int64  `json:"minMarketCap,omitempty"`
	TotalTickers        int    `json:"totalTickers,omitempty"`
	ProcessedTickers    int    `json:"processedTickers,omitempty"`
	TotalTargets        int    `json:"totalTargets,omitempty"`
	ProcessedTargets    int    `json:"processedTargets,omitempty"`
	LastProcessedTicker string `json:"lastProcessedTicker,omitempty"`
	CursorIndex         int    `json:"cursorIndex"`
	Completed           bool   `json:"completed"`
	SECTickerHash       string `json:"secTickerHash,omitempty"`
	UniverseHash        string `json:"universeHash,omitempty"`
	ShardIndex          int    `json:"shardIndex,omitempty"`
	ShardCount          int    `json:"shardCount,omitempty"`
	StartedAt           string `json:"startedAt"`
	UpdatedAt           string `json:"updatedAt"`
}

func LoadProgressState(path string) (ProgressState, bool, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return ProgressState{}, false, nil
	}
	if err != nil {
		return ProgressState{}, false, err
	}
	defer file.Close()

	var state ProgressState
	if err := json.NewDecoder(file).Decode(&state); err != nil {
		return ProgressState{}, false, err
	}
	return state, true, nil
}

func WriteProgressState(path string, state ProgressState) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tempPath := path + ".tmp"
	if err := os.WriteFile(tempPath, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	return nil
}

func HashCompanies(companies []Company) string {
	lines := make([]string, 0, len(companies))
	for _, company := range companies {
		ticker := NormalizeTicker(company.Ticker)
		if ticker == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s\t%010d\t%s", ticker, company.CIK, strings.TrimSpace(company.Title)))
	}
	sort.Strings(lines)
	return sha256Lines(lines)
}

func HashCollectableTickers(tickers []CollectableTicker) string {
	lines := make([]string, 0, len(tickers))
	for _, ticker := range tickers {
		normalizedTicker := NormalizeTicker(ticker.Ticker)
		if normalizedTicker == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s\t%s\t%d\t%s", normalizedTicker, ticker.CIK, ticker.MarketCap, strings.TrimSpace(ticker.Source)))
	}
	sort.Strings(lines)
	return sha256Lines(lines)
}

func sha256Lines(lines []string) string {
	hash := sha256.New()
	for _, line := range lines {
		hash.Write([]byte(line))
		hash.Write([]byte{'\n'})
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

type RuntimeBudget struct {
	StartedAt    time.Time
	MaxRuntime   time.Duration
	GracefulStop time.Duration
	Clock        func() time.Time
}

func (b RuntimeBudget) ShouldStopBeforeNext() bool {
	if b.MaxRuntime <= 0 {
		return false
	}
	clock := b.Clock
	if clock == nil {
		clock = time.Now
	}
	stopAt := b.StartedAt.Add(b.MaxRuntime - b.GracefulStop)
	return !clock().Before(stopAt)
}

func UTCDateRunID(now time.Time) string {
	return now.UTC().Format("2006-01-02")
}

func SortCompaniesByTicker(companies []Company) []Company {
	sorted := make([]Company, 0, len(companies))
	for _, company := range companies {
		company.Ticker = NormalizeTicker(company.Ticker)
		if company.Ticker == "" {
			continue
		}
		sorted = append(sorted, company)
	}
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Ticker < sorted[j].Ticker
	})
	return sorted
}

func SelectCompanyShard(companies []Company, shardIndex int, shardCount int) []Company {
	sorted := SortCompaniesByTicker(companies)
	if shardCount <= 1 {
		return sorted
	}
	selected := make([]Company, 0, len(sorted)/shardCount+1)
	for index, company := range sorted {
		if index%shardCount == shardIndex {
			selected = append(selected, company)
		}
	}
	return selected
}
