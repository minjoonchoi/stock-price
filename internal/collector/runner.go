package collector

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"sort"
	"time"
)

type RunnerConfig struct {
	Store     *FileStore
	Provider  PriceProvider
	StartDate time.Time
	Clock     func() time.Time
	LogWriter io.Writer
}

type Runner struct {
	store     *FileStore
	provider  PriceProvider
	startDate time.Time
	clock     func() time.Time
	logger    *log.Logger
}

type Summary struct {
	Processed int
	Skipped   int
	Appended  int
	Failed    int
}

func NewRunner(config RunnerConfig) *Runner {
	clock := config.Clock
	if clock == nil {
		clock = time.Now
	}
	logWriter := config.LogWriter
	if logWriter == nil {
		logWriter = os.Stderr
	}
	return &Runner{
		store:     config.Store,
		provider:  config.Provider,
		startDate: startOfUTCDate(config.StartDate),
		clock:     clock,
		logger:    log.New(logWriter, "", log.LstdFlags),
	}
}

func (r *Runner) CollectTickers(ctx context.Context, companies []Company) (Summary, error) {
	var summary Summary
	end := yesterday(r.clock)

	for _, company := range companies {
		ticker := NormalizeTicker(company.Ticker)
		if ticker == "" {
			continue
		}
		summary.Processed++

		meta, ok, err := r.store.LoadMeta(ticker)
		if err != nil {
			summary.Failed++
			return summary, fmt.Errorf("%s load meta: %w", ticker, err)
		}

		start := r.startDate
		lastDate := time.Time{}
		if ok && meta.LastDate != "" {
			parsedLastDate, err := ParseDate(meta.LastDate)
			if err != nil {
				summary.Failed++
				return summary, fmt.Errorf("%s parse lastDate %q: %w", ticker, meta.LastDate, err)
			}
			lastDate = parsedLastDate
			start = parsedLastDate.AddDate(0, 0, 1)
		}

		if !start.After(end) {
			history, err := r.provider.FetchHistory(ctx, ticker, start, end)
			if err != nil {
				summary.Failed++
				r.logger.Printf("%s fetch history failed: %v", ticker, err)
				continue
			}

			newRecords := filterNewRecords(history.Records, ticker, lastDate, start, end)
			if len(newRecords) == 0 {
				summary.Failed++
				r.logger.Printf("%s fetch history returned no new records for %s..%s", ticker, FormatDate(start), FormatDate(end))
				continue
			}
			if err := r.store.AppendPrices(ticker, newRecords, r.clock()); err != nil {
				summary.Failed++
				return summary, fmt.Errorf("%s append prices: %w", ticker, err)
			}
			summary.Appended += len(newRecords)
			continue
		}

		summary.Skipped++
	}

	return summary, nil
}

func filterNewRecords(records []PriceRecord, ticker string, lastDate time.Time, start time.Time, end time.Time) []PriceRecord {
	seen := make(map[string]struct{}, len(records))
	filtered := make([]PriceRecord, 0, len(records))
	for _, record := range records {
		record.Ticker = NormalizeTicker(record.Ticker)
		if record.Ticker == "" {
			record.Ticker = NormalizeTicker(ticker)
		}
		if record.Source == "" {
			record.Source = SourceYahoo
		}

		recordDate, err := ParseDate(record.Date)
		if err != nil {
			continue
		}
		if !lastDate.IsZero() && !recordDate.After(lastDate) {
			continue
		}
		if recordDate.Before(start) || recordDate.After(end) {
			continue
		}
		if _, ok := seen[record.Date]; ok {
			continue
		}
		seen[record.Date] = struct{}{}
		filtered = append(filtered, record)
	}

	sort.SliceStable(filtered, func(i, j int) bool {
		return filtered[i].Date < filtered[j].Date
	})
	return filtered
}
