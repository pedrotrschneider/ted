package metadata

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type ProviderConfig struct {
	TMDBToken, TVDBKey, TVDBPIN string
	Language, Country           string
}
type Providers struct {
	config           ProviderConfig
	http             *http.Client
	tmdbURL, tvdbURL string
	mu               sync.Mutex
	tvdbToken        string
	tokenUntil       time.Time
	artworkTypes     map[int]string
	peopleTypes      map[int]string
	typesUntil       time.Time
	loginGate        chan struct{}
}

func NewProviders(config ProviderConfig) *Providers {
	if config.Language == "" {
		config.Language = "en"
	}
	if config.Country == "" {
		config.Country = "US"
	}
	return &Providers{config: config, loginGate: make(chan struct{}, 1), http: &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, tmdbURL: "https://api.themoviedb.org/3", tvdbURL: "https://api4.thetvdb.com/v4"}
}
func (p *Providers) Enabled(provider string) bool {
	if provider == "Tmdb" {
		return p.config.TMDBToken != ""
	}
	return provider == "Tvdb" && p.config.TVDBKey != ""
}
func (p *Providers) get(ctx context.Context, provider, path string, out any) error {
	if !p.Enabled(provider) {
		return ErrUnconfigured
	}
	if provider == "Tmdb" {
		u, e := url.Parse(p.tmdbURL + path)
		if e != nil {
			return ErrInvalid
		}
		q := u.Query()
		q.Set("language", p.config.Language)
		u.RawQuery = q.Encode()
		return p.request(ctx, http.MethodGet, u.String(), p.config.TMDBToken, nil, out)
	}
	token, e := p.token(ctx)
	if e != nil {
		return e
	}
	e = p.request(ctx, http.MethodGet, p.tvdbURL+path, token, nil, out)
	// Retry authentication once; never loop or sleep in a provider adapter.
	if _, ok := e.(unauthorized); ok {
		p.mu.Lock()
		if p.tvdbToken == token {
			p.tokenUntil = time.Time{}
		}
		p.mu.Unlock()
		token, e = p.token(ctx)
		if e != nil {
			return e
		}
		e = p.request(ctx, http.MethodGet, p.tvdbURL+path, token, nil, out)
	}
	return e
}

type unauthorized struct{}

func (unauthorized) Error() string { return "metadata provider authentication failed" }
func (p *Providers) request(ctx context.Context, method, address, token string, body []byte, out any) error {
	req, e := http.NewRequestWithContext(ctx, method, address, bytes.NewReader(body))
	if e != nil {
		return ErrInvalid
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, e := p.http.Do(req)
	if e != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrProvider
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case 200:
	case 404:
		return ErrMissing
	case 401, 403:
		return unauthorized{}
	default:
		return ErrProvider
	}
	raw, e := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024+1))
	if e != nil || len(raw) > 8*1024*1024 {
		return ErrPartial
	}
	if e = json.Unmarshal(raw, out); e != nil {
		return ErrPartial
	}
	return nil
}
func (p *Providers) token(ctx context.Context) (string, error) {
	select {
	case p.loginGate <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	defer func() { <-p.loginGate }()
	p.mu.Lock()
	if time.Now().Before(p.tokenUntil) {
		token := p.tvdbToken
		p.mu.Unlock()
		return token, nil
	}
	p.mu.Unlock()
	body, _ := json.Marshal(map[string]string{"apikey": p.config.TVDBKey, "pin": p.config.TVDBPIN})
	var response struct {
		Status string
		Data   *struct{ Token string }
	}
	if e := p.request(ctx, http.MethodPost, p.tvdbURL+"/login", "", body, &response); e != nil {
		return "", e
	}
	if response.Status != "success" || response.Data == nil || response.Data.Token == "" || len(response.Data.Token) > 8192 || strings.ContainsAny(response.Data.Token, "\r\n") {
		return "", ErrPartial
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.tvdbToken = response.Data.Token
	p.tokenUntil = time.Now().Add(20 * time.Hour)
	return p.tvdbToken, nil
}
func (p *Providers) Fetch(ctx context.Context, provider string, req Request) (Document, error) {
	if provider == "Tmdb" {
		return p.tmdb(ctx, req)
	}
	if provider == "Tvdb" {
		doc, err := p.tvdb(ctx, req)
		if req.Kind == "person" && doc.Item.Name != "" && (err == nil || errors.Is(err, ErrPartial)) {
			var fallbackErr error
			doc, fallbackErr = p.personBiographyFallback(ctx, doc)
			err = errors.Join(err, fallbackErr)
		}
		return doc, err
	}
	return Document{}, ErrInvalid
}
func (p *Providers) ResolveCredit(ctx context.Context, provider, kind, id string) (string, error) {
	if !positiveID.MatchString(id) || (kind != "movie" && kind != "series") {
		return "", ErrInvalid
	}
	if provider == "Tmdb" {
		endpoint := "/movie/"
		if kind == "series" {
			endpoint = "/tv/"
		}
		var v struct {
			IMDB string `json:"imdb_id"`
		}
		if e := p.get(ctx, provider, endpoint+id+"/external_ids", &v); e != nil {
			return "", e
		}
		if !titleID.MatchString(v.IMDB) {
			return "", ErrMissing
		}
		return v.IMDB, nil
	}
	endpoint := "/movies/"
	if kind == "series" {
		endpoint = "/series/"
	}
	var v struct {
		Status string
		Data   *tvRecord
	}
	if e := p.get(ctx, provider, endpoint+id+"/extended?short=true", &v); e != nil {
		return "", e
	}
	if v.Status != "success" || v.Data == nil || fmt.Sprint(v.Data.ID) != id {
		return "", ErrPartial
	}
	ids := remoteIDs(v.Data.RemoteIDs)
	if !titleID.MatchString(ids["Imdb"]) {
		return "", ErrMissing
	}
	return ids["Imdb"], nil
}
func imageURL(raw string) string {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || u.Port() != "" || len(raw) > 4096 {
		return ""
	}
	switch strings.ToLower(u.Hostname()) {
	case "image.tmdb.org", "artworks.thetvdb.com", "banners.thetvdb.com":
		return raw
	}
	return ""
}
func tmImage(path string) string {
	if path == "" || !strings.HasPrefix(path, "/") || strings.ContainsAny(path, "?#\\") || len(path) > 1024 {
		return ""
	}
	return imageURL("https://image.tmdb.org/t/p/original" + path)
}
func publicLink(raw string) string {
	u, e := url.Parse(raw)
	if e != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || len(raw) > 4096 {
		return ""
	}
	return raw
}
