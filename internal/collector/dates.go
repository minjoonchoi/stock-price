package collector

import "time"

const dateLayout = "2006-01-02"

func ParseDate(value string) (time.Time, error) {
	return time.ParseInLocation(dateLayout, value, time.UTC)
}

func FormatDate(value time.Time) string {
	return value.UTC().Format(dateLayout)
}

func startOfUTCDate(value time.Time) time.Time {
	utc := value.UTC()
	return time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
}

func yesterday(clock func() time.Time) time.Time {
	return startOfUTCDate(clock()).AddDate(0, 0, -1)
}
