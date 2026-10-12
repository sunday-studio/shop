package config

import "testing"

func TestFoodFactsBaseURL(t *testing.T) {
	t.Setenv("OFF_BASE_URL", "")
	c, err := Load()
	if err != nil || c.FoodFactsBaseURL != "" {
		t.Fatalf("default should be empty (real Open Food Facts): %q %v", c.FoodFactsBaseURL, err)
	}
	t.Setenv("OFF_BASE_URL", "http://fake:1234")
	c, err = Load()
	if err != nil || c.FoodFactsBaseURL != "http://fake:1234" {
		t.Fatalf("OFF_BASE_URL: %q %v", c.FoodFactsBaseURL, err)
	}
}
