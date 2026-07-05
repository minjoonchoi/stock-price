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
	Store         *FileStore
	Provider      PriceProvider
	StartDate     time.Time
	ForceBackfill bool
	RepairMeta    bool
	Clock         func() time.Time
	LogWriter     io.Writer
}

type Runner struct {
	store         *FileStore
	provider      PriceProvider
	startDate     time.Time
	forceBackfill bool
	repairMeta    bool
	clock         func() time.Time
	logger        *log.Logger
}

type Summary struct {
	Processed          int
	Skipped            int
	Appended           int
	Failed             int
	Backfilled         int
	IncrementalUpdated int
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
		store:         config.Store,
		provider:      config.Provider,
		startDate:     startOfUTCDate(config.StartDate),
		forceBackfill: config.ForceBackfill,
		repairMeta:    config.RepairMeta,
		clock:         clock,
		logger:        log.New(logWriter, "", log.LstdFlags),
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

		existingRecords, hasPrices, err := r.store.LoadPrices(ticker)
		if err != nil {
			summary.Failed++
			return summary, fmt.Errorf("%s load prices: %w", ticker, err)
		}
		meta, ok, err := r.store.LoadMeta(ticker)
		if err != nil {
			summary.Failed++
			return summary, fmt.Errorf("%s load meta: %w", ticker, err)
		}

		if r.repairMeta {
			if !hasPrices {
				summary.Skipped++
				continue
			}
			repairedMeta := metaFromRecords(ticker, existingRecords, r.clock(), false)
			if err := r.store.WriteMeta(ticker, repairedMeta); err != nil {
				summary.Failed++
				return summary, fmt.Errorf("%s repair meta: %w", ticker, err)
			}
			summary.Skipped++
			continue
		}

		needsBackfill := r.forceBackfill || !hasPrices || len(existingRecords) == 0
		if hasPrices {
			if !ok || !metaMatchesRecords(meta, ticker, existingRecords) || !meta.BackfillCompleted {
				needsBackfill = true
			}
		}

		if needsBackfill {
			history, err := r.provider.FetchHistory(ctx, ticker, time.Time{}, end)
			if err != nil {
				summary.Failed++
				r.logger.Printf("%s fetch history failed: %v", ticker, err)
				continue
			}

			freshRecords := filterRecordsInRange(history.Records, ticker, r.startDate, end)
			if len(freshRecords) == 0 {
				summary.Failed++
				r.logger.Printf("%s fetch history returned no records through %s", ticker, FormatDate(end))
				continue
			}
			mergedRecords, added := mergePriceRecords(existingRecords, freshRecords, ticker, end)
			if err := r.store.RewritePrices(ticker, mergedRecords, r.clock(), true); err != nil {
				summary.Failed++
				return summary, fmt.Errorf("%s rewrite prices: %w", ticker, err)
			}
			summary.Appended += added
			summary.Backfilled++
			continue
		}

		lastDate, err := ParseDate(meta.LastDate)
		if err != nil {
			summary.Failed++
			return summary, fmt.Errorf("%s parse lastDate %q: %w", ticker, meta.LastDate, err)
		}
		start := lastDate.AddDate(0, 0, 1)
		if start.After(end) {
			summary.Skipped++
			continue
		}

		history, err := r.provider.FetchHistory(ctx, ticker, start, end)
		if err != nil {
			summary.Failed++
			r.logger.Printf("%s fetch history failed: %v", ticker, err)
			continue
		}

		newRecords := filterNewRecords(history.Records, ticker, lastDate, start, end)
		if len(newRecords) == 0 {
			summary.Skipped++
			r.logger.Printf("%s fetch history returned no new records for %s..%s", ticker, FormatDate(start), FormatDate(end))
			continue
		}
		if err := r.store.AppendPrices(ticker, newRecords, r.clock()); err != nil {
			summary.Failed++
			return summary, fmt.Errorf("%s append prices: %w", ticker, err)
		}
		summary.Appended += len(newRecords)
		summary.IncrementalUpdated++
	}

	return summary, nil
}

func metaMatchesRecords(meta Meta, ticker string, records []PriceRecord) bool {
	if hasDuplicateRecordDates(records) {
		return false
	}
	expected := metaFromRecords(ticker, records, time.Time{}, meta.BackfillCompleted)
	return meta.Ticker == NormalizeTicker(ticker) &&
		meta.FirstDate == expected.FirstDate &&
		meta.LastDate == expected.LastDate &&
		meta.Records == expected.Records
}

func hasDuplicateRecordDates(records []PriceRecord) bool {
	seen := make(map[string]struct{}, len(records))
	for _, record := range records {
		if _, ok := seen[record.Date]; ok {
			return true
		}
		seen[record.Date] = struct{}{}
	}
	return false
}

func filterRecordsInRange(records []PriceRecord, ticker string, start time.Time, end time.Time) []PriceRecord {
	filtered := make([]PriceRecord, 0, len(records))
	for _, record := range records {
		record = normalizePriceRecord(record, ticker)
		recordDate, err := ParseDate(record.Date)
		if err != nil || recordDate.After(end) {
			continue
		}
		if !start.IsZero() && recordDate.Before(start) {
			continue
		}
		filtered = append(filtered, record)
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		return filtered[i].Date < filtered[j].Date
	})
	return filtered
}

func mergePriceRecords(existingRecords []PriceRecord, freshRecords []PriceRecord, ticker string, end time.Time) ([]PriceRecord, int) {
	byDate := make(map[string]PriceRecord, len(existingRecords)+len(freshRecords))
	existingDates := make(map[string]struct{}, len(existingRecords))
	for _, record := range existingRecords {
		record = normalizePriceRecord(record, ticker)
		if _, err := ParseDate(record.Date); err != nil {
			continue
		}
		byDate[record.Date] = record
		existingDates[record.Date] = struct{}{}
	}

	addedDates := make(map[string]struct{})
	for _, record := range freshRecords {
		record = normalizePriceRecord(record, ticker)
		recordDate, err := ParseDate(record.Date)
		if err != nil || recordDate.After(end) {
			continue
		}
		if _, existed := existingDates[record.Date]; !existed {
			addedDates[record.Date] = struct{}{}
		}
		byDate[record.Date] = record
	}

	dates := make([]string, 0, len(byDate))
	for date := range byDate {
		dates = append(dates, date)
	}
	sort.Strings(dates)

	merged := make([]PriceRecord, 0, len(dates))
	for _, date := range dates {
		merged = append(merged, byDate[date])
	}
	return merged, len(addedDates)
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
