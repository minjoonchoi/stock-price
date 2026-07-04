package collector

import (
	"context"
	"time"
)

const (
	SourceYahoo = "yahoo"
	SourceStooq = "stooq"
)

type Company struct {
	CIK    int
	Ticker string
	Title  string
}

type PriceRecord struct {
	Date     string  `json:"date"`
	Ticker   string  `json:"ticker"`
	Open     float64 `json:"open"`
	High     float64 `json:"high"`
	Low      float64 `json:"low"`
	Close    float64 `json:"close"`
	AdjClose float64 `json:"adjClose"`
	Volume   int64   `json:"volume"`
	Source   string  `json:"source"`
}

type Dividend struct {
	Date   string  `json:"date"`
	Amount float64 `json:"amount"`
}

type Split struct {
	Date        string  `json:"date"`
	Numerator   float64 `json:"numerator"`
	Denominator float64 `json:"denominator"`
	Ratio       string  `json:"ratio"`
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
	LastDate  string `json:"lastDate"`
	Records   int    `json:"records"`
	UpdatedAt string `json:"updatedAt"`
	Source    string `json:"source"`
}
