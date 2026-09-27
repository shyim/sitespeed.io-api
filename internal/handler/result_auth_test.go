package handler_test

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/shyim/sitespeed-api/internal/handler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testSecret = "s3cr3t-signing-key"

// newResultAuthServer builds a mux wired like cmd/api does, but with a stub
// handler so the tests exercise routing and auth only, no S3 involved.
func newResultAuthServer(t *testing.T) *httptest.Server {
	t.Helper()
	h := handler.NewHandler(nil, nil)

	mux := http.NewServeMux()
	mux.Handle("GET /result/{id}/{path...}",
		h.ResultAuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})))
	mux.Handle("GET /screenshot/{id}",
		h.ResultAuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})))

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func get(t *testing.T, url string, cookies []*http.Cookie) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	require.NoError(t, err)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func TestSignResultIsDeterministicAndURLSafe(t *testing.T) {
	sig := handler.SignResult(testSecret, "my-analysis", 0)

	assert.Equal(t, sig, handler.SignResult(testSecret, "my-analysis", 0))
	assert.NotContains(t, sig, "+")
	assert.NotContains(t, sig, "/")
	assert.NotContains(t, sig, "=")
}

func TestSignResultIsBoundToSecretAndID(t *testing.T) {
	base := handler.SignResult(testSecret, "my-analysis", 0)

	assert.NotEqual(t, base, handler.SignResult("other-secret", "my-analysis", 0))
	assert.NotEqual(t, base, handler.SignResult(testSecret, "other-analysis", 0))
	// The expiry is part of the signed material, so a non-expiring signature can
	// never be replayed as an expiring one (or the other way around).
	assert.NotEqual(t, base, handler.SignResult(testSecret, "my-analysis", 1234))
}

func TestSignResultExpiryRoundTrip(t *testing.T) {
	expires := handler.ResultLinkExpiry(time.Hour)
	assert.InDelta(t, time.Now().Add(time.Hour).Unix(), expires, 5)
}

func TestResultAuthDisabledWithoutSecret(t *testing.T) {
	t.Setenv("RESULT_AUTH_SECRET", "")
	t.Setenv("AUTH_TOKEN", "api-token")
	srv := newResultAuthServer(t)

	resp := get(t, srv.URL+"/result/my-analysis/index.html", nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Empty(t, resp.Cookies())
}

func TestResultAuthScreenshotAcceptsSignedLink(t *testing.T) {
	t.Setenv("RESULT_AUTH_SECRET", testSecret)
	t.Setenv("AUTH_TOKEN", "")
	srv := newResultAuthServer(t)

	// The screenshot endpoint takes the same signature as the report, since both
	// are keyed on the analysis id alone.
	sig := handler.SignResult(testSecret, "my-analysis", 0)

	resp := get(t, srv.URL+"/screenshot/my-analysis?sig="+sig, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, resp.Cookies(), 1)

	// An expiring link works here too, and the cookie still travels with the
	// same grant.
	expires := handler.ResultLinkExpiry(time.Hour)
	expiring := handler.SignResult(testSecret, "my-analysis", expires)
	expResp := get(t, srv.URL+"/screenshot/my-analysis?sig="+expiring+"&expires="+strconv.FormatInt(expires, 10), nil)
	assert.Equal(t, http.StatusOK, expResp.StatusCode)
}

func TestResultAuthRejectsUnsignedRequests(t *testing.T) {
	t.Setenv("RESULT_AUTH_SECRET", testSecret)
	t.Setenv("AUTH_TOKEN", "")
	srv := newResultAuthServer(t)

	assert.Equal(t, http.StatusUnauthorized, get(t, srv.URL+"/result/my-analysis/index.html", nil).StatusCode)
	assert.Equal(t, http.StatusUnauthorized, get(t, srv.URL+"/screenshot/my-analysis", nil).StatusCode)
}

func TestResultAuthScreenshotIsProtected(t *testing.T) {
	t.Setenv("RESULT_AUTH_SECRET", testSecret)
	t.Setenv("AUTH_TOKEN", "")
	srv := newResultAuthServer(t)

	// No signature at all.
	assert.Equal(t, http.StatusUnauthorized, get(t, srv.URL+"/screenshot/my-analysis", nil).StatusCode)

	// A signature for a different analysis id must not unlock this screenshot.
	other := handler.SignResult(testSecret, "other-analysis", 0)
	assert.Equal(t, http.StatusUnauthorized, get(t, srv.URL+"/screenshot/my-analysis?sig="+other, nil).StatusCode)

	// Nor may a report link be replayed against a guessed screenshot id.
	reportSig := handler.SignResult(testSecret, "my-analysis", 0)
	assert.Equal(t, http.StatusUnauthorized, get(t, srv.URL+"/screenshot/guessed-id?sig="+reportSig, nil).StatusCode)
}

func TestResultAuthAcceptsValidSignature(t *testing.T) {
	t.Setenv("RESULT_AUTH_SECRET", testSecret)
	t.Setenv("AUTH_TOKEN", "")
	srv := newResultAuthServer(t)

	sig := handler.SignResult(testSecret, "my-analysis", 0)
	resp := get(t, srv.URL+"/result/my-analysis/index.html?sig="+sig, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	cookies := resp.Cookies()
	require.Len(t, cookies, 1)
	assert.Equal(t, "sitespeed_result_auth", cookies[0].Name)
	assert.Equal(t, "0."+sig, cookies[0].Value)
	assert.True(t, cookies[0].HttpOnly)
}

func TestResultAuthCookieUnlocksRelativeAssets(t *testing.T) {
	t.Setenv("RESULT_AUTH_SECRET", testSecret)
	t.Setenv("AUTH_TOKEN", "")
	srv := newResultAuthServer(t)

	// The report links its pages and assets with relative URLs, so those requests
	// carry no signature of their own. The cookie set by the signed request is
	// what makes the report browsable.
	sig := handler.SignResult(testSecret, "my-analysis", 0)
	first := get(t, srv.URL+"/result/my-analysis/index.html?sig="+sig, nil)
	require.Equal(t, http.StatusOK, first.StatusCode)
	cookies := first.Cookies()
	require.Len(t, cookies, 1)

	asset := get(t, srv.URL+"/result/my-analysis/pages/example.com/data/screenshots/1/afterPageCompleteCheck.png", cookies)
	assert.Equal(t, http.StatusOK, asset.StatusCode)

	// Screenshots live under a different path but belong to the same report.
	shot := get(t, srv.URL+"/screenshot/my-analysis", cookies)
	assert.Equal(t, http.StatusOK, shot.StatusCode)
}

func TestResultAuthCookieIsBoundToAnalysisID(t *testing.T) {
	t.Setenv("RESULT_AUTH_SECRET", testSecret)
	t.Setenv("AUTH_TOKEN", "")
	srv := newResultAuthServer(t)

	sig := handler.SignResult(testSecret, "my-analysis", 0)
	first := get(t, srv.URL+"/result/my-analysis/index.html?sig="+sig, nil)
	require.Equal(t, http.StatusOK, first.StatusCode)
	cookies := first.Cookies()

	// A link to one report must not unlock any other report.
	other := get(t, srv.URL+"/result/other-analysis/index.html", cookies)
	assert.Equal(t, http.StatusUnauthorized, other.StatusCode)
}

func TestResultAuthAcceptsExpiringSignature(t *testing.T) {
	t.Setenv("RESULT_AUTH_SECRET", testSecret)
	t.Setenv("RESULT_AUTH_TTL", "24h")
	t.Setenv("AUTH_TOKEN", "")
	srv := newResultAuthServer(t)

	expires := handler.ResultLinkExpiry(time.Hour)
	sig := handler.SignResult(testSecret, "my-analysis", expires)
	url := srv.URL + "/result/my-analysis/index.html?sig=" + sig + "&expires=" + strconv.FormatInt(expires, 10)

	resp := get(t, url, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// The cookie must not outlive the link it came from.
	cookies := resp.Cookies()
	require.Len(t, cookies, 1)
	assert.InDelta(t, 3600, cookies[0].MaxAge, 60)
	assert.Equal(t, strconv.FormatInt(expires, 10)+"."+sig, cookies[0].Value)
}

func TestResultAuthRejectsExpiredSignature(t *testing.T) {
	t.Setenv("RESULT_AUTH_SECRET", testSecret)
	t.Setenv("AUTH_TOKEN", "")
	srv := newResultAuthServer(t)

	expires := handler.ResultLinkExpiry(-time.Hour)
	sig := handler.SignResult(testSecret, "my-analysis", expires)
	url := srv.URL + "/result/my-analysis/index.html?sig=" + sig + "&expires=" + strconv.FormatInt(expires, 10)

	resp := get(t, url, nil)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Empty(t, resp.Cookies())
}

func TestResultAuthRejectsForgedAndTamperedSignatures(t *testing.T) {
	t.Setenv("RESULT_AUTH_SECRET", testSecret)
	t.Setenv("AUTH_TOKEN", "")
	srv := newResultAuthServer(t)

	valid := handler.SignResult(testSecret, "my-analysis", 0)

	assert.Equal(t, http.StatusUnauthorized,
		get(t, srv.URL+"/result/my-analysis/index.html?sig="+handler.SignResult("guess", "my-analysis", 0), nil).StatusCode,
		"signature made with the wrong secret")
	assert.Equal(t, http.StatusUnauthorized,
		get(t, srv.URL+"/result/my-analysis/index.html?sig=notbase64%21%21", nil).StatusCode,
		"malformed signature")
	assert.Equal(t, http.StatusUnauthorized,
		get(t, srv.URL+"/result/my-analysis/index.html?sig="+valid[:len(valid)-1]+"A", nil).StatusCode,
		"signature with one byte flipped")
	assert.Equal(t, http.StatusUnauthorized,
		get(t, srv.URL+"/result/my-analysis/index.html?sig="+valid+"&expires=abc", nil).StatusCode,
		"non-numeric expiry")
}

func TestResultAuthAcceptsAPIBearerToken(t *testing.T) {
	t.Setenv("RESULT_AUTH_SECRET", testSecret)
	t.Setenv("AUTH_TOKEN", "api-token")
	srv := newResultAuthServer(t)

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/result/my-analysis/index.html", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer api-token")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	req, err = http.NewRequest(http.MethodGet, srv.URL+"/result/my-analysis/index.html", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer wrong-token")
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}
