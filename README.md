# Stock Price Collector

Go-based US stock price collection for GitHub Actions.

The repository keeps a market-cap filtered ticker universe and then collects daily prices only for:

```text
SEC company_tickers ∩ data/universe/collectable_tickers.jsonl
```

This avoids fetching prices for every SEC ticker on every run.

## Structure

```text
data/
  universe/
    collectable_tickers.jsonl
    collectable_tickers.meta.json
    excluded_tickers.jsonl
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
```

## Update Universe

`update-universe` downloads the SEC ticker list, queries Yahoo Finance market cap, writes collectable tickers sorted by market cap descending, writes excluded tickers sorted by ticker, and updates meta.

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

`collect-prices` downloads the SEC ticker list, reads `data/universe/collectable_tickers.jsonl`, computes the intersection, and collects prices only for the final target list. Daily price history and corporate actions are collected from Yahoo Finance Chart JSON only.

```bash
go run ./scripts/collect-prices \
  --data-dir data/prices \
  --universe-file data/universe/collectable_tickers.jsonl \
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

If the universe file is missing, the command fails with:

```text
collectable_tickers.jsonl not found. Run update-universe workflow first.
```

Use `--allow-all-sec-tickers` only for emergency/manual runs that intentionally bypass the market-cap filter.

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
- Reads `data/universe/collectable_tickers.jsonl` by default.
- Uses `timeout-minutes: 350`.
- Commits `data/prices`, `data/actions`, and `data/state` only when files change.
- Commit message: `chore(data): append daily yahoo prices`.

Both workflows require `SEC_USER_AGENT` in the `STOCK_PRICE` GitHub environment. Example:

```text
github-stock-collector your@email.com
```
