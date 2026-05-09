package telemetry

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

// ModelPricing is expressed in USD per 1M tokens.
type ModelPricing struct {
	InputPerM       float64 `yaml:"input_per_m" json:"inputPerM"`
	CachedInputPerM float64 `yaml:"cached_input_per_m" json:"cachedInputPerM"`
	OutputPerM      float64 `yaml:"output_per_m" json:"outputPerM"`
	TotalPerM       float64 `yaml:"total_per_m" json:"totalPerM"`

	HasInput       bool
	HasCachedInput bool
	HasOutput      bool
	HasTotal       bool
}

// EstimateCostUSD estimates a run cost from user-provided pricing. Prices are
// intentionally not hard-coded because users may run Codex through custom
// providers with their own model names and rates.
func EstimateCostUSD(model string, inputTokens, outputTokens, cacheReadTokens int64, totalOnly bool) (float64, bool) {
	return EstimateCostUSDFromEnv(os.Environ(), model, inputTokens, outputTokens, cacheReadTokens, totalOnly)
}

func EstimateCostUSDForRoot(root, model string, inputTokens, outputTokens, cacheReadTokens int64, totalOnly bool) (float64, bool) {
	env := append(os.Environ(), PricingEnvFromProviders(root)...)
	return EstimateCostUSDFromEnv(env, model, inputTokens, outputTokens, cacheReadTokens, totalOnly)
}

// EstimateCostUSDFromEnv is the testable/env-injection variant of
// EstimateCostUSD. Supported variables:
//
//	AGENCYCLI_PRICE_<MODEL>_INPUT_PER_M
//	AGENCYCLI_PRICE_<MODEL>_CACHED_INPUT_PER_M
//	AGENCYCLI_PRICE_<MODEL>_OUTPUT_PER_M
//	AGENCYCLI_PRICE_<MODEL>_TOTAL_PER_M
//
// Generic fallbacks without <MODEL> are also supported:
//
//	AGENCYCLI_PRICE_INPUT_PER_M
//	AGENCYCLI_PRICE_CACHED_INPUT_PER_M
//	AGENCYCLI_PRICE_OUTPUT_PER_M
//	AGENCYCLI_PRICE_TOTAL_PER_M
func EstimateCostUSDFromEnv(env []string, model string, inputTokens, outputTokens, cacheReadTokens int64, totalOnly bool) (float64, bool) {
	p := PricingFromEnv(env, model)
	if totalOnly {
		if !p.HasTotal {
			return 0, false
		}
		return float64(inputTokens) / 1e6 * p.TotalPerM, true
	}
	if !p.HasInput && !p.HasOutput && !p.HasCachedInput {
		return 0, false
	}
	uncachedInput := inputTokens
	if cacheReadTokens > 0 && cacheReadTokens < inputTokens {
		uncachedInput = inputTokens - cacheReadTokens
	}
	var cost float64
	if p.HasInput {
		cost += float64(uncachedInput) / 1e6 * p.InputPerM
		if cacheReadTokens > 0 {
			if p.HasCachedInput {
				cost += float64(cacheReadTokens) / 1e6 * p.CachedInputPerM
			} else {
				cost += float64(cacheReadTokens) / 1e6 * p.InputPerM
			}
		}
	}
	if p.HasOutput {
		cost += float64(outputTokens) / 1e6 * p.OutputPerM
	}
	return cost, true
}

func PricingFromEnv(env []string, model string) ModelPricing {
	suffix := sanitizePriceModel(model)
	lookup := func(keys ...string) (float64, bool) {
		for _, key := range keys {
			if suffix != "" {
				if v, ok := lookupEnv(env, "AGENCYCLI_PRICE_"+suffix+"_"+key+"_PER_M"); ok {
					return v, true
				}
			}
			if v, ok := lookupEnv(env, "AGENCYCLI_PRICE_"+key+"_PER_M"); ok {
				return v, true
			}
		}
		return 0, false
	}
	var p ModelPricing
	p.InputPerM, p.HasInput = lookup("INPUT")
	p.CachedInputPerM, p.HasCachedInput = lookup("CACHED_INPUT", "CACHE_READ")
	p.OutputPerM, p.HasOutput = lookup("OUTPUT")
	p.TotalPerM, p.HasTotal = lookup("TOTAL")
	return p
}

type providerPricingFileRow struct {
	Model   string       `yaml:"model"`
	Pricing ModelPricing `yaml:"pricing"`
}

func PricingEnvFromProviders(root string) []string {
	if root == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(root, ".agencycli", "providers.yaml"))
	if err != nil {
		return nil
	}
	var rows []providerPricingFileRow
	if yaml.Unmarshal(data, &rows) != nil {
		return nil
	}
	var env []string
	for _, row := range rows {
		suffix := sanitizePriceModel(row.Model)
		add := func(name string, value float64) {
			if value <= 0 {
				return
			}
			val := strings.TrimRight(strings.TrimRight(strconv.FormatFloat(value, 'f', 8, 64), "0"), ".")
			if suffix != "" {
				env = append(env, "AGENCYCLI_PRICE_"+suffix+"_"+name+"_PER_M="+val)
			} else {
				env = append(env, "AGENCYCLI_PRICE_"+name+"_PER_M="+val)
			}
		}
		add("INPUT", row.Pricing.InputPerM)
		add("CACHED_INPUT", row.Pricing.CachedInputPerM)
		add("OUTPUT", row.Pricing.OutputPerM)
		add("TOTAL", row.Pricing.TotalPerM)
	}
	return env
}

func lookupEnv(env []string, key string) (float64, bool) {
	for i := len(env) - 1; i >= 0; i-- {
		k, v, ok := strings.Cut(env[i], "=")
		if !ok || k != key {
			continue
		}
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return f, err == nil
	}
	return 0, false
}

func sanitizePriceModel(model string) string {
	var b strings.Builder
	lastUnderscore := false
	for _, r := range strings.ToUpper(strings.TrimSpace(model)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	return strings.Trim(b.String(), "_")
}
