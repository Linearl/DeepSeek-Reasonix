package provider

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestObservedQuotaResponsesNeverRetryOrBlameCredentials(t *testing.T) {
	for _, tc := range []struct {
		status     int
		body, code string
	}{
		{401, `{"error":{"type":"CreditsError","message":"Insufficient balance. https://example.test/private-billing"}}`, "CreditsError"},
		{402, `{"error":{"code":"too_many_requests","message":"Call failed: Insufficient token quota.","type":"rate_limit_error"}}`, "too_many_requests"},
		{429, `{"error":{"code":"insufficient_quota"}}`, "insufficient_quota"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			for _, managed := range []bool{false, true} {
				ctx := context.Background()
				if managed {
					ctx = WithManagedRecovery(ctx)
				}
				_, err := SendWithRetry(ctx, srv.Client(), SendOptions{Provider: "fixture", KeyPresent: true, RetryAuth: true}, func(ctx context.Context) (*http.Request, error) {
					return http.NewRequestWithContext(ctx, http.MethodPost, srv.URL, nil)
				})
				var quota *QuotaError
				var auth *AuthError
				if !errors.As(err, &quota) || errors.As(err, &auth) || quota.Status != tc.status || quota.Code != tc.code {
					t.Fatalf("wrong classification: %v", err)
				}
				if strings.Contains(err.Error(), "private-billing") || strings.Contains(err.Error(), "invalid") {
					t.Fatal("leaked private URL or misdiagnosed credentials")
				}
				f := ClassifyRecovery(err)
				if f.Phase != "quota" || f.Retryable {
					t.Fatalf("recovery=%+v", f)
				}
			}
			if calls != 2 {
				t.Fatalf("attempts=%d want one per call", calls)
			}
		})
	}
	if AsQuotaError(&AuthError{Status: 401, Body: `{"error":{"type":"AuthError","message":"Missing API key."}}`}) != nil {
		t.Fatal("auth classified as quota")
	}
	if ClassifyRecovery(&APIError{Status: 503, Body: `{"error":{"message":"billing service temporarily unavailable"}}`}).Phase == "quota" {
		t.Fatal("temporary billing service error treated as exhausted quota")
	}
}

// Task 845: the GLM/BigModel gateways answer an exhausted plan quota in
// Chinese with no English marker. Before 845 that fell through to the
// ordinary 429 lane and the quota long-wait never engaged; the narrow
// Chinese marker list must classify these as quota — and keep ordinary
// limits (no quota wording) out of the lane.
func TestChineseQuotaBodiesClassifyAsQuota(t *testing.T) {
	for _, body := range []string{
		`{"error":{"code":"1302","message":"额度已用完"}}`,
		`{"error":{"message":"当前套餐额度不足，请充值后重试"}}`,
		`{"error":{"message":"配额已用完，请明天再试"}}`,
		`{"error":{"message":"余额不足，请前往控制台充值"}}`,
		`{"error":{"message":"今日用量已达上限"}}`,
	} {
		if q := QuotaErrorFromResponse("glm", 429, body); q == nil {
			t.Fatalf("chinese quota body not classified: %s", body)
		}
	}
	for _, body := range []string{
		`{"error":{"message":"并发数超过上限，请稍后重试"}}`,
		`{"error":{"message":"请求过于频繁"}}`,
	} {
		if q := QuotaErrorFromResponse("glm", 429, body); q != nil {
			t.Fatalf("ordinary rate limit swallowed by the quota lane: %s", body)
		}
	}
}

// Task 845: the provider's Retry-After must survive into the quota error and
// the recovery classification, so the quota wait lane can schedule from it.
func TestQuotaRetryAfterSurvivesClassification(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(429)
		_, _ = w.Write([]byte(`{"error":{"message":"额度已用完"}}`))
	}))
	defer srv.Close()
	_, err := SendWithRetry(WithManagedRecovery(context.Background()), srv.Client(), SendOptions{Provider: "glm"}, func(ctx context.Context) (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodPost, srv.URL, nil)
	})
	var quota *QuotaError
	if !errors.As(err, &quota) || quota.RetryAfter != 120*time.Second {
		t.Fatalf("quota=%+v err=%v", quota, err)
	}
	if f := ClassifyRecovery(err); f.Phase != "quota" || f.RetryAfter != 120*time.Second {
		t.Fatalf("recovery=%+v", f)
	}
}
