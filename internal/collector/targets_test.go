package collector

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadNasdaqScreenerPriceTargetsIntersectsWithSECAndNormalizesSymbols(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "stocks.jsonl")
	raw := []byte(`
{"symbol":"aapl","name":"Apple from Nasdaq","marketCap":3500000000000,"recommendationFilter":"strong_buy|buy","source":"nasdaq"}
{"symbol":"BRK/B","name":"Berkshire from Nasdaq","marketCap":900000000000,"recommendationFilter":"buy","source":"nasdaq"}
{"symbol":" ms ft ","name":"Microsoft from Nasdaq","marketCap":3100000000000,"recommendationFilter":"strong_buy","source":"nasdaq"}
{"symbol":"NOTSEC","name":"Not in SEC","marketCap":1000000000,"recommendationFilter":"buy","source":"nasdaq"}
`)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	companies := []Company{
		{Ticker: "SECONLY", CIK: 1, Title: "SEC Only Inc."},
		{Ticker: "brk.b", CIK: 1067983, Title: "Berkshire Hathaway Inc."},
		{Ticker: "AAPL", CIK: 320193, Title: "Apple Inc."},
		{Ticker: "MSFT", CIK: 789019, Title: "Microsoft Corp."},
	}

	targets, filter, err := LoadNasdaqScreenerPriceTargets(path, companies)
	if err != nil {
		t.Fatalf("LoadNasdaqScreenerPriceTargets() error = %v", err)
	}

	gotTickers := []string{targets[0].SECTicker, targets[1].SECTicker, targets[2].SECTicker}
	wantTickers := []string{"AAPL", "BRK.B", "MSFT"}
	if !reflect.DeepEqual(gotTickers, wantTickers) {
		t.Fatalf("target SEC tickers = %+v, want %+v", gotTickers, wantTickers)
	}
	if targets[1].NormalizedKey != "BRK-B" || targets[1].ScreenerSymbol != "BRK/B" {
		t.Fatalf("BRK target = %+v", targets[1])
	}
	if targets[2].NormalizedKey != "MSFT" || targets[2].ScreenerSymbol != "ms ft" {
		t.Fatalf("MSFT target = %+v", targets[2])
	}
	if targets[0].CIK != "0000320193" || targets[0].Title != "Apple Inc." || targets[0].ScreenerName != "Apple from Nasdaq" || targets[0].Recommendation != "strong_buy|buy" || targets[0].Source != SourceNasdaqScreener {
		t.Fatalf("AAPL target = %+v", targets[0])
	}
	if targets[0].MarketCap == nil || *targets[0].MarketCap != 3500000000000 {
		t.Fatalf("AAPL market cap = %v", targets[0].MarketCap)
	}
	if filter.SECTickersTotal != 4 || filter.UniverseTickersTotal != 4 || filter.FinalTargetTickers != 3 || filter.ExcludedByUniverseFilter != 1 {
		t.Fatalf("filter = %+v", filter)
	}
}

func TestPriceTargetNormalizedKeyCandidatesUseDashedSymbolAliases(t *testing.T) {
	cases := map[string][]string{
		" BRK.B ": {"BRK.B", "BRK-B"},
		"brk/b":   {"BRK/B", "BRK-B"},
		" ms ft ": {"MSFT"},
	}
	for input, want := range cases {
		got := PriceTargetKeyCandidates(input)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("PriceTargetKeyCandidates(%q) = %+v, want %+v", input, got, want)
		}
	}
}
