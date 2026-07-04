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

The GitHub Actions workflow also supports manual inputs for a small verification run: `ticker`, `limit`, and `start_date`. Scheduled runs omit those inputs and process the SEC ticker list.

Each ticker is stored as:

```text
data/prices/A/AAPL.jsonl
data/prices/A/AAPL.meta.json
```

The meta file keeps `lastDate`, so incremental runs do not scan the JSONL tail. If `lastDate` is already yesterday or later, the ticker is skipped without calling the price providers.

For new tickers without a meta file, the default start date is dynamic: Yahoo first discovers `firstTradeDate` and then fetches the daily range from that date; Stooq omits `d1`. Records are still filtered to yesterday. Set `--start-date` or `STOCK_PRICE_START_DATE` only when you intentionally want to cap the initial backfill.
