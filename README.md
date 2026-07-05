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
  prices/
    AAPL/
      AAPL.jsonl
      AAPL.meta.json
  actions/
    A/
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
  --sleep-ms 150 \
  --sec-user-agent "github-stock-collector your@email.com" \
  --output-dir data/universe
```

Outputs:

```text
data/universe/collectable_tickers.jsonl
data/universe/collectable_tickers.meta.json
data/universe/excluded_tickers.jsonl
```

Market cap is read from Yahoo `marketCap`. Missing, zero, below-threshold, symbol-not-found, request-failed, invalid, or non-equity-like tickers are written to `excluded_tickers.jsonl` with a reason.

## Collect Prices

`collect-prices` downloads the SEC ticker list, reads `data/universe/collectable_tickers.jsonl`, computes the intersection, and collects prices only for the final target list.

```bash
go run ./scripts/collect-prices \
  --data-dir data/prices \
  --universe-file data/universe/collectable_tickers.jsonl \
  --allow-all-sec-tickers false \
  --start-date 2020-01-01 \
  --max-tickers 0 \
  --workers 4 \
  --sleep-ms 150 \
  --sec-user-agent "github-stock-collector your@email.com"
```

If the universe file is missing, the command fails with:

```text
collectable_tickers.jsonl not found. Run update-universe workflow first.
```

Use `--allow-all-sec-tickers` only for emergency/manual runs that intentionally bypass the market-cap filter.

## Price Storage

Each ticker is stored in its own directory:

```text
data/prices/AAPL/AAPL.jsonl
data/prices/AAPL/AAPL.meta.json
data/actions/A/AAPL.actions.jsonl
```

Price records store raw Yahoo OHLC and adjusted OHLC. Yahoo provides `adjClose`; `adjOpen`, `adjHigh`, and `adjLow` are calculated with `adjClose / close`. Rows with invalid adjusted prices are skipped.

When a ticker is new, incomplete, stale, missing hashes, missing actions, or due for validation, the collector requests provider max history and rewrites that ticker from provider truth. Otherwise it fetches only `lastDate + 1 day` through yesterday and appends new rows. New splits or split-like discontinuities trigger a full rewrite.

## GitHub Actions

`update-universe.yml`:

- Runs weekly at Sunday 21:00 UTC, which is Monday 06:00 KST.
- Supports manual `workflow_dispatch`.
- Commits `data/universe/*` only when files change.
- Commit message: `chore(data): update collectable ticker universe`.

`collect-prices.yml`:

- Runs daily at 22:00 UTC, which is 07:00 KST.
- Supports manual `workflow_dispatch`.
- Reads `data/universe/collectable_tickers.jsonl` by default.
- Commits `data/prices` and `data/actions` only when files change.
- Commit message: `chore(data): append daily yahoo prices`.

Both workflows require `SEC_USER_AGENT` in the `STOCK_PRICE` GitHub environment. Example:

```text
github-stock-collector your@email.com
```
