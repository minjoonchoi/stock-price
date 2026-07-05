# Stock Price Collector

Go-based daily US stock price collection for GitHub Actions.

The collector downloads the SEC company ticker list, fetches daily price history from Yahoo Finance Chart JSON with Stooq CSV fallback, and stores append-only JSONL files by ticker under `data/prices`.

## Usage

```bash
SEC_USER_AGENT="github-stock-collector your@email.com" \
go run ./scripts/collect-prices --data-dir data/prices
```

Collect only selected tickers while testing:

```bash
go run ./scripts/collect-prices \
  --user-agent "github-stock-collector your@email.com" \
  --start-date 2026-01-01 \
  --ticker AAPL,MSFT
```

The GitHub Actions workflow also supports manual inputs for a small verification run: `ticker`, `limit`, `start_date`, `request_delay`, `force_backfill`, and `repair_meta`. Scheduled runs omit ticker/limit inputs and process the SEC ticker list.

Outbound price-provider calls are rate limited. The default request delay is `2s`; override it with `--request-delay` or `PRICE_REQUEST_DELAY`.

Each ticker is stored as:

```text
data/prices/AAPL/AAPL.jsonl
data/prices/AAPL/AAPL.meta.json
```

The meta file tracks the JSONL coverage:

```json
{
  "ticker": "AAPL",
  "source": "yahoo",
  "firstDate": "1980-12-12",
  "lastDate": "2026-07-02",
  "records": 11234,
  "backfillCompleted": true,
  "updatedAt": "2026-07-03T22:00:00Z"
}
```

When a ticker has no JSONL file, has no meta file, has stale/inconsistent meta, or has `backfillCompleted=false`, the collector requests provider max history and merges every returned trading day into the JSONL file. Existing rows are kept, missing Yahoo/Stooq trading days are filled, duplicate dates are collapsed, and newly fetched rows win if the same date already exists.

When `backfillCompleted=true` and the meta matches the JSONL file, the collector fetches only `lastDate + 1 day` through yesterday and appends newly available trading days. If `lastDate` is already yesterday or later, the ticker is skipped without calling the price providers.

Use `--force-backfill` to full-history merge every selected ticker regardless of meta. Use `--repair-meta` to rebuild meta files from local JSONL without fetching price history; when no `--ticker` is supplied, it repairs all ticker directories found under `data/prices`.

For new or incomplete tickers, the default start date is dynamic: Yahoo first discovers `firstTradeDate` and then fetches the daily range from that date; Stooq omits `d1`. Records are still filtered to yesterday. Set `--start-date` or `STOCK_PRICE_START_DATE` only when you intentionally want to cap stored history for a manual run.
