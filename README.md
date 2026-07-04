# Stock Price Collector

Go-based daily US stock price collection for GitHub Actions.

The collector downloads the SEC company ticker list, fetches daily price history from Yahoo Finance Chart JSON, and stores append-only JSONL files by ticker under `data/prices`.

## Usage

```bash
SEC_USER_AGENT="github-stock-collector your@email.com" \
go run ./scripts/collect-prices --start-date 1970-01-01 --data-dir data/prices
```

Collect only selected tickers while testing:

```bash
go run ./scripts/collect-prices \
  --user-agent "github-stock-collector your@email.com" \
  --start-date 2026-01-01 \
  --ticker AAPL,MSFT
```

Each ticker is stored as:

```text
data/prices/A/AAPL.jsonl
data/prices/A/AAPL.meta.json
```

The meta file keeps `lastDate`, so incremental runs do not scan the JSONL tail. If `lastDate` is already yesterday or later, the ticker is skipped without calling Yahoo.
