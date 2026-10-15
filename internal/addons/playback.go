package addons

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/catalog"
	"entertainmentdistrict/internal/database"
	"entertainmentdistrict/internal/realdebrid"
	"entertainmentdistrict/internal/streaming"
	"entertainmentdistrict/internal/watchstate"
	"github.com/jackc/pgx/v5"
)

type selected struct {
	link       link
	descriptor Descriptor
	source     Source
	media      catalog.MediaIdentity
	addonKey   string
}

// Includes collection/account checks, bounded media validation and publication.
// HTTP callers need a slightly larger envelope to return a useful failure.
const SessionPreparationTimeout = 40 * time.Second
const mediaPreparationTimeout = 30 * time.Second

func (s *Service) selected(ctx context.Context, g accounts.NativeGrant, id string) (selected, error) {
	var v selected
	var descriptor, source []byte
	if !nativeUUID.MatchString(id) {
		return v, ErrSelection
	}
	err := s.pool.QueryRow(ctx, `SELECT ns.provider_id::text,ns.account_id,ns.account_updated_at,ns.media_kind,ns.media_identity,ns.addon_key,ns.descriptor,ns.source
 FROM native_addon_sources ns JOIN server_addon_accounts a ON a.id=ns.provider_id
 WHERE ns.id=$1 AND ns.user_id=$2 AND ns.user_id=$2 AND a.active AND a.updated_at=ns.account_updated_at AND a.auth_key!='' AND ns.expires_at>now()`, id, g.User.ID).Scan(&v.link.ID, &v.link.Account, &v.link.Updated, &v.media.Kind, &v.media.ID, &v.addonKey, &descriptor, &source)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, ErrSelection
	}
	if err != nil {
		return v, err
	}
	if json.Unmarshal(descriptor, &v.descriptor) != nil || json.Unmarshal(source, &v.source) != nil {
		return v, ErrIncomplete
	}
	return v, nil
}
func (s *Service) CreateSession(ctx context.Context, auth *accounts.NativeAuth, g accounts.NativeGrant, media catalog.MediaIdentity, sourceID string) (streaming.NativeSession, error) {
	ctx, stop := context.WithTimeout(ctx, SessionPreparationTimeout)
	defer stop()
	v, err := s.selectedForPlayback(ctx, g, media, sourceID)
	if err != nil {
		return streaming.NativeSession{}, err
	}
	l := v.link
	bounded, cancel := context.WithTimeout(ctx, mediaPreparationTimeout)
	started := time.Now()
	p, warm := s.takeWarm(g, sourceID, v.source)
	resolvedAt := time.Now()
	if !warm {
		var resolvedSource Source
		resolvedSource, err = s.resolveRD(bounded, v.source)
		resolvedAt = time.Now()
		if err == nil {
			p, err = s.preparePlayback(bounded, resolvedSource)
		}
	}
	cancel()
	if err != nil {
		if errors.Is(err, ErrUnavailable) || errors.Is(err, context.DeadlineExceeded) {
			if s.logger != nil {
				s.logger.Warn("selected media preparation failed", "operation", "addon_prepare", "media", media.ID, "elapsed_ms", time.Since(started).Milliseconds())
			}
			return streaming.NativeSession{}, fmt.Errorf("%w: %w", ErrPreparation, err)
		}
		return streaming.NativeSession{}, err
	}
	published := false
	defer func() {
		if !published {
			p.startup.close()
		}
	}()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return streaming.NativeSession{}, err
	}
	defer database.Rollback(tx)
	if err = auth.AuthorizeMutation(ctx, tx, g); err != nil {
		return streaming.NativeSession{}, err
	}
	if _, err = tx.Exec(ctx, `SELECT id FROM server_addon_accounts WHERE id=$1 FOR SHARE`, l.ID); err != nil {
		return streaming.NativeSession{}, err
	}
	if err = fence(ctx, tx, l, g.User.ID); err != nil {
		return streaming.NativeSession{}, err
	}
	if err = policyFence(ctx, tx, v.descriptor.PolicyKey()); err != nil {
		return streaming.NativeSession{}, err
	}
	if p.Source.RDScope != "" && s.RD != nil {
		if err = s.RD.ValidateScope(ctx, tx, p.Source.RDScope); err != nil {
			return streaming.NativeSession{}, err
		}
	}
	expected, err := watchstate.ExpectedRuntime(ctx, tx, media)
	if err != nil {
		return streaming.NativeSession{}, err
	}
	id, err := watchstate.EnsureNativeIdentity(ctx, tx, media)
	if err != nil {
		return streaming.NativeSession{}, err
	}
	if _, err = tx.Exec(ctx, `SELECT id FROM catalog_media WHERE id=$1 FOR SHARE`, id); err != nil {
		return streaming.NativeSession{}, err
	}
	var valid bool
	if err = tx.QueryRow(ctx, `SELECT expires_at>now() FROM native_addon_sources WHERE id=$1 FOR SHARE`, sourceID).Scan(&valid); err != nil {
		return streaming.NativeSession{}, err
	}
	if !valid {
		return streaming.NativeSession{}, ErrSelection
	}
	// A source ID is an explicit account-scoped selection, including when the
	// title also has an RD mapping. It does not replace that mapping.
	var state watchstate.State
	var revision int64
	err = tx.QueryRow(ctx, `SELECT state,revision FROM watch_current WHERE user_id=$1 AND media_id=$2`, g.User.ID, id).Scan(&state, &revision)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return streaming.NativeSession{}, err
	}
	var session string
	err = tx.QueryRow(ctx, `INSERT INTO native_playback_sessions(user_id,family_id,device_id,media_id,addon_source_id,duration_ms,applied_revision,expected_duration_ms) VALUES($1,$2,$3,$4,$5,0,$6,$7) RETURNING id::text`, g.User.ID, g.FamilyID, g.DeviceID, id, sourceID, revision, expected).Scan(&session)
	if err != nil {
		return streaming.NativeSession{}, err
	}
	private, err := json.Marshal(p.Source)
	if err != nil {
		return streaming.NativeSession{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO native_addon_playback(session_id,source,file_size,fingerprint,etag) VALUES($1,$2,$3,$4,$5)`, session, private, p.Size, p.Fingerprint, p.ETag); err != nil {
		return streaming.NativeSession{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return streaming.NativeSession{}, err
	}
	s.rememberStartup(session, p)
	published = true
	if s.logger != nil {
		s.logger.Info("selected media prepared", "session_id", session, "rd_direct", p.Source.RDFile != nil,
			"resolve_ms", resolvedAt.Sub(started).Milliseconds(), "validation_and_publish_ms", time.Since(resolvedAt).Milliseconds(), "elapsed_ms", time.Since(started).Milliseconds())
	}
	resume := state.Position / 10000
	subtitles := make([]streaming.NativeSubtitle, 0, len(p.Source.Subtitles))
	for i, sub := range p.Source.Subtitles {
		codec := ""
		if u, e := httpURL(sub.URL); e == nil {
			switch strings.ToLower(pathExtension(u.Path)) {
			case ".srt":
				codec = "subrip"
			case ".vtt":
				codec = "webvtt"
			case ".ass":
				codec = "ass"
			case ".ssa":
				codec = "ssa"
			}
		}
		subtitles = append(subtitles, streaming.NativeSubtitle{URL: fmt.Sprintf("/api/client/v1/playback/sessions/%s/subtitles/%d", session, i), Language: sub.Language, Label: sub.Label, Codec: codec})
	}
	return streaming.NativeSession{ID: session, Media: media, MediaURL: "/api/client/v1/playback/sessions/" + session + "/media", Source: "addon", AddonID: v.addonKey, BingeKey: bingeKey(v.addonKey, v.source.BingeGroup), DurationMS: 0, ExpectedDurationMS: expected, ResumeMS: resume, WatchRevision: revision, AudioLanguages: []string{"en"}, Technical: json.RawMessage(`{}`), Subtitles: subtitles}, nil
}
func (s *Service) playback(ctx context.Context, g accounts.NativeGrant, session string) (selected, prepared, error) {
	var v selected
	var p prepared
	var descriptor, source []byte
	if !nativeUUID.MatchString(session) {
		return v, p, ErrSelection
	}
	var isAddon bool
	err := s.pool.QueryRow(ctx, `SELECT addon_source_id IS NOT NULL FROM native_playback_sessions WHERE id=$1 AND user_id=$2 AND device_id=$3`, session, g.User.ID, g.DeviceID).Scan(&isAddon)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, p, accounts.ErrNotFound
	}
	if err != nil {
		return v, p, err
	}
	if !isAddon {
		return v, p, ErrNotAddon
	}
	err = s.pool.QueryRow(ctx, `SELECT ns.provider_id::text,ns.account_id,ns.account_updated_at,ns.media_kind,ns.media_identity,ns.addon_key,ns.descriptor,p.source,p.file_size,p.fingerprint,p.etag
 FROM native_addon_playback p JOIN native_playback_sessions ps ON ps.id=p.session_id JOIN native_addon_sources ns ON ns.id=ps.addon_source_id
 JOIN server_addon_accounts a ON a.id=ns.provider_id
 WHERE p.session_id=$1 AND ns.user_id=$2 AND a.active AND a.updated_at=ns.account_updated_at AND a.auth_key!=''`, session, g.User.ID).Scan(&v.link.ID, &v.link.Account, &v.link.Updated, &v.media.Kind, &v.media.ID, &v.addonKey, &descriptor, &source, &p.Size, &p.Fingerprint, &p.ETag)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, p, ErrChanged
	}
	if err != nil {
		return v, p, err
	}
	if json.Unmarshal(descriptor, &v.descriptor) != nil || json.Unmarshal(source, &p.Source) != nil {
		return v, p, ErrIncomplete
	}
	v.source = p.Source
	return v, p, nil
}
func (s *Service) renew(ctx context.Context, g accounts.NativeGrant, session string, old prepared, v selected) (prepared, error) {
	_, descriptors, _, err := s.collection(ctx, g.User.ID, false)
	if err != nil {
		return prepared{}, err
	}
	installed := false
	for _, d := range descriptors {
		if d.Key() == v.addonKey && d.Supports(v.media) {
			installed = true
			v.descriptor = d
		}
	}
	if !installed {
		return prepared{}, ErrSelection
	}
	// PostgreSQL serializes renewal across processes. Re-read after obtaining the
	// row lock so a waiting request reuses a successful earlier replacement.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return prepared{}, err
	}
	defer database.Rollback(tx)
	var current []byte
	err = tx.QueryRow(ctx, `SELECT source FROM native_addon_playback WHERE session_id=$1 FOR UPDATE`, session).Scan(&current)
	if err != nil {
		return prepared{}, err
	}
	var now Source
	if json.Unmarshal(current, &now) != nil {
		return prepared{}, ErrIncomplete
	}
	if now.URL != old.Source.URL {
		old.Source = now
		return old, nil
	}
	if err = fence(ctx, tx, v.link, g.User.ID); err != nil {
		return prepared{}, err
	}
	key := old.Source.ReleaseKey()
	if key == "" {
		return prepared{}, ErrSelection
	}
	if err = policyFence(ctx, tx, v.descriptor.PolicyKey()); err != nil {
		return prepared{}, err
	}
	// Collection reads are outside this transaction's account lock; fence again
	// before publication and never reuse a removed/reconfigured add-on.
	matches := []Source{}
	if old.Source.RDFile != nil {
		source, e := s.resolveRD(ctx, old.Source)
		if e != nil {
			return prepared{}, e
		}
		matches = append(matches, source)
	} else {
		sources, _, e := s.fetchStreams(ctx, v.descriptor, v.media.Kind, v.media.ID)
		if e != nil {
			return prepared{}, e
		}
		for _, source := range sources {
			if source.ReleaseKey() == key {
				matches = append(matches, source)
			}
		}
	}
	if len(matches) != 1 {
		return prepared{}, ErrSelection
	}
	fresh, err := s.preparePlayback(ctx, matches[0])
	published := false
	defer func() {
		if !published {
			fresh.startup.close()
		}
	}()
	if err != nil {
		return prepared{}, err
	}
	if fresh.Size != old.Size || fresh.Fingerprint != old.Fingerprint || (old.ETag != "" && fresh.ETag != old.ETag) {
		return prepared{}, ErrSelection
	}
	if _, err = tx.Exec(ctx, `SELECT id FROM server_addon_accounts WHERE id=$1 FOR SHARE`, v.link.ID); err != nil {
		return prepared{}, err
	}
	if err = fence(ctx, tx, v.link, g.User.ID); err != nil {
		return prepared{}, err
	}
	if fresh.Source.RDScope != "" {
		if err = s.RD.ValidateScope(ctx, tx, fresh.Source.RDScope); err != nil {
			return prepared{}, err
		}
	}
	data, _ := json.Marshal(fresh.Source)
	if _, err = tx.Exec(ctx, `UPDATE native_addon_playback SET source=$2,updated_at=clock_timestamp() WHERE session_id=$1`, session, data); err != nil {
		return prepared{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return prepared{}, err
	}
	s.rememberStartup(session, fresh)
	published = true
	return fresh, nil
}

func torrentSelection(s Source) realdebrid.TorrentSelection {
	return realdebrid.TorrentSelection{Hash: s.InfoHash, Index: s.FileIndex, Filename: s.Filename, Bytes: s.VideoSize}
}
func (s *Service) resolveRD(ctx context.Context, source Source) (Source, error) {
	if source.InfoHash == "" {
		return source, nil
	}
	if s.RD == nil {
		return source, realdebrid.ErrNotConfigured
	}
	c, e := s.RD.Snapshot(ctx)
	if e != nil {
		return source, e
	}
	scope := c.CredentialScope()
	var want realdebrid.FileRequest
	var result realdebrid.Resolved
	if source.RDFile != nil {
		want, result, e = s.RD.ResolvePinned(ctx, source.RDScope, *source.RDFile)
	} else {
		want, result, e = s.RD.ResolveSelection(ctx, torrentSelection(source))
	}
	if e != nil {
		return source, e
	}
	current, e := s.RD.Snapshot(ctx)
	if e != nil {
		return source, e
	}
	if current.CredentialScope() != scope {
		return source, realdebrid.ErrMismatch
	}
	source.RDFile = &want
	source.RDScope = scope
	source.URL = result.URL
	source.Headers = map[string]string{}
	if source.VideoSize == 0 {
		source.VideoSize = result.Bytes
	}
	return source, nil
}
func (s *Service) Serve(w http.ResponseWriter, r *http.Request, g accounts.NativeGrant, session string) error {
	return s.serve(w, r, g, session, true)
}
func (s *Service) serve(w http.ResponseWriter, r *http.Request, g accounts.NativeGrant, session string, useStartup bool) error {
	// A selected CDN may need longer than the JSON write deadline to connect.
	// Clear it before opening upstream; CopyMedia supplies the rolling deadline
	// once streaming starts. Database and connection/header waits remain bounded.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	lookup, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	v, p, err := s.playback(lookup, g, session)
	cancel()
	if err != nil {
		return err
	}
	if p.Source.RDFile != nil {
		if s.RD == nil {
			return realdebrid.ErrNotConfigured
		}
		c, e := s.RD.Snapshot(r.Context())
		if e != nil {
			return e
		}
		if !c.Configured() {
			return realdebrid.ErrNotConfigured
		}
		if c.CredentialScope() != p.Source.RDScope {
			bounded, stop := context.WithTimeout(r.Context(), SessionPreparationTimeout)
			p, e = s.renew(bounded, g, session, p, v)
			stop()
			if e != nil {
				return e
			}
		}
	}
	rangeValue := r.Header.Get("Range")
	etag := `"ted-` + p.Fingerprint + `"`
	if validator := r.Header.Get("If-Range"); validator != "" && validator != etag {
		rangeValue = ""
	}
	start, end, err := parseRange(rangeValue, p.Size)
	if err != nil {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", p.Size))
		w.WriteHeader(416)
		return nil
	}
	if r.Method == "HEAD" {
		start = 0
		end = 0
	}
	if cached := s.cachedStartup(session, p); useStartup && cached != nil {
		if handled := s.serveStartup(w, r, p, cached, start, end, rangeValue, func(w http.ResponseWriter, r *http.Request) error {
			return s.serve(w, r, g, session, false)
		}); handled {
			return nil
		}
	}
	open := func(source Source) (*http.Response, error) {
		started := time.Now()
		req, err := sourceRequest(r.Context(), source, fmt.Sprintf("bytes=%d-%d", start, end))
		if err != nil {
			return nil, err
		}
		res, err := s.mediaHTTP().Do(req)
		if err != nil {
			if r.Context().Err() == nil && s.logger != nil {
				s.logger.WarnContext(r.Context(), "selected media connection failed", "operation", "addon_media_open", "session_id", session, "rd_direct", source.RDFile != nil, "elapsed_ms", time.Since(started).Milliseconds())
			}
			return nil, ErrUnavailable
		}
		return res, nil
	}
	res, err := open(p.Source)
	if err != nil {
		return err
	}
	if res.StatusCode == 401 || res.StatusCode == 403 || res.StatusCode == 404 || res.StatusCode == 410 {
		res.Body.Close()
		bounded, cancel := context.WithTimeout(r.Context(), SessionPreparationTimeout)
		p, err = s.renew(bounded, g, session, p, v)
		cancel()
		if err != nil {
			return err
		}
		res, err = open(p.Source)
		if err != nil {
			return err
		}
	}
	defer res.Body.Close()
	a, b, size, err := contentRange(res.Header.Get("Content-Range"))
	if res.StatusCode != 206 || err != nil || a != start || b != end || size != p.Size || !fileContentType(res.Header.Get("Content-Type")) || !identityEncoding(res.Header.Get("Content-Encoding")) {
		return ErrSelection
	}
	if p.ETag != "" && res.Header.Get("ETag") != p.ETag {
		return ErrSelection
	}
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Cache-Control", "private, no-store")
	contentType := res.Header.Get("Content-Type")
	if strings.HasPrefix(strings.ToLower(contentType), "application/force-download") {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("ETag", etag)
	if r.Method == "HEAD" {
		w.Header().Set("Content-Length", strconv.FormatInt(p.Size, 10))
		w.WriteHeader(200)
		return nil
	}
	length := end - start + 1
	w.Header().Set("Content-Length", strconv.FormatInt(length, 10))
	if rangeValue != "" {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, size))
		w.WriteHeader(206)
	} else {
		w.WriteHeader(200)
	}
	// A short transfer remains a short body; never append a JSON error to media.
	transferred, copyErr := streaming.CopyMedia(w, res.Body, length)
	if copyErr != nil && r.Context().Err() == nil && s.logger != nil {
		s.logger.WarnContext(r.Context(), "add-on media transfer interrupted", "session_id", session, "expected_bytes", length, "transferred_bytes", transferred)
	}
	return nil
}
func (s *Service) Subtitle(ctx context.Context, g accounts.NativeGrant, session string, index int) ([]byte, string, error) {
	_, p, err := s.playback(ctx, g, session)
	if err != nil {
		return nil, "", err
	}
	if index < 0 || index >= len(p.Source.Subtitles) {
		return nil, "", accounts.ErrNotFound
	}
	sub := p.Source.Subtitles[index]
	// A sidecar's own URL is selected configuration. Do not forward the media
	// provider's cookies/authorization to a different subtitle origin.
	headers := map[string]string{}
	mediaURL, _ := httpURL(p.Source.URL)
	subURL, err := httpURL(sub.URL)
	if err != nil {
		return nil, "", err
	}
	if mediaURL.Scheme == subURL.Scheme && strings.EqualFold(mediaURL.Host, subURL.Host) {
		headers = p.Source.Headers
	}
	if p.Source.RDFile != nil && len(sub.Headers) > 0 {
		headers = sub.Headers
	}
	req, err := sourceRequest(ctx, Source{URL: sub.URL, Headers: headers}, "")
	if err != nil {
		return nil, "", err
	}
	res, err := s.http.Do(req)
	if err != nil {
		return nil, "", ErrUnavailable
	}
	defer res.Body.Close()
	if res.StatusCode != 200 || !identityEncoding(res.Header.Get("Content-Encoding")) {
		return nil, "", ErrUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, (8<<20)+1))
	if err != nil || len(data) == 0 || len(data) > 8<<20 {
		return nil, "", ErrUnsupported
	}
	// Pass supported sidecars through unchanged, including legacy encodings.
	// Syntax distinguishes SRT/VTT/ASS/SSA from HTML, images and archives.
	prefix := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(string(data), "\xef\xbb\xbf")))
	if !strings.Contains(prefix, " --> ") && !strings.HasPrefix(prefix, "webvtt") && !strings.Contains(prefix, "[script info]") && !strings.Contains(prefix, "[events]") {
		return nil, "", ErrUnsupported
	}
	if strings.Contains(prefix, "<html") || strings.Contains(prefix, "<!doctype") {
		return nil, "", ErrUnsupported
	}
	return data, "text/plain", nil
}

func (s *Service) selectedForPlayback(ctx context.Context, g accounts.NativeGrant, media catalog.MediaIdentity, sourceID string) (selected, error) {
	v, err := s.selected(ctx, g, sourceID)
	if err != nil {
		return selected{}, err
	}
	if v.media != media {
		return selected{}, ErrSelection
	}
	// Re-check installation against the current account cache. A removed or
	// reconfigured add-on cannot use an already issued picker entry after refresh.
	l, descriptors, _, err := s.collection(ctx, g.User.ID, false)
	if err != nil {
		return selected{}, err
	}
	installed := false
	for _, d := range descriptors {
		if d.Key() == v.addonKey && d.Supports(media) {
			installed = true
		}
	}
	if !installed || l.ID != v.link.ID || !l.Updated.Equal(v.link.Updated) {
		return selected{}, ErrSelection
	}
	if off, e := disabledProviders(ctx, s.pool); e != nil {
		return selected{}, e
	} else if off[v.descriptor.PolicyKey()] {
		return selected{}, ErrSelection
	}
	return v, nil
}
