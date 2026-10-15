package streaming

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"time"

	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/catalog"
	"entertainmentdistrict/internal/database"
	"entertainmentdistrict/internal/watchstate"
	"github.com/jackc/pgx/v5"
)

var ErrReleaseChanged = errors.New("playback mapping changed; select the source again")

type NativeSession struct {
	OriginalLanguage   string                `json:"original_language,omitempty"`
	ID                 string                `json:"id"`
	Media              catalog.MediaIdentity `json:"media"`
	MediaURL           string                `json:"media_url"`
	Source             string                `json:"source"`
	AddonID            string                `json:"addon_id,omitempty"`
	BingeKey           string                `json:"binge_key,omitempty"`
	DurationMS         int64                 `json:"duration_ms"`
	ExpectedDurationMS int64                 `json:"expected_duration_ms,omitempty"`
	ResumeMS           int64                 `json:"resume_ms"`
	WatchRevision      int64                 `json:"watch_revision"`
	AudioLanguages     []string              `json:"audio_languages"`
	// ffprobe output is bounded and contains technical fields only. Provider
	// URLs, headers, account IDs and torrent provenance are never returned.
	Technical json.RawMessage  `json:"technical"`
	Subtitles []NativeSubtitle `json:"subtitles,omitempty"`
}
type NativeSubtitle struct {
	URL      string `json:"url"`
	Language string `json:"language"`
	Label    string `json:"label"`
	Codec    string `json:"codec,omitempty"`
}

// Return only known technical fields, even if an older/imported probe contains
// filename, provider data or future fields. Durable originals stay untouched.
func nativeTechnical(raw []byte) (json.RawMessage, error) {
	if len(raw) > 2<<20 {
		return nil, ErrProbeInvalid
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(raw, &root) != nil {
		return nil, ErrProbeInvalid
	}
	filter := func(raw json.RawMessage, keys []string) (map[string]json.RawMessage, error) {
		var source map[string]json.RawMessage
		if json.Unmarshal(raw, &source) != nil {
			return nil, ErrProbeInvalid
		}
		value := make(map[string]json.RawMessage)
		for _, key := range keys {
			if field, ok := source[key]; ok {
				value[key] = field
			}
		}
		return value, nil
	}
	format, err := filter(root["format"], []string{"format_name", "duration", "size", "bit_rate"})
	if err != nil {
		return nil, err
	}
	var streams []json.RawMessage
	if json.Unmarshal(root["streams"], &streams) != nil || len(streams) > 512 {
		return nil, ErrProbeInvalid
	}
	values := make([]map[string]json.RawMessage, 0, len(streams))
	for _, raw := range streams {
		value, err := filter(raw, []string{"index", "codec_type", "codec_name", "codec_tag_string", "width", "height", "channels", "channel_layout", "sample_rate", "bit_rate", "profile", "level", "pix_fmt", "bits_per_raw_sample", "avg_frame_rate", "r_frame_rate", "color_range", "color_space", "color_transfer", "color_primaries"})
		if err != nil {
			return nil, err
		}
		var original map[string]json.RawMessage
		if json.Unmarshal(raw, &original) != nil {
			return nil, ErrProbeInvalid
		}
		for _, nested := range []struct {
			name string
			keys []string
		}{{"tags", []string{"language", "title"}}, {"disposition", []string{"default", "forced"}}} {
			if field, ok := original[nested.name]; ok {
				filtered, err := filter(field, nested.keys)
				if err != nil {
					return nil, err
				}
				encoded, err := json.Marshal(filtered)
				if err != nil {
					return nil, err
				}
				value[nested.name] = encoded
			}
		}
		values = append(values, value)
	}
	return json.Marshal(map[string]any{"format": format, "streams": values})
}

func nativeDuration(probe []byte) (int64, error) {
	var data struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(probe, &data); err != nil {
		return 0, ErrProbeInvalid
	}
	n, err := strconv.ParseFloat(data.Format.Duration, 64)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n <= 0 || n > 604800 {
		return 0, ErrProbeInvalid
	}
	return max(1, int64(math.Round(n*1000))), nil
}

func (s *Service) CreateNativeSession(ctx context.Context, auth *accounts.NativeAuth, grant accounts.NativeGrant, media catalog.MediaIdentity) (NativeSession, error) {
	if !media.Valid() {
		return NativeSession{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return NativeSession{}, err
	}
	defer database.Rollback(tx)
	if err = auth.AuthorizeMutation(ctx, tx, grant); err != nil {
		return NativeSession{}, err
	}
	var id, mapping string
	var probe json.RawMessage
	var local bool
	err = tx.QueryRow(ctx, `SELECT c.id::text,m.id::text,s.probe,COALESCE(l.available,false)
 FROM catalog_media c JOIN media_mappings m ON m.movie_id=c.id JOIN streaming_items s ON s.movie_id=c.id AND s.mapping_id=m.id
 LEFT JOIN local_files l ON l.movie_id=c.id WHERE c.cinemeta_id=$1 AND c.kind=$2 AND s.published AND s.phase='ready' FOR SHARE OF m,s`, media.ID, media.Kind).Scan(&id, &mapping, &probe, &local)
	if errors.Is(err, pgx.ErrNoRows) {
		return NativeSession{}, ErrNotReady
	}
	if err != nil {
		return NativeSession{}, err
	}
	duration, err := nativeDuration(probe)
	if err != nil {
		return NativeSession{}, err
	}
	technical, err := nativeTechnical(probe)
	if err != nil {
		return NativeSession{}, err
	}
	var category string
	if err = tx.QueryRow(ctx, `SELECT COALESCE((SELECT sh.category FROM catalog_episodes e JOIN catalog_seasons se ON se.id=e.season_id JOIN catalog_series sh ON sh.id=se.series_id WHERE e.media_id=$1),'movie')`, id).Scan(&category); err != nil {
		return NativeSession{}, err
	}
	languages := []string{"en"}
	if category == "anime" {
		languages = []string{"ja", "zh", "ko", "en"}
	}
	var state watchstate.State
	var revision int64
	err = tx.QueryRow(ctx, `SELECT state,revision FROM watch_current WHERE user_id=$1 AND media_id=$2`, grant.User.ID, id).Scan(&state, &revision)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return NativeSession{}, err
	}
	var session string
	err = tx.QueryRow(ctx, `INSERT INTO native_playback_sessions(user_id,family_id,device_id,media_id,mapping_id,duration_ms,applied_revision)
 VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id::text`, grant.User.ID, grant.FamilyID, grant.DeviceID, id, mapping, duration, revision).Scan(&session)
	if err != nil {
		return NativeSession{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return NativeSession{}, err
	}
	source := "mapped_rd"
	if local && s.storage != nil {
		source = "local"
	}
	resume := state.Position / 10000
	// Watched flags describe past viewing; an unfinished rewatch still resumes.
	if resume >= duration {
		resume = 0
	}
	return NativeSession{ID: session, Media: media, MediaURL: "/api/client/v1/playback/sessions/" + session + "/media", Source: source, DurationMS: duration, ResumeMS: resume, WatchRevision: revision, AudioLanguages: languages, Technical: technical}, nil
}

// ServeNativeSession checks current access at the HTTP boundary, then pins the
// mapped release on every range/reconnect. Session IDs never authorize playback.
func (s *Service) ServeNativeSession(w http.ResponseWriter, r *http.Request, grant accounts.NativeGrant, session string) error {
	if !uuidPattern.MatchString(session) {
		return ErrInvalid
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	var id, mapping string
	err := s.pool.QueryRow(ctx, `SELECT media_id::text,mapping_id::text FROM native_playback_sessions WHERE id=$1 AND user_id=$2 AND device_id=$3 AND mapping_id IS NOT NULL`, session, grant.User.ID, grant.DeviceID).Scan(&id, &mapping)
	cancel()
	if errors.Is(err, pgx.ErrNoRows) {
		return accounts.ErrNotFound
	}
	if err != nil {
		return err
	}
	ctx, cancel = context.WithTimeout(r.Context(), 8*time.Second)
	m, err := s.media(ctx, id, true)
	cancel()
	if err != nil {
		return err
	}
	if m.MappingID != mapping {
		return ErrReleaseChanged
	}
	// Explicit remapping may interrupt a body; new
	// requests refuse a different cut. File locks still protect local playback.
	s.playPinnedMedia(w, r, m, mapping)
	return nil
}
