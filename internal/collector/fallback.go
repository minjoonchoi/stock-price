package collector

import (
	"context"
	"errors"
	"strings"
	"time"
)

type FallbackProvider struct {
	providers []PriceProvider
}

func NewFallbackProvider(providers ...PriceProvider) *FallbackProvider {
	filtered := make([]PriceProvider, 0, len(providers))
	for _, provider := range providers {
		if provider != nil {
			filtered = append(filtered, provider)
		}
	}
	return &FallbackProvider{providers: filtered}
}

func (p *FallbackProvider) FetchHistory(ctx context.Context, ticker string, start time.Time, end time.Time) (PriceHistory, error) {
	var messages []string
	for _, provider := range p.providers {
		history, err := provider.FetchHistory(ctx, ticker, start, end)
		if err == nil {
			if len(history.Records) > 0 {
				return history, nil
			}
			messages = append(messages, "provider returned no price records")
			continue
		}
		messages = append(messages, err.Error())
	}
	if len(messages) == 0 {
		return PriceHistory{}, errors.New("no price providers configured")
	}
	return PriceHistory{}, errors.New(strings.Join(messages, "; "))
}
