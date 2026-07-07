# Stock Price Collector

Go-based US stock price collection for GitHub Actions.

The repository collects Nasdaq Screener stocks and then collects daily prices only for:

```text
SEC company_tickers ∩ data/nasdaq/screener/stocks.jsonl
```

The Nasdaq Screener workflow filters for United States mega/large/mid market cap stocks with strong_buy or buy recommendations before price collection runs.

## Structure

```text
data/
  universe/
    collectable_tickers.jsonl
    collectable_tickers.meta.json
    excluded_tickers.jsonl
  nasdaq/
    screener/
      stocks.jsonl
      stocks.meta.json
  state/
    update-universe.state.json
    collect-prices.state.json
  prices/
    AAPL/
      AAPL.jsonl
      AAPL.meta.json
  actions/
    AAPL/
      AAPL.actions.jsonl
scripts/
  update-universe/
    main.go
  collect-prices/
    main.go
.github/workflows/
  update-universe.yml
  collect-prices.yml
  collect-nasdaq-splits.yml
  collect-nasdaq-dividends.yml
  collect-nasdaq-screener.yml
```

## Update Legacy Market-Cap Universe

`update-universe` downloads the SEC ticker list, queries Yahoo Finance market cap, writes collectable tickers sorted by market cap descending, writes excluded tickers sorted by ticker, and updates meta. This legacy universe is still available with `collect-prices --target-source collectable-universe`, but it is no longer the default price target source.

```bash
go run ./scripts/update-universe \
  --min-market-cap 300000000 \
  --max-tickers 0 \
  --workers 4 \
  --sleep-ms 2000 \
  --batch-size 1000 \
  --max-runtime-minutes 330 \
  --graceful-stop-minutes 10 \
  --sec-user-agent "github-stock-collector your@email.com" \
  --output-dir data/universe
```

Outputs:

```text
data/universe/collectable_tickers.jsonl
data/universe/collectable_tickers.meta.json
data/universe/excluded_tickers.jsonl
```

Market cap is read from Yahoo quote data, with Yahoo fundamentals timeseries as a fallback when quote endpoints require crumb authentication. Missing, zero, below-threshold, symbol-not-found, request-failed, invalid, or non-equity-like tickers are written to `excluded_tickers.jsonl` with a reason.

Long universe updates are resumable. Each run stores progress in `data/state/update-universe.state.json` and accumulates partial universe output under `data/state/update-universe/`. When the cursor reaches the end, the final universe is written to `data/universe`.

## Collect Prices

`collect-prices` downloads the SEC ticker list, reads `data/nasdaq/screener/stocks.jsonl`, computes the intersection by normalized ticker key, and collects prices only for the final target list. Screener-only symbols that are not present in SEC `company_tickers.json` are excluded, and SEC tickers that are not present in the screener file are excluded. Daily price history and corporate actions are collected from Yahoo Finance Chart JSON only.

```bash
go run ./scripts/collect-prices \
  --data-dir data/prices \
  --target-source nasdaq-screener \
  --screener-file data/nasdaq/screener/stocks.jsonl \
  --allow-all-sec-tickers false \
  --start-date 2020-01-01 \
  --max-tickers 0 \
  --workers 4 \
  --sleep-ms 150 \
  --batch-size 500 \
  --max-runtime-minutes 330 \
  --graceful-stop-minutes 10 \
  --sec-user-agent "github-stock-collector your@email.com"
```

If the Nasdaq screener file is missing, the command fails with:

```text
nasdaq screener stocks.jsonl not found. Run collect-nasdaq-screener workflow first.
```

Use `--target-source collectable-universe --universe-file data/universe/collectable_tickers.jsonl` to run against the legacy Yahoo market-cap universe. Use `--allow-all-sec-tickers` only for emergency/manual runs that intentionally bypass the target filter.

Price collection is also resumable. Each run stores `data/state/collect-prices.state.json`, advances the cursor after each ticker, and exits successfully on partial completion when the batch size or runtime budget is reached.

## Price Storage

Each ticker is stored in its own directory:

```text
data/prices/AAPL/AAPL.jsonl
data/prices/AAPL/AAPL.meta.json
data/actions/AAPL/AAPL.actions.jsonl
```

Price records store raw Yahoo OHLC and adjusted OHLC. Yahoo provides `adjClose`; `adjOpen`, `adjHigh`, and `adjLow` are calculated with `adjClose / close`. Rows with invalid adjusted prices are skipped.

When a ticker is new, incomplete, stale, missing hashes, missing actions, or due for validation, the collector requests provider max history and rewrites that ticker from provider truth. Otherwise it fetches only `lastDate + 1 day` through yesterday and appends new rows. New splits or split-like discontinuities trigger a full rewrite.

## GitHub Actions

`update-universe.yml`:

- Runs a weekly cursor window at Sunday 21:00/23:00 UTC and Monday 01:00..15:00 UTC every 2 hours.
- With the default `batch_size=1000`, the weekly window can advance about 10,000 tickers.
- Supports manual `workflow_dispatch`.
- Uses `timeout-minutes: 350`.
- Commits `data/universe/*` and `data/state/*` only when files change.
- Commit message: `chore(data): update collectable ticker universe`.

`collect-prices.yml`:

- Runs Tue-Sat UTC 01:00..17:00 every 2 hours.
- With the default `batch_size=500`, the daily cursor window can advance about 4,500 tickers.
- Supports manual `workflow_dispatch`.
- Reads `data/nasdaq/screener/stocks.jsonl` by default.
- Uses `timeout-minutes: 350`.
- Commits `data/prices`, `data/actions`, and `data/state` only when files change.
- Commit message: `chore(data): append daily yahoo prices`.

Both workflows require `SEC_USER_AGENT` in the `STOCK_PRICE` GitHub environment. Example:

```text
github-stock-collector your@email.com
```

## Nasdaq JSONL Collectors

This repository also collects additional Nasdaq datasets:

- Split calendar
- Dividend calendar
- Screener stocks with mega/large/mid market cap and strong_buy/buy recommendations

Workflows:

- `collect-nasdaq-splits.yml`: runs daily at 21:10 UTC and writes `data/nasdaq/splits/splits.jsonl`.
- `collect-nasdaq-dividends.yml`: runs daily at 21:20 UTC and writes `data/nasdaq/dividends/dividends.jsonl`.
- `collect-nasdaq-screener.yml`: runs daily at 21:30 UTC and writes `data/nasdaq/screener/stocks.jsonl`.

Each workflow supports `workflow_dispatch`. The dividend workflow accepts a single `date` or uses the default lookback/lookahead window. The screener workflow accepts `limit`, `marketcap`, `recommendation`, and `country` inputs.

Nasdaq output:

```text
data/nasdaq/splits/splits.jsonl
data/nasdaq/splits/splits.meta.json
data/nasdaq/splits/snapshots/YYYY-MM-DD.jsonl

data/nasdaq/dividends/dividends.jsonl
data/nasdaq/dividends/dividends.meta.json
data/nasdaq/dividends/by-date/YYYY/MM/YYYY-MM-DD.jsonl

data/nasdaq/screener/stocks.jsonl
data/nasdaq/screener/stocks.meta.json
data/nasdaq/screener/snapshots/YYYY-MM-DD.jsonl
```
