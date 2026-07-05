# Stock Price Collector

Go-based daily US stock price collection for GitHub Actions.

The collector downloads the SEC company ticker list, fetches daily price history from Yahoo Finance Chart JSON with Stooq CSV fallback, and stores ticker JSONL files under `data/prices`.

## Usage

```bash
SEC_USER_AGENT="github-stock-collector your@email.com" \
go run ./scripts/collect-prices --data-dir data/prices
```

Collect only selected tickers while testing:

```bash
go run ./scripts/collect-prices \
  --user-agent "github-stock-collector your@email.com" \
  --ticker AAPL,MSFT
```

The GitHub Actions workflow also supports manual inputs for a small verification run: `ticker`, `limit`, `start_date`, `request_delay`, `force_backfill`, `repair_meta`, `force_validate_adjusted`, `full_validation_days`, and `disable_price_discontinuity_check`. Scheduled runs omit ticker/limit inputs and process the SEC ticker list at 01:00 UTC.

Outbound price-provider calls are rate limited. The default request delay is `2s`; override it with `--request-delay` or `PRICE_REQUEST_DELAY`.

Each ticker is stored as:

```text
data/prices/AAPL/AAPL.jsonl
data/prices/AAPL/AAPL.meta.json
data/actions/A/AAPL.actions.jsonl
```

Price records store both raw Yahoo OHLC and adjusted OHLC. Yahoo provides `adjClose`; `adjOpen`, `adjHigh`, and `adjLow` are calculated with `adjClose / close`. Rows with invalid adjusted prices are skipped.

The meta file tracks JSONL coverage, adjusted validation, and corporate action hashes:

```json
{
  "ticker": "AAPL",
  "source": "yahoo",
  "firstDate": "1980-12-12",
  "lastDate": "2026-07-02",
  "records": 11234,
  "backfillCompleted": true,
  "adjustedSeriesValidated": true,
  "lastCorporateActionDate": "2020-08-31",
  "lastSplitDate": "2020-08-31",
  "corporateActionHash": "sha256:...",
  "priceDataHash": "sha256:...",
  "lastFullValidationAt": "2026-07-03T22:00:00Z",
  "updatedAt": "2026-07-03T22:00:00Z"
}
```

When a ticker has no JSONL file, has no meta file, has stale/inconsistent meta, has missing hashes, has `backfillCompleted=false`, or has `adjustedSeriesValidated=false`, the collector requests provider max history and rewrites the ticker from the provider response as the source of truth. Full rewrite also rewrites the corporate action JSONL, recalculates adjusted prices, removes duplicate dates, and keeps prices sorted ascending.

When `backfillCompleted=true`, `adjustedSeriesValidated=true`, hashes match, and the meta matches the JSONL file, the collector fetches only `lastDate + 1 day` through yesterday and appends newly available trading days. If the latest range includes a new split or a split-like raw price discontinuity, append is abandoned and a full rewrite is performed.

Use `--force-backfill` or `--force-validate-adjusted` to full-history rewrite every selected ticker regardless of meta. Use `--full-validation-days` to control periodic full validation; the default is 7 days. Use `--repair-meta` to rebuild meta files from local JSONL without fetching price history; when no `--ticker` is supplied, it repairs all ticker directories found under `data/prices`.

For new, incomplete, or validation-required tickers, Yahoo first discovers `firstTradeDate` and then fetches the daily range from that date; Stooq omits `d1`. Full backfill and full validation are not capped by `--start-date`, so partial manual runs cannot accidentally mark truncated history as complete.
