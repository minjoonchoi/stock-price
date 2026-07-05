package collector

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestStooqProviderFetchHistoryParsesDailyCSV(t *testing.T) {
	var gotPath string
	var gotQuery string
	var gotUserAgent string
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotUserAgent = r.Header.Get("User-Agent")
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body: io.NopCloser(bytes.NewBufferString(`Date,Open,High,Low,Close,Volume
2026-07-01,293.44,296.59,289.20,294.38,50164200
2026-07-02,294.12,309.42,293.68,308.63,75352800
`)),
			Request: r,
		}, nil
	})}

	provider := NewStooqProvider(StooqProviderConfig{
		BaseURL:   "https://stooq.com",
		UserAgent: "github-stock-collector test@example.com",
		Client:    httpClient,
	})

	history, err := provider.FetchHistory(context.Background(), "AAPL", mustDate(t, "2026-07-01"), mustDate(t, "2026-07-02"))
	if err != nil {
		t.Fatalf("FetchHistory() error = %v", err)
	}

	if gotPath != "/q/d/l/" {
		t.Fatalf("path = %q", gotPath)
	}
	assertQueryContains(t, gotQuery, "s=aapl.us")
	assertQueryContains(t, gotQuery, "i=d")
	assertQueryContains(t, gotQuery, "d1=20260701")
	assertQueryContains(t, gotQuery, "d2=20260702")
	if gotUserAgent != "github-stock-collector test@example.com" {
		t.Fatalf("User-Agent = %q", gotUserAgent)
	}
	if len(history.Records) != 2 {
		t.Fatalf("expected 2 records, got %+v", history.Records)
	}
	first := history.Records[0]
	if first.Date != "2026-07-01" || first.Ticker != "AAPL" || first.Close != 294.38 || first.AdjClose != 294.38 || first.Volume != 50164200 || first.Source != SourceStooq {
		t.Fatalf("unexpected first record: %+v", first)
	}
}

func TestStooqProviderFetchHistoryWithZeroStartOmitsD1(t *testing.T) {
	var gotQuery string
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotQuery = r.URL.RawQuery
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body: io.NopCloser(bytes.NewBufferString(`Date,Open,High,Low,Close,Volume
2026-07-02,294.12,309.42,293.68,308.63,75352800
`)),
			Request: r,
		}, nil
	})}
	provider := NewStooqProvider(StooqProviderConfig{
		BaseURL: "https://stooq.com",
		Client:  httpClient,
	})

	_, err := provider.FetchHistory(context.Background(), "AAPL", time.Time{}, mustDate(t, "2026-07-02"))
	if err != nil {
		t.Fatalf("FetchHistory() error = %v", err)
	}

	if strings.Contains(gotQuery, "d1=") {
		t.Fatalf("dynamic earliest query should omit d1: %q", gotQuery)
	}
	assertQueryContains(t, gotQuery, "d2=20260702")
}

func TestStooqProviderSolvesBrowserVerificationChallengeThenRetriesCSV(t *testing.T) {
	var requests []string
	var verifyBody string
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch len(requests) {
		case 1:
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
				Body: io.NopCloser(bytes.NewBufferString(`<!DOCTYPE html><html><body><script>
(async()=>{const c="abc",d=1,t="0".repeat(d),e=new TextEncoder;let n=0;while(1){const h=await crypto.subtle.digest("SHA-256",e.encode(c+n)),x=Array.from(new Uint8Array(h)).map(b=>b.toString(16).padStart(2,"0")).join("");if(x.startsWith(t))break;n++}const r=await fetch("/__verify",{method:"POST",headers:{"Content-Type":"application/x-www-form-urlencoded"},body:"c="+encodeURIComponent(c)+"&n="+n,credentials:"same-origin"});if(r.ok)location.reload()})();
</script></body></html>`)),
				Request: r,
			}, nil
		case 2:
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read verify body: %v", err)
			}
			verifyBody = string(body)
			header := make(http.Header)
			header.Set("Set-Cookie", "stooq_verified=1; Path=/")
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     header,
				Body:       io.NopCloser(bytes.NewBufferString(`OK`)),
				Request:    r,
			}, nil
		case 3:
			if got := r.Header.Get("Cookie"); !strings.Contains(got, "stooq_verified=1") {
				t.Fatalf("retry request missing verification cookie: %q", got)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     make(http.Header),
				Body: io.NopCloser(bytes.NewBufferString(`Date,Open,High,Low,Close,Volume
2026-07-02,10,11,9,10.5,1234
`)),
				Request: r,
			}, nil
		default:
			t.Fatalf("unexpected request %d to %s", len(requests), r.URL.String())
			return nil, nil
		}
	})}

	provider := NewStooqProvider(StooqProviderConfig{
		BaseURL: "https://stooq.com",
		Client:  httpClient,
	})

	history, err := provider.FetchHistory(context.Background(), "KMDA", mustDate(t, "2026-07-01"), mustDate(t, "2026-07-02"))
	if err != nil {
		t.Fatalf("FetchHistory() error = %v", err)
	}

	if strings.Join(requests, ",") != "GET /q/d/l/,POST /__verify,GET /q/d/l/" {
		t.Fatalf("unexpected request sequence: %+v", requests)
	}
	if !strings.Contains(verifyBody, "c=abc") || !strings.Contains(verifyBody, "n=") {
		t.Fatalf("unexpected verify body: %q", verifyBody)
	}
	if len(history.Records) != 1 || history.Records[0].Ticker != "KMDA" || history.Records[0].Source != SourceStooq {
		t.Fatalf("unexpected history: %+v", history)
	}
}

func TestStooqProviderReturnsErrorForChallengeHTML(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewBufferString(`<!DOCTYPE html><html><body>verify your browser</body></html>`)),
			Request:    r,
		}, nil
	})}
	provider := NewStooqProvider(StooqProviderConfig{
		BaseURL: "https://stooq.com",
		Client:  httpClient,
	})

	_, err := provider.FetchHistory(context.Background(), "AAC", mustDate(t, "2026-07-01"), mustDate(t, "2026-07-02"))
	if err == nil {
		t.Fatal("expected challenge HTML to return an error")
	}
}

func TestStooqProviderReturnsClearErrorForAccessDenied(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewBufferString(`Access denied`)),
			Request:    r,
		}, nil
	})}
	provider := NewStooqProvider(StooqProviderConfig{
		BaseURL: "https://stooq.com",
		Client:  httpClient,
	})

	_, err := provider.FetchHistory(context.Background(), "KMDA", mustDate(t, "2026-07-01"), mustDate(t, "2026-07-02"))
	if err == nil {
		t.Fatal("expected access denied error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "access denied") {
		t.Fatalf("error = %v", err)
	}
}
