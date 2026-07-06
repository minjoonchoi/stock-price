package nasdaq

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"path/filepath"
	"time"
)

type CollectOptions struct {
	OutputDir   string
	DryRun      bool
	GitHubRunID string
	Now         time.Time
	Logger      *log.Logger
}

type CollectSummary struct {
	RowsFetched   int
	CanonicalRows int
	Status        string
	Error         string
}

func CollectSplits(ctx context.Context, client *Client, options CollectOptions) (CollectSummary, error) {
	now := collectNow(options.Now)
	body, err := client.Get(ctx, "/api/calendar/splits", nil)
	if err != nil {
		return writeSplitsError(options, now, err)
	}
	records, err := ParseSplits(body, now)
	if err != nil {
		return CollectSummary{}, err
	}
	snapshotFile := filepath.Join(options.OutputDir, "snapshots", formatDate(now)+".jsonl")
	canonicalFile := filepath.Join(options.OutputDir, "splits.jsonl")
	metaFile := filepath.Join(options.OutputDir, "splits.meta.json")
	if options.DryRun {
		return CollectSummary{RowsFetched: len(records), CanonicalRows: len(records), Status: "dry-run"}, nil
	}
	if err := WriteJSONL(snapshotFile, records); err != nil {
		return CollectSummary{}, err
	}
	merged, err := WriteMergedJSONL(canonicalFile, records, nil, SplitKey, SortSplits)
	if err != nil {
		return CollectSummary{}, err
	}
	meta := Meta{
		"source":          SourceNasdaq,
		"api":             APISplits,
		"lastCollectedAt": now.UTC().Format(time.RFC3339),
		"asOf":            formatDate(now),
		"rowsFetched":     len(records),
		"canonicalRows":   len(merged),
		"snapshotFile":    filepath.ToSlash(snapshotFile),
		"lastStatus":      "success",
		"lastError":       nil,
	}
	addGitHubRunID(meta, options.GitHubRunID)
	if err := WriteJSONFile(metaFile, meta); err != nil {
		return CollectSummary{}, err
	}
	return CollectSummary{RowsFetched: len(records), CanonicalRows: len(merged), Status: "success"}, nil
}

func CollectDividends(ctx context.Context, client *Client, dates []string, options CollectOptions) (CollectSummary, error) {
	now := collectNow(options.Now)
	allRecords := make([]DividendRecord, 0)
	fetchedDates := 0
	errorMessage := ""
	for _, date := range dates {
		query := url.Values{"date": []string{date}}
		body, err := client.Get(ctx, "/api/calendar/dividends", query)
		if err != nil {
			logf(options.Logger, "%s dividends fetch failed: %v", date, err)
			if errorMessage == "" {
				errorMessage = err.Error()
			}
			continue
		}
		records, err := ParseDividends(body, date, now)
		if err != nil {
			return CollectSummary{}, err
		}
		fetchedDates++
		allRecords = append(allRecords, records...)
		if !options.DryRun {
			snapshotFile := dividendSnapshotFile(options.OutputDir, date)
			if err := WriteJSONL(snapshotFile, records); err != nil {
				return CollectSummary{}, err
			}
		}
	}
	status := "success"
	var lastError any
	if errorMessage != "" {
		status = "error"
		lastError = errorMessage
	}
	if options.DryRun {
		if status == "success" {
			status = "dry-run"
		}
		return CollectSummary{RowsFetched: len(allRecords), CanonicalRows: len(allRecords), Status: status, Error: errorMessage}, nil
	}
	canonicalFile := filepath.Join(options.OutputDir, "dividends.jsonl")
	metaFile := filepath.Join(options.OutputDir, "dividends.meta.json")
	merged, err := WriteMergedJSONL(canonicalFile, allRecords, nil, DividendKey, SortDividends)
	if err != nil {
		return CollectSummary{}, err
	}
	fromDate, toDate := dateSpan(dates)
	meta := Meta{
		"source":          SourceNasdaq,
		"api":             APIDividends,
		"lastCollectedAt": now.UTC().Format(time.RFC3339),
		"fromDate":        fromDate,
		"toDate":          toDate,
		"datesFetched":    fetchedDates,
		"rowsFetched":     len(allRecords),
		"canonicalRows":   len(merged),
		"lastStatus":      status,
		"lastError":       lastError,
	}
	addGitHubRunID(meta, options.GitHubRunID)
	if err := WriteJSONFile(metaFile, meta); err != nil {
		return CollectSummary{}, err
	}
	return CollectSummary{RowsFetched: len(allRecords), CanonicalRows: len(merged), Status: status, Error: errorMessage}, nil
}

func CollectScreener(ctx context.Context, client *Client, screenerOptions ScreenerOptions, options CollectOptions) (CollectSummary, error) {
	now := collectNow(options.Now)
	body, err := client.Get(ctx, ScreenerRequestPath(screenerOptions), nil)
	if err != nil {
		return writeScreenerError(options, screenerOptions, now, err)
	}
	records, err := ParseScreener(body, screenerOptions, now)
	if err != nil {
		return CollectSummary{}, err
	}
	snapshotFile := filepath.Join(options.OutputDir, "snapshots", formatDate(now)+".jsonl")
	canonicalFile := filepath.Join(options.OutputDir, "stocks.jsonl")
	metaFile := filepath.Join(options.OutputDir, "stocks.meta.json")
	if options.DryRun {
		return CollectSummary{RowsFetched: len(records), CanonicalRows: len(records), Status: "dry-run"}, nil
	}
	if err := WriteJSONL(snapshotFile, records); err != nil {
		return CollectSummary{}, err
	}
	merged, err := WriteMergedJSONL(canonicalFile, records, nil, ScreenerKey, SortScreener)
	if err != nil {
		return CollectSummary{}, err
	}
	meta := screenerMeta(options, screenerOptions, now, len(records), len(merged), filepath.ToSlash(snapshotFile), "success", nil)
	if err := WriteJSONFile(metaFile, meta); err != nil {
		return CollectSummary{}, err
	}
	return CollectSummary{RowsFetched: len(records), CanonicalRows: len(merged), Status: "success"}, nil
}

func ScreenerRequestPath(options ScreenerOptions) string {
	return fmt.Sprintf(
		"/api/screener/stocks?tableonly=%t&limit=%d&marketcap=%s&recommendation=%s&country=%s",
		options.TableOnly,
		options.Limit,
		url.QueryEscape(options.MarketCap),
		url.QueryEscape(options.Recommendation),
		url.QueryEscape(options.Country),
	)
}

func writeSplitsError(options CollectOptions, now time.Time, err error) (CollectSummary, error) {
	logf(options.Logger, "splits fetch failed: %v", err)
	if !options.DryRun {
		meta := Meta{
			"source":          SourceNasdaq,
			"api":             APISplits,
			"lastCollectedAt": now.UTC().Format(time.RFC3339),
			"asOf":            formatDate(now),
			"rowsFetched":     0,
			"canonicalRows":   0,
			"snapshotFile":    nil,
			"lastStatus":      "error",
			"lastError":       err.Error(),
		}
		addGitHubRunID(meta, options.GitHubRunID)
		if writeErr := WriteJSONFile(filepath.Join(options.OutputDir, "splits.meta.json"), meta); writeErr != nil {
			return CollectSummary{}, writeErr
		}
	}
	return CollectSummary{Status: "error", Error: err.Error()}, nil
}

func writeScreenerError(options CollectOptions, screenerOptions ScreenerOptions, now time.Time, err error) (CollectSummary, error) {
	logf(options.Logger, "screener fetch failed: %v", err)
	if !options.DryRun {
		meta := screenerMeta(options, screenerOptions, now, 0, 0, "", "error", err.Error())
		if writeErr := WriteJSONFile(filepath.Join(options.OutputDir, "stocks.meta.json"), meta); writeErr != nil {
			return CollectSummary{}, writeErr
		}
	}
	return CollectSummary{Status: "error", Error: err.Error()}, nil
}

func screenerMeta(options CollectOptions, screenerOptions ScreenerOptions, now time.Time, rowsFetched int, canonicalRows int, snapshotFile string, status string, lastError any) Meta {
	meta := Meta{
		"source":          SourceNasdaq,
		"api":             APIScreener,
		"lastCollectedAt": now.UTC().Format(time.RFC3339),
		"limit":           screenerOptions.Limit,
		"marketcap":       screenerOptions.MarketCap,
		"recommendation":  screenerOptions.Recommendation,
		"country":         screenerOptions.Country,
		"rowsFetched":     rowsFetched,
		"canonicalRows":   canonicalRows,
		"snapshotFile":    snapshotFile,
		"lastStatus":      status,
		"lastError":       lastError,
	}
	addGitHubRunID(meta, options.GitHubRunID)
	return meta
}

func dividendSnapshotFile(outputDir string, date string) string {
	parsed, err := time.Parse("2006-01-02", date)
	if err != nil {
		return filepath.Join(outputDir, "by-date", date+".jsonl")
	}
	return filepath.Join(outputDir, "by-date", parsed.Format("2006"), parsed.Format("01"), date+".jsonl")
}

func collectNow(value time.Time) time.Time {
	if value.IsZero() {
		return time.Now().UTC()
	}
	return value.UTC()
}

func dateSpan(dates []string) (string, string) {
	if len(dates) == 0 {
		return "", ""
	}
	return dates[0], dates[len(dates)-1]
}

func addGitHubRunID(meta Meta, runID string) {
	if runID != "" {
		meta["githubRunId"] = runID
	}
}

func logf(logger *log.Logger, format string, values ...any) {
	if logger != nil {
		logger.Printf(format, values...)
	}
}
