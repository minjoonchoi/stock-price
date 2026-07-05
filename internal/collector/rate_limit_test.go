package collector

import (
	"bytes"
	"io"
	"net/http"
	"testing"
	"time"
)

func TestRateLimitedTransportSleepsBeforeSecondRequest(t *testing.T) {
	var slept []time.Duration
	now := time.Date(2026, 7, 5, 0, 0, 0, 0, time.UTC)
	var requests int
	transport := &RateLimitedTransport{
		Base: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			requests++
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     make(http.Header),
				Body:       io.NopCloser(bytes.NewBufferString(`OK`)),
				Request:    r,
			}, nil
		}),
		Delay: 2 * time.Second,
		now: func() time.Time {
			return now
		},
		sleep: func(d time.Duration) {
			slept = append(slept, d)
			now = now.Add(d)
		},
	}
	client := &http.Client{Transport: transport}

	for i := 0; i < 2; i++ {
		response, err := client.Get("https://example.test")
		if err != nil {
			t.Fatalf("request %d error = %v", i+1, err)
		}
		_ = response.Body.Close()
	}

	if requests != 2 {
		t.Fatalf("requests = %d", requests)
	}
	if len(slept) != 1 || slept[0] != 2*time.Second {
		t.Fatalf("slept = %+v", slept)
	}
}

func TestRateLimitedTransportDoesNotSleepWhenDelayIsZero(t *testing.T) {
	var slept []time.Duration
	transport := &RateLimitedTransport{
		Base: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     make(http.Header),
				Body:       io.NopCloser(bytes.NewBufferString(`OK`)),
				Request:    r,
			}, nil
		}),
		Delay: 0,
		sleep: func(d time.Duration) {
			slept = append(slept, d)
		},
	}
	client := &http.Client{Transport: transport}

	for i := 0; i < 2; i++ {
		response, err := client.Get("https://example.test")
		if err != nil {
			t.Fatalf("request %d error = %v", i+1, err)
		}
		_ = response.Body.Close()
	}

	if len(slept) != 0 {
		t.Fatalf("slept = %+v", slept)
	}
}
