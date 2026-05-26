package daemon

import "strings"

// FailureReasonProviderCapacity marks a transient upstream provider-side
// capacity rejection. The current auto-retry path can safely requeue these
// under the normal task retry budget because they are infrastructure-shaped,
// not a user/code problem.
const FailureReasonProviderCapacity = "provider_capacity"

// classifyRetryableProviderFailure recognizes provider-side failures that
// should enter the existing auto-retry path instead of surfacing as a plain
// agent_error. Keep this intentionally narrow: false positives would hide
// real model/config errors behind retries.
func classifyRetryableProviderFailure(provider, errMsg string) (string, bool) {
	if errMsg == "" {
		return "", false
	}

	lowered := strings.ToLower(strings.TrimSpace(errMsg))
	switch provider {
	case "codex":
		if strings.Contains(lowered, "selected model is at capacity") {
			return FailureReasonProviderCapacity, true
		}
	}

	return "", false
}
