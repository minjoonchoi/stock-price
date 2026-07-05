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
	Store                          *FileStore
	Provider                       PriceProvider
	StartDate                      time.Time
	ForceBackfill                  bool
	RepairMeta                     bool
	ForceValidateAdjusted          bool
	FullValidationDays             int
	DisablePriceDiscontinuityCheck bool
	Clock                          func() time.Time
	LogWriter                      io.Writer
}

type Runner struct {
	store                          *FileStore
	provider                       PriceProvider
	startDate                      time.Time
	forceBackfill                  bool
	repairMeta                     bool
	forceValidateAdjusted          bool
	fullValidationDays             int
	disablePriceDiscontinuityCheck bool
	clock                          func() time.Time
	logger                         *log.Logger
}

type Summary struct {
	Processed                int
	Skipped                  int
	Appended                 int
	Failed                   int
	Backfilled               int
	IncrementalUpdated       int
	FullRewritten            int
	SplitDetected            int
	CorporateActionsChanged  int
	DiscontinuityDetected    int
	AdjustedValidated        int
	RowsAdjustedRecalculated int
	ActionsWritten           int
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
	fullValidationDays := config.FullValidationDays
	if fullValidationDays <= 0 {
		fullValidationDays = 7
	}
	return &Runner{
		store:                          config.Store,
		provider:                       config.Provider,
		startDate:                      startOfUTCDate(config.StartDate),
		forceBackfill:                  config.ForceBackfill,
		repairMeta:                     config.RepairMeta,
		forceValidateAdjusted:          config.ForceValidateAdjusted,
		fullValidationDays:             fullValidationDays,
		disablePriceDiscontinuityCheck: config.DisablePriceDiscontinuityCheck,
		clock:                          clock,
		logger:                         log.New(logWriter, "", log.LstdFlags),
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
		existingActions, hasActions, err := r.store.LoadActions(ticker)
		if err != nil {
			summary.Failed++
			return summary, fmt.Errorf("%s load actions: %w", ticker, err)
		}

		if r.repairMeta {
			if !hasPrices {
				summary.Skipped++
				continue
			}
			repairedMeta := metaFromRecordsAndActions(ticker, existingRecords, existingActions, r.clock(), RewriteOptions{
				BackfillCompleted:       false,
				AdjustedSeriesValidated: false,
			})
			if err := r.store.WriteMeta(ticker, repairedMeta); err != nil {
				summary.Failed++
				return summary, fmt.Errorf("%s repair meta: %w", ticker, err)
			}
			summary.Skipped++
			continue
		}

		fullReason := r.fullRewriteReason(ticker, meta, ok, existingRecords, hasPrices, existingActions, hasActions)
		if fullReason != "" {
			if err := r.fullRewrite(ctx, ticker, existingRecords, meta, ok, end, fullReason, &summary); err != nil {
				return summary, err
			}
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

		incrementalActions := corporateActionsFromHistory(ticker, history)
		if hasNewerSplit(incrementalActions, meta.LastSplitDate) {
			summary.SplitDetected++
			if err := r.fullRewrite(ctx, ticker, existingRecords, meta, ok, end, "new-split", &summary); err != nil {
				return summary, err
			}
			continue
		}

		newRecords := filterNewRecords(history.Records, ticker, lastDate, start, end)
		if len(newRecords) == 0 {
			summary.Skipped++
			r.logger.Printf("%s fetch history returned no new records for %s..%s", ticker, FormatDate(start), FormatDate(end))
			continue
		}
		if !r.disablePriceDiscontinuityCheck && hasPriceDiscontinuity(lastPriceRecord(existingRecords), newRecords[0]) {
			summary.DiscontinuityDetected++
			if err := r.fullRewrite(ctx, ticker, existingRecords, meta, ok, end, "price-discontinuity", &summary); err != nil {
				return summary, err
			}
			continue
		}
		if err := r.store.AppendPrices(ticker, newRecords, r.clock()); err != nil {
			summary.Failed++
			return summary, fmt.Errorf("%s append prices: %w", ticker, err)
		}
		mergedRecords := append(append([]PriceRecord(nil), existingRecords...), newRecords...)
		mergedActions := append(append([]CorporateAction(nil), existingActions...), incrementalActions...)
		options := rewriteOptionsFromMeta(meta)
		if _, actionsWritten, err := r.store.RewriteActionsAndMeta(ticker, mergedRecords, mergedActions, r.clock(), options); err != nil {
			summary.Failed++
			return summary, fmt.Errorf("%s update actions/meta: %w", ticker, err)
		} else {
			summary.ActionsWritten += actionsWritten
		}
		summary.Appended += len(newRecords)
		summary.IncrementalUpdated++
	}

	return summary, nil
}

func (r *Runner) fullRewriteReason(ticker string, meta Meta, hasMeta bool, records []PriceRecord, hasPrices bool, actions []CorporateAction, hasActions bool) string {
	if r.forceBackfill {
		return "force-backfill"
	}
	if r.forceValidateAdjusted {
		return "force-validate-adjusted"
	}
	if !hasPrices || len(records) == 0 {
		return "missing-prices"
	}
	if !hasMeta {
		return "missing-meta"
	}
	if !hasActions {
		return "missing-actions"
	}
	if !metaMatchesRecordsAndActions(meta, ticker, records, actions) {
		return "stale-meta"
	}
	if !meta.BackfillCompleted {
		return "backfill-incomplete"
	}
	if !meta.AdjustedSeriesValidated {
		return "adjusted-not-validated"
	}
	if meta.CorporateActionHash == "" || meta.PriceDataHash == "" {
		return "missing-hash"
	}
	if r.clock().UTC().Weekday() == time.Sunday {
		return "weekly-validation"
	}
	if fullValidationExpired(meta.LastFullValidationAt, r.clock, r.fullValidationDays) {
		return "validation-expired"
	}
	return ""
}

func (r *Runner) fullRewrite(ctx context.Context, ticker string, existingRecords []PriceRecord, oldMeta Meta, hasMeta bool, end time.Time, reason string, summary *Summary) error {
	history, err := r.provider.FetchHistory(ctx, ticker, time.Time{}, end)
	if err != nil {
		summary.Failed++
		r.logger.Printf("%s fetch history failed: %v", ticker, err)
		return nil
	}

	freshRecords := filterRecordsInRange(history.Records, ticker, time.Time{}, end)
	if len(freshRecords) == 0 {
		summary.Failed++
		r.logger.Printf("%s fetch history returned no records through %s", ticker, FormatDate(end))
		return nil
	}
	actions := corporateActionsFromHistory(ticker, history)
	newActionHash := hashCorporateActions(actions)
	if hasMeta && oldMeta.CorporateActionHash != "" && oldMeta.CorporateActionHash != newActionHash {
		summary.CorporateActionsChanged++
	}

	meta, actionsWritten, err := r.store.RewriteTickerData(ticker, freshRecords, actions, r.clock(), RewriteOptions{
		BackfillCompleted:       true,
		AdjustedSeriesValidated: isYahooAdjustedSeries(freshRecords),
		FullValidationAt:        r.clock(),
	})
	if err != nil {
		summary.Failed++
		return fmt.Errorf("%s rewrite ticker data: %w", ticker, err)
	}

	summary.Appended += countNewDates(existingRecords, freshRecords)
	summary.Backfilled++
	summary.FullRewritten++
	if meta.AdjustedSeriesValidated {
		summary.AdjustedValidated++
	}
	summary.RowsAdjustedRecalculated += len(freshRecords)
	summary.ActionsWritten += actionsWritten
	r.logger.Printf("%s full rewrite completed: reason=%s records=%d actions=%d", ticker, reason, len(freshRecords), actionsWritten)
	return nil
}

func metaMatchesRecordsAndActions(meta Meta, ticker string, records []PriceRecord, actions []CorporateAction) bool {
	if hasDuplicateRecordDates(records) {
		return false
	}
	if !priceRecordsSorted(records) {
		return false
	}
	expected := metaFromRecordsAndActions(ticker, records, actions, time.Time{}, RewriteOptions{
		BackfillCompleted:       meta.BackfillCompleted,
		AdjustedSeriesValidated: meta.AdjustedSeriesValidated,
	})
	return meta.Ticker == NormalizeTicker(ticker) &&
		meta.FirstDate == expected.FirstDate &&
		meta.LastDate == expected.LastDate &&
		meta.Records == expected.Records &&
		meta.CorporateActionHash == expected.CorporateActionHash &&
		meta.PriceDataHash == expected.PriceDataHash
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

func priceRecordsSorted(records []PriceRecord) bool {
	for i := 1; i < len(records); i++ {
		if records[i-1].Date > records[i].Date {
			return false
		}
	}
	return true
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

func rewriteOptionsFromMeta(meta Meta) RewriteOptions {
	return RewriteOptions{
		BackfillCompleted:       meta.BackfillCompleted,
		AdjustedSeriesValidated: meta.AdjustedSeriesValidated,
		FullValidationAt:        parseMetaTimestamp(meta.LastFullValidationAt),
	}
}

func parseMetaTimestamp(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func fullValidationExpired(lastFullValidationAt string, clock func() time.Time, days int) bool {
	last := parseMetaTimestamp(lastFullValidationAt)
	if last.IsZero() {
		return true
	}
	return !last.AddDate(0, 0, days).After(clock().UTC())
}

func hasNewerSplit(actions []CorporateAction, lastSplitDate string) bool {
	for _, action := range actions {
		if action.Type == ActionTypeSplit && action.Date > lastSplitDate {
			return true
		}
	}
	return false
}

func lastPriceRecord(records []PriceRecord) PriceRecord {
	if len(records) == 0 {
		return PriceRecord{}
	}
	last := records[0]
	for _, record := range records[1:] {
		if record.Date > last.Date {
			last = record
		}
	}
	return last
}

func hasPriceDiscontinuity(previous PriceRecord, next PriceRecord) bool {
	if previous.Close == 0 || next.Close == 0 || previous.AdjClose == 0 || next.AdjClose == 0 {
		return false
	}
	rawRatio := next.Close / previous.Close
	if rawRatio > 0.55 && rawRatio < 1.80 {
		return false
	}
	adjustedRatio := next.AdjClose / previous.AdjClose
	return absFloat(adjustedRatio-1) < absFloat(rawRatio-1)
}

func absFloat(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}

func isYahooAdjustedSeries(records []PriceRecord) bool {
	if len(records) == 0 {
		return false
	}
	for _, record := range records {
		if record.Source != SourceYahoo || record.AdjustmentVersion != AdjustmentVersionYahooChartV1 || !isValidAdjustedRecord(record) {
			return false
		}
	}
	return true
}

func countNewDates(existingRecords []PriceRecord, freshRecords []PriceRecord) int {
	existing := make(map[string]struct{}, len(existingRecords))
	for _, record := range existingRecords {
		existing[record.Date] = struct{}{}
	}
	count := 0
	for _, record := range freshRecords {
		if _, ok := existing[record.Date]; !ok {
			count++
		}
	}
	return count
}
