package tracker

import (
	"strings"
	"sync"
)

type Pricing struct {
	InputPerMTok      float64
	OutputPerMTok     float64
	CacheReadPerMTok  float64
	CacheWritePerMTok float64
}

// pricingStore is the runtime pricing state, safe for concurrent reads after
// initial setup (writes only happen once at startup via ApplyPricing).
var pricingStore = struct {
	mu       sync.RWMutex
	models   map[string]Pricing
	aliases  map[string]string
	fallback Pricing
}{
	models: map[string]Pricing{
		"claude-sonnet-4-20250514": {3.00, 15.00, 0.30, 3.75},
		"claude-opus-4-20250514":   {15.00, 75.00, 1.50, 18.75},
		"claude-3-7-sonnet-20250219": {3.00, 15.00, 0.30, 3.75},
		"claude-3-5-sonnet-20241022": {3.00, 15.00, 0.30, 3.75},
		"claude-3-5-haiku-20241022":  {0.80, 4.00, 0.08, 1.00},
		"claude-3-opus-20240229":     {15.00, 75.00, 1.50, 18.75},
	},
	aliases: map[string]string{
		"claude-sonnet-4":   "claude-sonnet-4-20250514",
		"claude-opus-4":     "claude-opus-4-20250514",
		"claude-3-7-sonnet": "claude-3-7-sonnet-20250219",
		"claude-3-5-sonnet": "claude-3-5-sonnet-20241022",
		"claude-3-5-haiku":  "claude-3-5-haiku-20241022",
		"claude-3-opus":     "claude-3-opus-20240229",
	},
	fallback: Pricing{3.00, 15.00, 0.30, 3.75},
}

// ModelPricingEntry is the external representation used by config loading.
type ModelPricingEntry struct {
	Aliases           []string
	InputPerMTok      float64
	OutputPerMTok     float64
	CacheReadPerMTok  float64
	CacheWritePerMTok float64
}

// ApplyPricing replaces the pricing tables with values from config.
// Pass nil entries to keep built-in defaults for that part.
func ApplyPricing(models map[string]ModelPricingEntry, fallback *Pricing) {
	pricingStore.mu.Lock()
	defer pricingStore.mu.Unlock()

	if models != nil {
		pricingStore.models = make(map[string]Pricing, len(models))
		pricingStore.aliases = make(map[string]string)
		for name, entry := range models {
			pricingStore.models[name] = Pricing{
				InputPerMTok:      entry.InputPerMTok,
				OutputPerMTok:     entry.OutputPerMTok,
				CacheReadPerMTok:  entry.CacheReadPerMTok,
				CacheWritePerMTok: entry.CacheWritePerMTok,
			}
			for _, alias := range entry.Aliases {
				pricingStore.aliases[alias] = name
			}
		}
	}
	if fallback != nil {
		pricingStore.fallback = *fallback
	}
}

func GetPricing(model string) Pricing {
	pricingStore.mu.RLock()
	defer pricingStore.mu.RUnlock()

	if p, ok := pricingStore.models[model]; ok {
		return p
	}
	if resolved, ok := pricingStore.aliases[model]; ok {
		if p, ok := pricingStore.models[resolved]; ok {
			return p
		}
	}
	for prefix, full := range pricingStore.aliases {
		if strings.HasPrefix(model, prefix) {
			if p, ok := pricingStore.models[full]; ok {
				return p
			}
		}
	}
	return pricingStore.fallback
}

func CalculateCost(model string, inputTokens, outputTokens, cacheRead, cacheWrite int) float64 {
	p := GetPricing(model)
	cost := float64(inputTokens) * p.InputPerMTok / 1_000_000
	cost += float64(outputTokens) * p.OutputPerMTok / 1_000_000
	cost += float64(cacheRead) * p.CacheReadPerMTok / 1_000_000
	cost += float64(cacheWrite) * p.CacheWritePerMTok / 1_000_000
	return cost
}
