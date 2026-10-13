package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// QuotaError distinguishes exhausted credits/entitlement from invalid keys,
// including gateways which encode billing failures as HTTP 401 or 429. Raw
// bodies may contain private billing URLs or credentials and are not retained.
// RetryAfter carries the provider-supplied wait (task 845) so the agent's
// quota lane can schedule its long wait instead of probing blindly.
type QuotaError struct {
	Provider            string
	ProviderDisplayName string
	Protocol            string
	Status              int
	Code                string
	RetryAfter          time.Duration
}

func (e *QuotaError) Error() string {
	return fmt.Sprintf("provider %q credits or subscription quota exhausted (HTTP %d); check account allowance before continuing", ProviderDisplayLabel(e.Provider, e.ProviderDisplayName, e.Protocol), e.Status)
}

// Unwrap preserves status-based APIError consumers without exposing the raw
// billing response or making a quota rejection look like an AuthError.
func (e *QuotaError) Unwrap() error {
	return &APIError{Provider: e.Provider, ProviderDisplayName: e.ProviderDisplayName, Protocol: e.Protocol, Status: e.Status}
}

func QuotaErrorFromResponse(name string, status int, body string) *QuotaError {
	return quotaErrorFromResponse(name, "", "", status, body, 0)
}

func QuotaErrorFromResponseWithIdentity(name, displayName, protocol string, status int, body string) *QuotaError {
	return quotaErrorFromResponse(name, displayName, protocol, status, body, 0)
}

func quotaErrorFromResponse(name, displayName, protocol string, status int, body string, retryAfter time.Duration) *QuotaError {
	var v struct {
		Error struct {
			Code    string `json:"code"`
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal([]byte(body), &v)
	code := v.Error.Code
	if code == "" {
		code = v.Error.Type
	}
	lower := strings.ToLower(body)
	// Task 845: the GLM/BigModel gateways answer an exhausted plan quota in
	// Chinese ("额度已用完") with no English marker — previously that fell
	// through to the ordinary 429 lane and the long-wait quota lane never
	// engaged. The list stays NARROW: a misfire here only converts a waitable
	// rate limit into the (also waitable, cancellable) quota lane, but a wide
	// list would swallow ordinary limits. Match on the raw body — ToLower is
	// a no-op on CJK, so the markers below are the exact user-visible strings.
	for _, marker := range []string{"insufficient_quota", "insufficient balance", "insufficient token quota", "out of budget", "quota exceeded", "freeusagelimiterror", "gousagelimiterror", "creditserror", "monthly usage limit reached", "available balance", "额度已用完", "额度不足", "配额已用完", "配额不足", "余额不足", "用量已达上限"} {
		if strings.Contains(lower, marker) {
			return &QuotaError{Provider: name, ProviderDisplayName: displayName, Protocol: protocol, Status: status, Code: code, RetryAfter: retryAfter}
		}
	}
	if status == 402 {
		return &QuotaError{Provider: name, ProviderDisplayName: displayName, Protocol: protocol, Status: status, Code: code, RetryAfter: retryAfter}
	}
	return nil
}

// AsQuotaError also handles legacy adapters returning structured API/Auth errors.
func AsQuotaError(err error) *QuotaError {
	var quota *QuotaError
	if errors.As(err, &quota) {
		return quota
	}
	var api *APIError
	if errors.As(err, &api) {
		return quotaErrorFromResponse(api.Provider, api.ProviderDisplayName, api.Protocol, api.Status, api.Body, api.RetryAfter)
	}
	var auth *AuthError
	if errors.As(err, &auth) {
		return quotaErrorFromResponse(auth.Provider, auth.ProviderDisplayName, auth.Protocol, auth.Status, auth.Body, 0)
	}
	return nil
}
