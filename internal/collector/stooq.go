package collector

import (
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const DefaultStooqBaseURL = "https://stooq.com"

type StooqProviderConfig struct {
	BaseURL   string
	UserAgent string
	Client    *http.Client
}

type StooqProvider struct {
	baseURL   string
	userAgent string
	client    *http.Client
}

func NewStooqProvider(config StooqProviderConfig) *StooqProvider {
	baseURL := strings.TrimRight(config.BaseURL, "/")
	if baseURL == "" {
		baseURL = DefaultStooqBaseURL
	}
	client := config.Client
	if client == nil {
		client = http.DefaultClient
	}
	if client.Jar == nil {
		jar, err := cookiejar.New(nil)
		if err == nil {
			copied := *client
			copied.Jar = jar
			client = &copied
		}
	}
	return &StooqProvider{
		baseURL:   baseURL,
		userAgent: config.UserAgent,
		client:    client,
	}
}

func (p *StooqProvider) FetchHistory(ctx context.Context, ticker string, start time.Time, end time.Time) (PriceHistory, error) {
	requestURL, err := url.Parse(p.baseURL + "/q/d/l/")
	if err != nil {
		return PriceHistory{}, err
	}

	params := requestURL.Query()
	params.Set("s", StooqSymbol(ticker))
	params.Set("i", "d")
	if !start.IsZero() {
		params.Set("d1", formatStooqDate(start))
	}
	params.Set("d2", formatStooqDate(end))
	requestURL.RawQuery = params.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return PriceHistory{}, err
	}
	if p.userAgent != "" {
		request.Header.Set("User-Agent", p.userAgent)
	}

	response, err := p.client.Do(request)
	if err != nil {
		return PriceHistory{}, err
	}
	body, err := readResponseBody(response)
	if err != nil {
		return PriceHistory{}, err
	}

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return PriceHistory{}, fmt.Errorf("Stooq request for %s failed: %s", ticker, response.Status)
	}
	if challenge, ok := parseStooqVerificationChallenge(body); ok {
		if err := p.solveVerificationChallenge(ctx, challenge); err != nil {
			return PriceHistory{}, fmt.Errorf("Stooq request for %s failed: %w", ticker, err)
		}
		response, err = p.client.Do(request.Clone(ctx))
		if err != nil {
			return PriceHistory{}, err
		}
		body, err = readResponseBody(response)
		if err != nil {
			return PriceHistory{}, err
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return PriceHistory{}, fmt.Errorf("Stooq request for %s failed after verification: %s", ticker, response.Status)
		}
	}

	records, err := parseStooqCSV(strings.NewReader(body), NormalizeTicker(ticker))
	if err != nil {
		return PriceHistory{}, fmt.Errorf("Stooq request for %s failed: %w", ticker, err)
	}
	if len(records) == 0 {
		return PriceHistory{}, fmt.Errorf("Stooq request for %s failed: no data", ticker)
	}
	return PriceHistory{Records: records}, nil
}

func readResponseBody(response *http.Response) (string, error) {
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

type stooqVerificationChallenge struct {
	Token  string
	Digits int
	Path   string
}

var (
	stooqTokenPattern  = regexp.MustCompile(`const c="([^"]+)"`)
	stooqDigitsPattern = regexp.MustCompile(`,d=([0-9]+),t=`)
	stooqPathPattern   = regexp.MustCompile(`fetch\("([^"]+)"`)
)

func parseStooqVerificationChallenge(body string) (stooqVerificationChallenge, bool) {
	if !strings.Contains(body, "crypto.subtle.digest") || !strings.Contains(body, "/__verify") {
		return stooqVerificationChallenge{}, false
	}

	tokenMatch := stooqTokenPattern.FindStringSubmatch(body)
	digitsMatch := stooqDigitsPattern.FindStringSubmatch(body)
	pathMatch := stooqPathPattern.FindStringSubmatch(body)
	if len(tokenMatch) != 2 || len(digitsMatch) != 2 || len(pathMatch) != 2 {
		return stooqVerificationChallenge{}, false
	}

	digits, err := strconv.Atoi(digitsMatch[1])
	if err != nil {
		return stooqVerificationChallenge{}, false
	}
	return stooqVerificationChallenge{
		Token:  tokenMatch[1],
		Digits: digits,
		Path:   pathMatch[1],
	}, true
}

func (p *StooqProvider) solveVerificationChallenge(ctx context.Context, challenge stooqVerificationChallenge) error {
	nonce, err := solveSHA256Prefix(challenge.Token, challenge.Digits)
	if err != nil {
		return err
	}
	verifyURL, err := url.Parse(p.baseURL + challenge.Path)
	if err != nil {
		return err
	}

	form := url.Values{}
	form.Set("c", challenge.Token)
	form.Set("n", strconv.Itoa(nonce))
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, verifyURL.String(), strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if p.userAgent != "" {
		request.Header.Set("User-Agent", p.userAgent)
	}

	response, err := p.client.Do(request)
	if err != nil {
		return err
	}
	_, err = readResponseBody(response)
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("verification failed: %s", response.Status)
	}
	return nil
}

func solveSHA256Prefix(token string, digits int) (int, error) {
	if digits < 0 || digits > 8 {
		return 0, fmt.Errorf("unsupported verification difficulty %d", digits)
	}
	prefix := strings.Repeat("0", digits)
	for nonce := 0; nonce < 10_000_000; nonce++ {
		sum := sha256.Sum256([]byte(token + strconv.Itoa(nonce)))
		if strings.HasPrefix(hex.EncodeToString(sum[:]), prefix) {
			return nonce, nil
		}
	}
	return 0, errors.New("verification nonce not found")
}

func StooqSymbol(ticker string) string {
	return strings.ToLower(strings.ReplaceAll(NormalizeTicker(ticker), ".", "-")) + ".us"
}

func formatStooqDate(value time.Time) string {
	return value.UTC().Format("20060102")
}

func parseStooqCSV(reader io.Reader, ticker string) ([]PriceRecord, error) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(string(data))
	lower := strings.ToLower(trimmed)
	if strings.Contains(lower, "access denied") {
		return nil, errors.New("access denied")
	}
	if strings.HasPrefix(lower, "<!doctype") || strings.HasPrefix(lower, "<html") {
		return nil, errors.New("unexpected non-csv response")
	}

	csvReader := csv.NewReader(strings.NewReader(trimmed))
	csvReader.FieldsPerRecord = -1

	rows, err := csvReader.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	if !isStooqHeader(rows[0]) {
		return nil, fmt.Errorf("unexpected csv header %q", strings.Join(rows[0], ","))
	}

	records := make([]PriceRecord, 0, len(rows)-1)
	for _, row := range rows[1:] {
		if len(row) < 6 {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(row[1]), "N/D") {
			continue
		}

		open, err := strconv.ParseFloat(strings.TrimSpace(row[1]), 64)
		if err != nil {
			return nil, fmt.Errorf("parse open for %s: %w", row[0], err)
		}
		high, err := strconv.ParseFloat(strings.TrimSpace(row[2]), 64)
		if err != nil {
			return nil, fmt.Errorf("parse high for %s: %w", row[0], err)
		}
		low, err := strconv.ParseFloat(strings.TrimSpace(row[3]), 64)
		if err != nil {
			return nil, fmt.Errorf("parse low for %s: %w", row[0], err)
		}
		closePrice, err := strconv.ParseFloat(strings.TrimSpace(row[4]), 64)
		if err != nil {
			return nil, fmt.Errorf("parse close for %s: %w", row[0], err)
		}
		volume, err := strconv.ParseInt(strings.TrimSpace(row[5]), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse volume for %s: %w", row[0], err)
		}

		date, err := ParseDate(strings.TrimSpace(row[0]))
		if err != nil {
			return nil, fmt.Errorf("parse date %q: %w", row[0], err)
		}

		records = append(records, PriceRecord{
			Date:              FormatDate(date),
			Ticker:            ticker,
			Open:              open,
			High:              high,
			Low:               low,
			Close:             closePrice,
			AdjOpen:           open,
			AdjHigh:           high,
			AdjLow:            low,
			AdjClose:          closePrice,
			Volume:            volume,
			Source:            SourceStooq,
			AdjustmentVersion: AdjustmentVersionStooqRawV1,
		})
	}

	sort.SliceStable(records, func(i, j int) bool {
		return records[i].Date < records[j].Date
	})
	return records, nil
}

func isStooqHeader(row []string) bool {
	if len(row) < 6 {
		return false
	}
	expected := []string{"date", "open", "high", "low", "close", "volume"}
	for i, value := range expected {
		if strings.ToLower(strings.TrimSpace(row[i])) != value {
			return false
		}
	}
	return true
}
