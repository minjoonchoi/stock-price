package nasdaq

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"time"
)

const DefaultBaseURL = "https://api.nasdaq.com"

type ClientConfig struct {
	BaseURL      string
	HTTPClient   *http.Client
	Sleep        func(time.Duration)
	CurlFallback func(context.Context, string) ([]byte, error)
}

type Client struct {
	baseURL      string
	httpClient   *http.Client
	sleep        func(time.Duration)
	curlFallback func(context.Context, string) ([]byte, error)
}

func NewHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout}
}

func NewClient(config ClientConfig) *Client {
	baseURL := strings.TrimRight(config.BaseURL, "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = NewHTTPClient(30 * time.Second)
	}
	sleep := config.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	return &Client{baseURL: baseURL, httpClient: httpClient, sleep: sleep, curlFallback: config.CurlFallback}
}

func (c *Client) Get(ctx context.Context, path string, query url.Values) ([]byte, error) {
	backoffs := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}
	var lastErr error
	for attempt := 0; attempt <= len(backoffs); attempt++ {
		body, retry, err := c.getOnce(ctx, path, query)
		if err == nil {
			return body, nil
		}
		lastErr = err
		if !retry || attempt == len(backoffs) {
			break
		}
		c.sleep(backoffs[attempt])
	}
	if c.curlFallback != nil {
		requestURL, err := c.requestURL(path, query)
		if err != nil {
			return nil, err
		}
		body, fallbackErr := c.curlFallback(ctx, requestURL)
		if fallbackErr == nil {
			return body, nil
		}
		return nil, fmt.Errorf("%w; curl fallback failed: %v", lastErr, fallbackErr)
	}
	return nil, lastErr
}

func (c *Client) getOnce(ctx context.Context, path string, query url.Values) ([]byte, bool, error) {
	requestURL, err := c.requestURL(path, query)
	if err != nil {
		return nil, false, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, false, err
	}
	request.Header.Set("Accept", "application/json, text/plain, */*")
	request.Header.Set("Accept-Language", "en-US,en;q=0.9")
	request.Header.Set("Cache-Control", "no-cache")
	request.Header.Set("Pragma", "no-cache")
	request.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36")
	request.Header.Set("Origin", "https://www.nasdaq.com")
	request.Header.Set("Referer", "https://www.nasdaq.com/")
	request.Header.Set("Sec-Fetch-Dest", "empty")
	request.Header.Set("Sec-Fetch-Mode", "cors")
	request.Header.Set("Sec-Fetch-Site", "same-site")

	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, true, err
	}
	defer response.Body.Close()
	body, readErr := io.ReadAll(response.Body)
	if readErr != nil {
		return nil, true, readErr
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return body, false, nil
	}
	return nil, isRetriableStatus(response.StatusCode), fmt.Errorf("nasdaq request %s failed: %s: %s", request.URL.Path, response.Status, strings.TrimSpace(string(body)))
}

func (c *Client) requestURL(path string, query url.Values) (string, error) {
	endpoint, err := url.Parse(c.baseURL + "/" + strings.TrimLeft(path, "/"))
	if err != nil {
		return "", err
	}
	if len(query) > 0 {
		endpoint.RawQuery = query.Encode()
	}
	return endpoint.String(), nil
}

func DefaultCurlFallback(ctx context.Context, requestURL string) ([]byte, error) {
	args := []string{
		"-sS",
		"--compressed",
		"--fail",
		"--max-time", "30",
		"-H", "Accept: application/json, text/plain, */*",
		"-H", "Accept-Language: en-US,en;q=0.9",
		"-H", "Cache-Control: no-cache",
		"-H", "Pragma: no-cache",
		"-H", "User-Agent: Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36",
		"-H", "Origin: https://www.nasdaq.com",
		"-H", "Referer: https://www.nasdaq.com/",
		"-H", "Sec-Fetch-Dest: empty",
		"-H", "Sec-Fetch-Mode: cors",
		"-H", "Sec-Fetch-Site: same-site",
		requestURL,
	}
	command := exec.CommandContext(ctx, "curl", args...)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	body, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return body, nil
}

func isRetriableStatus(status int) bool {
	switch status {
	case http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}
