package nasdaq

const (
	SourceNasdaq = "nasdaq"

	APISplits    = "calendar/splits"
	APIDividends = "calendar/dividends"
	APIScreener  = "screener/stocks"
)

type SplitRecord struct {
	Symbol        string   `json:"symbol"`
	Name          string   `json:"name"`
	RatioRaw      string   `json:"ratioRaw"`
	Numerator     *float64 `json:"numerator"`
	Denominator   *float64 `json:"denominator"`
	Ratio         *float64 `json:"ratio"`
	ParseError    *string  `json:"parseError,omitempty"`
	ExecutionDate *string  `json:"executionDate"`
	Source        string   `json:"source"`
	API           string   `json:"api"`
	AsOf          string   `json:"asOf"`
	CollectedAt   string   `json:"collectedAt"`
}

type DividendRecord struct {
	Symbol                  string   `json:"symbol"`
	CompanyName             string   `json:"companyName"`
	ExDividendDate          string   `json:"exDividendDate"`
	PaymentDate             *string  `json:"paymentDate"`
	RecordDate              *string  `json:"recordDate"`
	DividendRate            *float64 `json:"dividendRate"`
	IndicatedAnnualDividend *float64 `json:"indicatedAnnualDividend"`
	AnnouncementDate        *string  `json:"announcementDate"`
	Source                  string   `json:"source"`
	API                     string   `json:"api"`
	CalendarDate            string   `json:"calendarDate"`
	AsOf                    string   `json:"asOf"`
	CollectedAt             string   `json:"collectedAt"`
}

type ScreenerRecord struct {
	Symbol               string   `json:"symbol"`
	Name                 string   `json:"name"`
	LastSale             *float64 `json:"lastSale"`
	NetChange            *float64 `json:"netChange"`
	PctChange            *float64 `json:"pctChange"`
	MarketCap            *int64   `json:"marketCap"`
	URL                  string   `json:"url"`
	Country              string   `json:"country"`
	MarketCapFilter      string   `json:"marketCapFilter"`
	RecommendationFilter string   `json:"recommendationFilter"`
	Source               string   `json:"source"`
	API                  string   `json:"api"`
	CollectedAt          string   `json:"collectedAt"`
}

type ScreenerOptions struct {
	Limit          int
	MarketCap      string
	Recommendation string
	Country        string
	TableOnly      bool
}

type Meta map[string]any
