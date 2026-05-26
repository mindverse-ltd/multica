package daemon

import "testing"

func TestClassifyRetryableProviderFailureCodexCapacity(t *testing.T) {
	msg := "Selected model is at capacity. Please try a different model."

	if got, ok := classifyRetryableProviderFailure("codex", msg); !ok || got != FailureReasonProviderCapacity {
		t.Fatalf("expected codex capacity to be %q, got reason=%q ok=%v", FailureReasonProviderCapacity, got, ok)
	}
}

func TestClassifyRetryableProviderFailureDoesNotCatchModelRejects(t *testing.T) {
	msg := "The 'gpt-5.1-codex-mini' model is not supported when using Codex with a ChatGPT account."

	if got, ok := classifyRetryableProviderFailure("codex", msg); ok {
		t.Fatalf("model support errors should not be retryable, got %q", got)
	}
}
