package handler

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	// ResultAuthSecretEnv is the static shared secret used to sign result links.
	// When unset, the result endpoints stay unauthenticated (previous behaviour).
	ResultAuthSecretEnv = "RESULT_AUTH_SECRET"

	// ResultAuthTTLEnv sets how long a signed link is allowed to live, e.g. "24h".
	// It is optional: signed links do not expire unless this is configured. When
	// set, it is also used as the lifetime of the cookie that carries the grant
	// to the report's relative assets.
	ResultAuthTTLEnv = "RESULT_AUTH_TTL"

	resultAuthCookieName = "sitespeed_result_auth"
	resultAuthSigParam   = "sig"
	resultAuthExpParam   = "expires"

	// defaultResultAuthCookieTTL bounds how long the browser keeps the grant for
	// an otherwise non-expiring link, so a shared machine does not stay unlocked
	// forever.
	defaultResultAuthCookieTTL = time.Hour
)

// SignResult returns the signature that authorises access to the report of the
// given analysis id. It is the "static shared secret" flavour of an AWS presigned
// URL: the value is derived from the secret and the id only, so anyone holding
// the secret can mint links offline without calling this API.
//
// expires is a unix timestamp in seconds; pass 0 for a link that never expires.
// The signature is URL-safe base64 and can be used as `?sig=...`.
func SignResult(secret, id string, expires int64) string {
	mac := hmac.New(sha256.New, []byte(secret))
	// The separator stops `id` and `expires` from being interchangeable, so
	// ("a\n1", 0) and ("a", 1) cannot produce the same signature.
	mac.Write([]byte(id + "\n" + strconv.FormatInt(expires, 10)))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// ResultLinkExpiry returns a unix expiry timestamp for a link valid for d, meant
// to be passed both to SignResult and as the `expires` query parameter.
func ResultLinkExpiry(d time.Duration) int64 {
	return time.Now().Add(d).Unix()
}

// resultGrant is the material that proves access to one analysis id: a signature
// plus the optional absolute expiry that was signed together with it.
type resultGrant struct {
	sig     string
	expires int64
}

// encode renders the grant as "<expires>.<sig>", always including the expiry so
// that the cookie format stays parseable for non-expiring links too.
func (g resultGrant) encode() string {
	return strconv.FormatInt(g.expires, 10) + "." + g.sig
}

func parseResultGrant(s string) (resultGrant, bool) {
	expPart, sig, found := strings.Cut(s, ".")
	if !found || sig == "" {
		return resultGrant{}, false
	}
	expires, err := strconv.ParseInt(expPart, 10, 64)
	if err != nil || expires < 0 {
		return resultGrant{}, false
	}
	return resultGrant{sig: sig, expires: expires}, true
}

func (g resultGrant) valid(secret, id string, now time.Time) bool {
	if g.sig == "" {
		return false
	}
	if g.expires != 0 && !now.Before(time.Unix(g.expires, 0)) {
		return false
	}
	expected := SignResult(secret, id, g.expires)
	return subtle.ConstantTimeCompare([]byte(expected), []byte(g.sig)) == 1
}

// ResultAuthMiddleware protects the report endpoints (`/result/...` and
// `/screenshot/...`) with signed links when RESULT_AUTH_SECRET is configured.
//
// A request is authorised by, in order:
//
//  1. the API bearer token, so API clients keep working unchanged;
//  2. a `sig` query parameter, optionally paired with `expires`;
//  3. the cookie that (2) sets on the response.
//
// The cookie exists because a sitespeed.io report is a tree of relative links.
// The signature is computed over the analysis id rather than the file path, so
// one signature covers every file in the report, and the cookie carries that
// grant to the CSS, JS and images the report requests afterwards. The cookie is
// bound to the id it was signed for, so a link to one report never unlocks
// another.
//
// This must wrap the handler inside the mux: it relies on the "id" path value
// that the router sets.
func (h *Handler) ResultAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secret := os.Getenv(ResultAuthSecretEnv)
		if secret == "" {
			next.ServeHTTP(w, r)
			return
		}

		id := r.PathValue("id")
		if id == "" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		// Escape hatch for callers that already hold the API token.
		if token := os.Getenv("AUTH_TOKEN"); token != "" && bearerToken(r) == token {
			next.ServeHTTP(w, r)
			return
		}

		now := time.Now()

		if grant, ok := grantFromQuery(r); ok && grant.valid(secret, id, now) {
			setResultAuthCookie(w, grant, now)
			next.ServeHTTP(w, r)
			return
		}

		if cookie, err := r.Cookie(resultAuthCookieName); err == nil {
			if grant, ok := parseResultGrant(cookie.Value); ok && grant.valid(secret, id, now) {
				next.ServeHTTP(w, r)
				return
			}
		}

		http.Error(w, "Unauthorized", http.StatusUnauthorized)
	})
}

func grantFromQuery(r *http.Request) (resultGrant, bool) {
	query := r.URL.Query()
	sig := query.Get(resultAuthSigParam)
	if sig == "" {
		return resultGrant{}, false
	}
	grant := resultGrant{sig: sig}
	if exp := query.Get(resultAuthExpParam); exp != "" {
		expires, err := strconv.ParseInt(exp, 10, 64)
		if err != nil {
			return resultGrant{}, false
		}
		grant.expires = expires
	}
	return grant, true
}

func setResultAuthCookie(w http.ResponseWriter, grant resultGrant, now time.Time) {
	ttl := resultAuthCookieTTL(grant, now)
	if ttl <= 0 {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     resultAuthCookieName,
		Value:    grant.encode(),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(ttl.Seconds()),
	})
}

func resultAuthCookieTTL(grant resultGrant, now time.Time) time.Duration {
	ttl := defaultResultAuthCookieTTL
	if v := os.Getenv(ResultAuthTTLEnv); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			ttl = d
		}
	}
	if grant.expires != 0 {
		if remaining := time.Unix(grant.expires, 0).Sub(now); remaining < ttl {
			ttl = remaining
		}
	}
	if ttl <= 0 {
		return 0
	}
	return ttl
}
