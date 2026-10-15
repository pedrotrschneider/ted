package httpapi

import (
	"bytes"
	"crypto/sha256"
	"net/http"
	"time"

	"entertainmentdistrict/internal/accounts"

	"github.com/alexedwards/scs/v2"
)

// Authentication JSON responses are small and finite. Publish them only after the
// session save succeeds: SCS LoadAndSave can write an error then append a handler's
// success body when its store fails. Media and future SSE routes must not use this buffer.
func (a *API) sessionHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.Header.Values("Authorization")) != 0 {
			a.fail(w, r, accounts.ErrUnauthorized)
			return
		}
		w.Header().Add("Vary", "Cookie")
		token := ""
		if cookie, err := r.Cookie(a.sessions.Cookie.Name); err == nil {
			token = cookie.Value
		}
		if token != "" {
			digest := sha256.Sum256([]byte(token))
			var revoked bool
			if err := a.pool.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM browser_session_revocations WHERE token_digest=$1 AND expires_at>clock_timestamp())", digest[:]).Scan(&revoked); err != nil {
				a.fail(w, r, err)
				return
			}
			if revoked {
				token = ""
			}
		}
		ctx, err := a.sessions.Load(r.Context(), token)
		if err != nil {
			a.fail(w, r, err)
			return
		}
		r = r.WithContext(ctx)
		output := &responseBuffer{header: make(http.Header), status: 200}
		next.ServeHTTP(output, r)
		switch a.sessions.Status(ctx) {
		case scs.Modified:
			nextToken, expiry, err := a.sessions.Commit(ctx)
			if err != nil {
				a.fail(w, r, err)
				return
			}
			a.sessions.WriteSessionCookie(ctx, w, nextToken, expiry)
		case scs.Destroyed:
			a.sessions.WriteSessionCookie(ctx, w, "", time.Time{})
		}
		for key, values := range output.header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(output.status)
		_, _ = w.Write(output.body.Bytes())
	})
}

type responseBuffer struct {
	header  http.Header
	status  int
	written bool
	body    bytes.Buffer
}

func (b *responseBuffer) Header() http.Header { return b.header }
func (b *responseBuffer) WriteHeader(status int) {
	if !b.written {
		b.status = status
		b.written = true
	}
}
func (b *responseBuffer) Write(p []byte) (int, error) {
	if !b.written {
		b.WriteHeader(200)
	}
	return b.body.Write(p)
}

// Browser-only logout leaves native families and other browser sessions intact.
// Retain a bounded digest tombstone beyond the old session's maximum lifetime,
// so even an already-loaded request's late session save cannot resurrect it.
func (a *API) logoutBrowser(w http.ResponseWriter, r *http.Request, _ accounts.User) {
	cookie, err := r.Cookie(a.sessions.Cookie.Name)
	if err != nil {
		a.fail(w, r, accounts.ErrUnauthorized)
		return
	}
	digest := sha256.Sum256([]byte(cookie.Value))
	_, err = a.pool.Exec(r.Context(), `INSERT INTO browser_session_revocations(token_digest,expires_at)
 VALUES($1,clock_timestamp()+make_interval(secs => $2)) ON CONFLICT(token_digest) DO UPDATE SET expires_at=GREATEST(browser_session_revocations.expires_at,EXCLUDED.expires_at)`, digest[:], a.sessions.Lifetime.Seconds())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if err = a.sessions.Destroy(r.Context()); err != nil {
		a.fail(w, r, err)
		return
	}
	respond(w, 200, map[string]string{"status": "browser session signed out"})
}
