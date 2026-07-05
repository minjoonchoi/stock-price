package collector

import (
	"context"
	"time"
)

const (
	SourceYahoo = "yahoo"

	AdjustmentVersionYahooChartV1 = "yahoo-chart-v1"

	ActionTypeSplit    = "split"
	ActionTypeDividend = "dividend"
)

type Company struct {
	CIK    int
	Ticker string
	Title  string
}

type PriceRecord struct {
	Date              string  `json:"date"`
	Ticker            string  `json:"ticker"`
	Open              float64 `json:"open"`
	High              float64 `json:"high"`
	Low               float64 `json:"low"`
	Close             float64 `json:"close"`
	AdjOpen           float64 `json:"adjOpen"`
	AdjHigh           float64 `json:"adjHigh"`
	AdjLow            float64 `json:"adjLow"`
	AdjClose          float64 `json:"adjClose"`
	Volume            int64   `json:"volume"`
	Source            string  `json:"source"`
	AdjustmentVersion string  `json:"adjustmentVersion"`
}

type Dividend struct {
	Date   string  `json:"date"`
	Amount float64 `json:"amount"`
}

type Split struct {
	Date        string  `json:"date"`
	Numerator   float64 `json:"numerator"`
	Denominator float64 `json:"denominator"`
	Ratio       float64 `json:"ratio"`
}

type CorporateAction struct {
	Date        string  `json:"date"`
	Ticker      string  `json:"ticker"`
	Type        string  `json:"type"`
	Numerator   float64 `json:"numerator,omitempty"`
	Denominator float64 `json:"denominator,omitempty"`
	Ratio       float64 `json:"ratio,omitempty"`
	Amount      float64 `json:"amount,omitempty"`
	Source      string  `json:"source"`
}

type PriceHistory struct {
	Records   []PriceRecord
	Dividends []Dividend
	Splits    []Split
}

type PriceProvider interface {
	FetchHistory(ctx context.Context, ticker string, start time.Time, end time.Time) (PriceHistory, error)
}

type Meta struct {
	Ticker                  string `json:"ticker"`
	Source                  string `json:"source"`
	FirstDate               string `json:"firstDate"`
	LastDate                string `json:"lastDate"`
	Records                 int    `json:"records"`
	BackfillCompleted       bool   `json:"backfillCompleted"`
	AdjustedSeriesValidated bool   `json:"adjustedSeriesValidated"`
	LastCorporateActionDate string `json:"lastCorporateActionDate"`
	LastSplitDate           string `json:"lastSplitDate"`
	CorporateActionHash     string `json:"corporateActionHash"`
	PriceDataHash           string `json:"priceDataHash"`
	LastFullValidationAt    string `json:"lastFullValidationAt"`
	UpdatedAt               string `json:"updatedAt"`
}
