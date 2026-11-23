package httpapi

import (
	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/addons"
	"entertainmentdistrict/internal/streaming"
	"errors"
	"net/http"
)

func (a *API) nativeCachePlan(w http.ResponseWriter, r *http.Request, g accounts.NativeGrant) {
	var input struct {
		Resume int64 `json:"resume_ms"`
		Ahead  int64 `json:"ahead_bytes"`
		Back   int64 `json:"back_bytes"`
	}
	if !decode(w, r, &input) {
		return
	}
	id := r.PathValue("id")
	if !nativeUUID.MatchString(id) {
		a.nativePlaybackFailure(w, r, streaming.ErrInvalid)
		return
	}
	select {
	case a.nativeCachePlans <- struct{}{}:
		defer func() { <-a.nativeCachePlans }()
	case <-r.Context().Done():
		return
	default:
		w.Header().Set("Retry-After", "60")
		a.nativePlaybackFailure(w, r, streaming.ErrUnavailable)
		return
	}
	failures := make(chan error, 1)
	media := http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		err := a.addons.Serve(w, request, g, id)
		if errors.Is(err, addons.ErrNotAddon) && a.Streaming != nil {
			err = a.Streaming.ServeNativeSession(w, request, g, id)
		}
		if err != nil {
			select {
			case failures <- err:
			default:
			}
			a.nativeSourceFailure(w, request, err)
		}
	})
	plan, err := streaming.BuildNativeCachePlan(r.Context(), media, input.Resume, input.Ahead, input.Back)
	if err != nil {
		select {
		case failure := <-failures:
			a.nativeSourceFailure(w, r, failure)
			return
		default:
		}
		if a.logger != nil && r.Context().Err() == nil {
			a.logger.WarnContext(r.Context(), "next-episode cache planning unavailable", "operation", "native_cache_plan", "session", id, "reason", streaming.CachePlanFailureCode(err))
		}
		if errors.Is(err, streaming.ErrInvalid) {
			a.nativePlaybackFailure(w, r, err)
			return
		}
		// Failing to map a speculative byte window is not proof that MPV
		// cannot play the already validated selected media.
		respond(w, http.StatusServiceUnavailable, map[string]string{"code": "next_cache_unavailable"})
		return
	}
	var expected int64
	if err = a.pool.QueryRow(r.Context(), `SELECT GREATEST(duration_ms,expected_duration_ms) FROM native_playback_sessions WHERE id=$1 AND user_id=$2 AND device_id=$3`, id, g.User.ID, g.DeviceID).Scan(&expected); err != nil {
		a.nativeSourceFailure(w, r, err)
		return
	}
	if expected > 0 && plan.DurationMS*100 <= expected*30 {
		a.nativeSourceFailure(w, r, addons.ErrPlaceholder)
		return
	}
	respond(w, 200, plan)
}
