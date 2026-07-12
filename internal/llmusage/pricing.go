package llmusage

import (
	"encoding/json"
	"fmt"
)

// ModelPrice is the USD price per 1,000,000 tokens for one Gemini model.
// Cached input tokens are billed separately (and cheaper) from regular input
// tokens.
type ModelPrice struct {
	InputPerMillionUSD       float64 `json:"inputPerMillionUSD"`
	OutputPerMillionUSD      float64 `json:"outputPerMillionUSD"`
	CachedInputPerMillionUSD float64 `json:"cachedInputPerMillionUSD"`
}

// PricingTable maps a Gemini model name to its price.
type PricingTable map[string]ModelPrice

// defaultPricing covers MODEL_NAME's default (gemini-2.5-flash) so cost
// tracking works out of the box. Google revises Gemini API pricing
// independently of this codebase, so update it via LLM_PRICING_JSON (see
// internal/config.Config.LLMPricingJSON), not by editing this map.
var defaultPricing = PricingTable{
	"gemini-2.5-flash": {
		InputPerMillionUSD:       0.30,
		OutputPerMillionUSD:      2.50,
		CachedInputPerMillionUSD: 0.075,
	},
}

// LoadPricing parses jsonStr (LLM_PRICING_JSON: a JSON object of model name
// to ModelPrice) and merges it over defaultPricing, with entries in jsonStr
// overriding same-named defaults. An empty jsonStr returns defaultPricing
// unchanged.
func LoadPricing(jsonStr string) (PricingTable, error) {
	table := make(PricingTable, len(defaultPricing))
	for k, v := range defaultPricing {
		table[k] = v
	}
	if jsonStr == "" {
		return table, nil
	}
	var override PricingTable
	if err := json.Unmarshal([]byte(jsonStr), &override); err != nil {
		return nil, fmt.Errorf("parse LLM_PRICING_JSON: %w", err)
	}
	for k, v := range override {
		table[k] = v
	}
	return table, nil
}

// EstimateCostUSD returns the estimated cost of one LLM call. promptTokens
// is the Gemini API's total effective prompt size, which already includes
// cachedTokens; the non-cached portion is billed at the regular input rate
// and cachedTokens at the (cheaper) cached rate. A model absent from the
// table prices as $0 so usage is still visible (token counts are recorded
// regardless) even though the cost estimate can't be computed for it.
func (t PricingTable) EstimateCostUSD(model string, promptTokens, candidatesTokens, cachedTokens int32) float64 {
	p, ok := t[model]
	if !ok {
		return 0
	}
	billableInput := promptTokens - cachedTokens
	if billableInput < 0 {
		billableInput = 0
	}
	const million = 1_000_000.0
	return float64(billableInput)/million*p.InputPerMillionUSD +
		float64(cachedTokens)/million*p.CachedInputPerMillionUSD +
		float64(candidatesTokens)/million*p.OutputPerMillionUSD
}
