package telemetry

import "testing"

func TestEstimateCostUSDFromEnv_ModelSpecificPricing(t *testing.T) {
	env := []string{
		"AGENCYCLI_PRICE_GPT_5_5_INPUT_PER_M=1.25",
		"AGENCYCLI_PRICE_GPT_5_5_CACHED_INPUT_PER_M=0.125",
		"AGENCYCLI_PRICE_GPT_5_5_OUTPUT_PER_M=10",
	}
	cost, ok := EstimateCostUSDFromEnv(env, "gpt-5.5", 1_000_000, 500_000, 400_000, false)
	if !ok {
		t.Fatal("expected cost to be available")
	}
	want := 0.6*1.25 + 0.4*0.125 + 0.5*10
	if cost != want {
		t.Fatalf("cost: got %v, want %v", cost, want)
	}
}

func TestEstimateCostUSDFromEnv_TotalOnly(t *testing.T) {
	env := []string{"AGENCYCLI_PRICE_TOTAL_PER_M=2"}
	cost, ok := EstimateCostUSDFromEnv(env, "", 1_500_000, 0, 0, true)
	if !ok {
		t.Fatal("expected cost to be available")
	}
	if cost != 3 {
		t.Fatalf("cost: got %v, want 3", cost)
	}
}
