package main

import (
	"testing"
)

func TestGenerateShortCode(t *testing.T) {
	code1 := generateShortCode()
	code2 := generateShortCode()

	if len(code1) != 8 {
		t.Errorf("expected 8 hex chars, got %d: %s", len(code1), code1)
	}
	if code1 == code2 {
		t.Error("two sequential codes should be different")
	}

	// Check it's valid hex
	for _, c := range code1 {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("invalid hex character: %c", c)
		}
	}
}
