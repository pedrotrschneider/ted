package addons

import (
	"context"
	"encoding/json"
	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/streaming"
	"entertainmentdistrict/internal/watchstate"
	"net/http"
	"strconv"
)

func (s *Service) NativeDownload(ctx context.Context, g accounts.NativeGrant, session string) (streaming.NativeDownload, error) {
	v, p, err := s.playback(ctx, g, session)
	if err != nil {
		return streaming.NativeDownload{}, err
	}
	if p.Size <= 0 || p.Fingerprint == "" {
		return streaming.NativeDownload{}, ErrIncomplete
	}
	var expected, actual int64
	var placeholder bool
	if err = s.pool.QueryRow(ctx, `SELECT ps.expected_duration_ms,p.observed_duration_ms,p.placeholder FROM native_addon_playback p JOIN native_playback_sessions ps ON ps.id=p.session_id WHERE p.session_id=$1`, session).Scan(&expected, &actual, &placeholder); err != nil {
		return streaming.NativeDownload{}, err
	}
	if placeholder {
		return streaming.NativeDownload{}, ErrPlaceholder
	}
	if expected == 0 {
		expected, err = watchstate.ExpectedRuntime(ctx, s.pool, v.media)
		if err != nil {
			return streaming.NativeDownload{}, err
		}
		if expected > 0 {
			if _, err = s.pool.Exec(ctx, `UPDATE native_playback_sessions SET expected_duration_ms=$2 WHERE id=$1`, session, expected); err != nil {
				return streaming.NativeDownload{}, err
			}
		}
	}
	if watchstate.ShortRuntime(actual, expected) {
		return streaming.NativeDownload{}, ErrPlaceholder
	}
	if expected > 0 && actual == 0 {
		raw, e := streaming.ProbeFile(ctx, p.Size, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if e := s.Serve(w, r, g, session); e != nil {
				http.Error(w, "source unavailable", 502)
			}
		}))
		if e != nil {
			return streaming.NativeDownload{}, ErrPreparation
		}
		var data struct {
			Format struct {
				Duration string `json:"duration"`
			} `json:"format"`
		}
		if json.Unmarshal(raw, &data) != nil {
			return streaming.NativeDownload{}, ErrIncomplete
		}
		n, e := strconv.ParseFloat(data.Format.Duration, 64)
		if e != nil {
			return streaming.NativeDownload{}, ErrIncomplete
		}
		actual = int64(n * 1000)
		placeholder = watchstate.ShortRuntime(actual, expected)
		if _, err = s.pool.Exec(ctx, `UPDATE native_addon_playback SET observed_duration_ms=$2,placeholder=$3 WHERE session_id=$1`, session, actual, placeholder); err != nil {
			return streaming.NativeDownload{}, err
		}
		if placeholder {
			return streaming.NativeDownload{}, ErrPlaceholder
		}
	}
	d := streaming.DownloadDescriptor(session, p.Size, `"ted-`+p.Fingerprint+`"`)
	d.DurationMS = actual
	d.ExpectedDurationMS = expected
	return d, nil
}
