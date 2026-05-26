package service

import "testing"

func TestProviderCapacityFailureIsRetryable(t *testing.T) {
	if !retryableReasons["provider_capacity"] {
		t.Fatal("provider_capacity should be eligible for the existing auto-retry budget")
	}
}
