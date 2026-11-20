package serve

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"reasonix/internal/config"
	"reasonix/internal/control"
)

// tl1724 pins the serve side of the invalid-scope contract: POST /rewind with
// any scope outside "code" | "conversation" | "both" must be rejected with 400
// and exactly control.ErrRewindInvalidScope's text — the same outcome the
// desktop preview/commit entry points now produce for the same input.
func TestServeRewindInvalidScopeRejectedWithSharedContract(t *testing.T) {
	s := New(control.New(control.Options{}), NewBroadcaster(), config.ServeConfig{})
	h := s.Handler()

	for _, scope := range []string{"bogus", "", "CODE", "code "} {
		body := `{"turn":1,"scope":` + strconv.Quote(scope) + `}`
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/rewind", strings.NewReader(body))
		req.Host = "127.0.0.1" // httptest defaults to example.com; hostGuard 421s that
		req.Header.Set("Content-Type", "application/json")
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("POST /rewind scope=%q: want 400, got %d body=%s", scope, rec.Code, rec.Body.String())
		}
		if got := rec.Body.String(); got != control.ErrRewindInvalidScope.Error()+"\n" {
			t.Fatalf("POST /rewind scope=%q: body = %q, want shared contract text %q", scope, got, control.ErrRewindInvalidScope.Error())
		}
	}
}
