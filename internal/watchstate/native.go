package watchstate

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/catalog"
	"entertainmentdistrict/internal/database"
	"entertainmentdistrict/internal/sourceprefs"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Playback thresholds apply to live reports and durable offline replay.
const (
	ViewingQualificationPercent = 5
	CompletionPercent           = 95
)

type NativeState struct {
	Media      catalog.MediaIdentity `json:"media"`
	Revision   int64                 `json:"revision"`
	Played     bool                  `json:"played"`
	PositionMS int64                 `json:"position_ms"`
	DurationMS *int64                `json:"duration_ms"`
	PlayCount  int                   `json:"play_count"`
	LastPlayed *time.Time            `json:"last_played"`
}

// EnsureNativeIdentity shares unmapped identity creation with native edits and
// imported watch state. It creates no mapping or library membership.
func EnsureNativeIdentity(ctx context.Context, tx pgx.Tx, media catalog.MediaIdentity) (string, error) {
	if !media.Valid() {
		return "", ErrInvalid
	}
	return importIdentity(ctx, tx, media.ID, []byte(`{}`))
}

func nativeState(media catalog.MediaIdentity, revision int64, state State, duration *int64) NativeState {
	return NativeState{media, revision, state.Played, state.Position / 10000, duration, state.PlayCount, state.LastPlayed}
}

func (s *Store) NativeState(ctx context.Context, user int64, media catalog.MediaIdentity) (NativeState, error) {
	if !media.Valid() {
		return NativeState{}, ErrInvalid
	}
	var state State
	var revision int64
	var duration *int64
	err := s.pool.QueryRow(ctx, `SELECT w.state,w.revision,w.duration_ms FROM watch_current w JOIN catalog_media c ON c.id=w.media_id WHERE w.user_id=$1 AND c.kind=$2 AND c.cinemeta_id=$3`, user, media.Kind, media.ID).Scan(&state, &revision, &duration)
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	}
	return nativeState(media, revision, state, duration), err
}

type NativeEdit struct {
	Media    catalog.MediaIdentity `json:"media"`
	Revision int64                 `json:"revision"`
	Action   string                `json:"action"`
	DateMode string                `json:"date_mode"`
}

func (s *Store) EditNative(ctx context.Context, auth *accounts.NativeAuth, grant accounts.NativeGrant, request NativeEdit) (NativeState, error) {
	if !request.Media.Valid() || request.Revision < 0 || (request.Action != "watched" && request.Action != "unwatched" && request.Action != "reset") || !validDateMode(request.Action == "watched", request.DateMode) {
		return NativeState{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return NativeState{}, err
	}
	defer database.Rollback(tx)
	if err = auth.AuthorizeMutation(ctx, tx, grant); err != nil {
		return NativeState{}, err
	}
	state, err := s.editNativeTx(ctx, tx, grant, request)
	if err != nil {
		return NativeState{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return NativeState{}, err
	}
	return state, nil
}

// Single-episode and whole-show edits use the same revision, history and
// integration effects. The caller owns the transaction and native grant check.
func (s *Store) editNativeTx(ctx context.Context, tx pgx.Tx, grant accounts.NativeGrant, request NativeEdit) (NativeState, error) {
	media, err := importIdentity(ctx, tx, request.Media.ID, []byte(`{}`))
	if err != nil {
		return NativeState{}, err
	}
	var old State
	var revision int64
	var duration *int64
	err = tx.QueryRow(ctx, `SELECT state,revision,duration_ms FROM watch_current WHERE user_id=$1 AND media_id=$2 FOR UPDATE`, grant.User.ID, media).Scan(&old, &revision, &duration)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return NativeState{}, err
	}
	if revision != request.Revision {
		return NativeState{}, ErrConflict
	}
	desired := editedState(old, request.Action, request.DateMode)
	value, err := json.Marshal(desired)
	if err != nil {
		return NativeState{}, err
	}
	var now time.Time
	if err = tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		return NativeState{}, err
	}
	if err = saveCurrent(ctx, tx, grant.User.ID, media, value, "TEDNativeEdit", now); err != nil {
		return NativeState{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO watch_history(user_id,media_id,kind,state,observed_at) VALUES($1,$2,$3,$4,$5)`, grant.User.ID, media, "ted_"+request.Action, value, now); err != nil {
		return NativeState{}, err
	}
	if request.Action != "reset" {
		if err = queueStremio(ctx, tx, grant.User.ID, media, desired.Played); err != nil {
			return NativeState{}, err
		}
	}
	return nativeState(request.Media, revision+1, desired, duration), nil
}

type NativeEvent struct {
	ID         string                `json:"id"`
	SessionID  string                `json:"session_id"`
	Sequence   int64                 `json:"sequence"`
	Media      catalog.MediaIdentity `json:"media"`
	Kind       string                `json:"kind"` // start, progress, complete (explicit Next), stop
	PositionMS int64                 `json:"position_ms"`
	DurationMS int64                 `json:"duration_ms"`
	ObservedAt time.Time             `json:"observed_at"`
	Origin     string                `json:"origin"` // live, offline
	NaturalEOF bool                  `json:"natural_eof"`
}

type NativeEventResult struct {
	ID        string `json:"id"`
	Outcome   string `json:"outcome"`
	Duplicate bool   `json:"duplicate"`
}

func validNativeEvent(e NativeEvent) bool {
	return uuid.MatchString(e.ID) && uuid.MatchString(e.SessionID) && e.Media.Valid() && e.Sequence > 0 &&
		(e.Kind == "start" || e.Kind == "progress" || e.Kind == "stop" || e.Kind == "complete" && e.Media.Kind == "episode") && (e.Origin == "live" || e.Origin == "offline") &&
		(e.Kind == "stop" || !e.NaturalEOF) && e.PositionMS >= 0 && e.PositionMS <= 604800000 && e.DurationMS >= 0 && e.DurationMS <= 604800000 &&
		e.ObservedAt.Year() >= 1970 && e.ObservedAt.Year() <= 2100
}

// IngestNative keeps a durable envelope/outcome for exact replay. Intake is
// ordered per session, and the applied revision fences an older device/session
// after another session or explicit decision updates this user's current row.
func (s *Store) IngestNative(ctx context.Context, auth *accounts.NativeAuth, grant accounts.NativeGrant, events []NativeEvent) ([]NativeEventResult, error) {
	return s.IngestNativeOffline(ctx, auth, grant, events, nil)
}

func (s *Store) IngestNativeOffline(ctx context.Context, auth *accounts.NativeAuth, grant accounts.NativeGrant, events []NativeEvent, sessions []NativeOfflineSession) ([]NativeEventResult, error) {
	if len(events) < 1 || len(events) > 100 {
		return nil, ErrInvalid
	}
	for i := range events {
		if !validNativeEvent(events[i]) {
			return nil, ErrInvalid
		}
		events[i].ObservedAt = events[i].ObservedAt.UTC()
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer database.Rollback(tx)
	if err = auth.AuthorizeMutation(ctx, tx, grant); err != nil {
		return nil, err
	}
	if err = s.registerOffline(ctx, tx, grant, sessions); err != nil {
		return nil, err
	}
	results := make([]NativeEventResult, 0, len(events))
	for _, event := range events {
		out, err := s.applyNative(ctx, tx, grant, event)
		if err != nil {
			return nil, err
		}
		results = append(results, out)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return results, nil
}

func (s *Store) applyNative(ctx context.Context, tx pgx.Tx, grant accounts.NativeGrant, e NativeEvent) (NativeEventResult, error) {
	raw, err := json.Marshal(e)
	if err != nil {
		return NativeEventResult{}, err
	}
	var same bool
	var outcome string
	err = tx.QueryRow(ctx, `SELECT envelope=$3::jsonb,outcome FROM native_watch_events WHERE id=$1 AND user_id=$2`, e.ID, grant.User.ID, raw).Scan(&same, &outcome)
	if err == nil {
		if !same {
			return NativeEventResult{}, ErrConflict
		}
		// A new login on this device may replay its own retained queue, but one
		// device cannot use another's event receipt as an authorization shortcut.
		var owned bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM native_playback_sessions WHERE id=$1 AND user_id=$2 AND device_id=$3)`, e.SessionID, grant.User.ID, grant.DeviceID).Scan(&owned); err != nil {
			return NativeEventResult{}, err
		}
		if !owned {
			return NativeEventResult{}, accounts.ErrNotFound
		}
		return NativeEventResult{e.ID, outcome, true}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return NativeEventResult{}, err
	}
	var media, kind, identity string
	var sequence, applied, duration, expected int64
	var addon bool
	var completed, started, stopped bool
	var last *time.Time
	var now time.Time
	var origin string
	var previous *string
	var registration []byte
	err = tx.QueryRow(ctx, `SELECT p.media_id::text,c.kind,c.cinemeta_id,p.last_sequence,p.applied_revision,p.duration_ms,p.completed,p.started,p.stopped,p.last_event_at,clock_timestamp(),p.origin,p.previous_offline_session::text,p.offline_registration,p.expected_duration_ms,p.addon_source_id IS NOT NULL
 FROM native_playback_sessions p JOIN catalog_media c ON c.id=p.media_id
 WHERE p.id=$1 AND p.user_id=$2 AND p.device_id=$3 FOR UPDATE OF p`, e.SessionID, grant.User.ID, grant.DeviceID).Scan(&media, &kind, &identity, &sequence, &applied, &duration, &completed, &started, &stopped, &last, &now, &origin, &previous, &registration, &expected, &addon)
	if errors.Is(err, pgx.ErrNoRows) {
		return NativeEventResult{}, accounts.ErrNotFound
	}
	if err != nil {
		return NativeEventResult{}, err
	}
	if kind != e.Media.Kind || identity != e.Media.ID || e.ObservedAt.After(now.Add(5*time.Minute)) {
		return NativeEventResult{}, ErrInvalid
	}
	if origin == "offline" {
		var registered NativeOfflineSession
		if json.Unmarshal(registration, &registered) != nil {
			return NativeEventResult{}, ErrInvalid
		}
		if e.Origin != "offline" || e.ObservedAt.Before(registered.StartedAt) || (sequence == 0 && (e.Kind != "start" || !e.ObservedAt.Equal(registered.StartedAt))) {
			return NativeEventResult{}, ErrInvalid
		}
	}
	if e.Sequence != sequence+1 || stopped {
		return NativeEventResult{}, ErrConflict
	}
	if expected == 0 {
		expected, err = ExpectedRuntime(ctx, tx, e.Media)
		if err != nil {
			return NativeEventResult{}, err
		}
	}
	actual := e.DurationMS
	if actual == 0 {
		actual = duration
	}
	if ShortRuntime(actual, expected) || addon && expected > 0 && actual == 0 {
		// A receipt settles the immutable report without creating viewing history,
		// changing resume/watch state, recents or outgoing integration flags.
		if _, err = tx.Exec(ctx, `INSERT INTO native_watch_events(id,user_id,session_id,sequence,envelope,outcome) VALUES($1,$2,$3,$4,$5,'ignored')`, e.ID, grant.User.ID, e.SessionID, e.Sequence, raw); err != nil {
			return NativeEventResult{}, err
		}
		if _, err = tx.Exec(ctx, `UPDATE native_playback_sessions SET last_sequence=$2,last_event_at=$3,stopped=$4,duration_ms=$5,expected_duration_ms=$6 WHERE id=$1`, e.SessionID, e.Sequence, e.ObservedAt, e.Kind == "stop" || e.Kind == "complete", actual, expected); err != nil {
			return NativeEventResult{}, err
		}
		return NativeEventResult{e.ID, "ignored", false}, nil
	}
	var old State
	var revision int64
	var observed time.Time
	err = tx.QueryRow(ctx, `SELECT state,revision,observed_at FROM watch_current WHERE user_id=$1 AND media_id=$2 FOR UPDATE`, grant.User.ID, media).Scan(&old, &revision, &observed)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return NativeEventResult{}, err
	}
	// Consecutive disconnected viewings may share a downloaded baseline. Only
	// the explicit same-device predecessor's exact applied revision can bridge
	// that baseline; an intervening edit/other device remains authoritative.
	if origin == "offline" && sequence == 0 && previous != nil {
		var parentRevision int64
		var parentStopped bool
		var parentAt *time.Time
		if err = tx.QueryRow(ctx, `SELECT applied_revision,stopped,last_event_at FROM native_playback_sessions WHERE id=$1 AND user_id=$2 AND device_id=$3 AND media_id=$4`, *previous, grant.User.ID, grant.DeviceID, media).Scan(&parentRevision, &parentStopped, &parentAt); err != nil {
			return NativeEventResult{}, err
		}
		if parentStopped && parentRevision == revision && parentAt != nil && !e.ObservedAt.Before(*parentAt) {
			applied = parentRevision
		}
	}
	outcome = "applied"
	if revision != applied || e.ObservedAt.Before(observed) || last != nil && e.ObservedAt.Before(*last) {
		outcome = "stale"
	}
	// A brief preview is not viewing history or resume progress. Keep its
	// immutable receipt and ordering so offline replay remains safe, but do not
	// publish state until this session reaches 5%. Once qualified, seeking
	// backwards remains legitimate progress. Explicit Next is a user decision.
	qualificationDuration := duration
	if qualificationDuration == 0 {
		qualificationDuration = actual
	}
	qualified := started || e.Kind == "complete" || qualificationDuration > 0 && e.PositionMS*100 >= qualificationDuration*ViewingQualificationPercent || qualificationDuration == 0 && e.NaturalEOF
	if !qualified {
		if _, err = tx.Exec(ctx, `INSERT INTO native_watch_events(id,user_id,session_id,sequence,envelope,outcome) VALUES($1,$2,$3,$4,$5,'ignored')`, e.ID, grant.User.ID, e.SessionID, e.Sequence, raw); err != nil {
			return NativeEventResult{}, err
		}
		lastAt := e.ObservedAt
		if last != nil && last.After(lastAt) {
			lastAt = *last
		}
		if _, err = tx.Exec(ctx, `UPDATE native_playback_sessions SET last_sequence=$2,last_event_at=$3,stopped=$4,duration_ms=$5,applied_revision=$6 WHERE id=$1`, e.SessionID, e.Sequence, lastAt, e.Kind == "stop", qualificationDuration, applied); err != nil {
			return NativeEventResult{}, err
		}
		return NativeEventResult{e.ID, "ignored", false}, nil
	}
	if _, err = tx.Exec(ctx, `INSERT INTO native_watch_events(id,user_id,session_id,sequence,envelope,outcome) VALUES($1,$2,$3,$4,$5,$6)`, e.ID, grant.User.ID, e.SessionID, e.Sequence, raw, outcome); err != nil {
		var pe *pgconn.PgError
		if errors.As(err, &pe) && pe.Code == "23505" {
			return NativeEventResult{}, ErrConflict
		}
		return NativeEventResult{}, err
	}
	// Historical intake uses the same completion policy but cannot modify the
	// newer current decision or send a stale flag to Stremio.
	desired := old
	if outcome == "stale" {
		desired = State{Played: completed}
		if completed {
			desired.PlayCount = 1
		}
	}
	if duration == 0 && e.DurationMS > 0 {
		duration = e.DurationMS
	}
	position := e.PositionMS
	if duration > 0 {
		position = min(position, duration)
	}
	desired.Position = position * 10000
	desired.LastPlayed = &e.ObservedAt
	// A transport can emit EOF after only part of a known-length movie. It is
	// an interruption, not evidence that the user finished the title.
	// Next Episode is a deliberate completion action, with the actual offset
	// retained in the immutable report. It never fabricates viewing duration.
	complete := e.Kind == "complete" || e.Kind != "start" && (duration > 0 && e.PositionMS*100 >= duration*CompletionPercent || duration == 0 && e.NaturalEOF)
	historyKind := ""
	if !started {
		if err = sourceprefs.RecordStarted(ctx, tx, e.SessionID, e.ID, e.ObservedAt); err != nil {
			return NativeEventResult{}, err
		}
		started = true
		historyKind = "playback_started"
	}
	completedNow := complete && !completed
	if completedNow {
		desired.Played = true
		desired.PlayCount = min(desired.PlayCount+1, 2147483647)
		completed = true
		historyKind = "playback_completed"
	} else if e.Kind == "stop" {
		historyKind = "playback_stopped"
	} else if e.Kind == "complete" {
		// An explicit Next action after the 95% event still supplies a new,
		// legitimate recency anchor without counting the same viewing twice.
		historyKind = "ted_watched"
	}
	if completed {
		desired.Position = 0
	}
	value, err := json.Marshal(desired)
	if err != nil {
		return NativeEventResult{}, err
	}
	if outcome == "applied" {
		if err = saveCurrent(ctx, tx, grant.User.ID, media, value, "TEDNativePlayback", e.ObservedAt); err != nil {
			return NativeEventResult{}, err
		}
		if _, err = tx.Exec(ctx, `UPDATE watch_current SET duration_ms=NULLIF($3,0) WHERE user_id=$1 AND media_id=$2`, grant.User.ID, media, duration); err != nil {
			return NativeEventResult{}, err
		}
		if completedNow {
			if err = queueStremio(ctx, tx, grant.User.ID, media, true); err != nil {
				return NativeEventResult{}, err
			}
		}
		applied = revision + 1
	}
	if historyKind != "" {
		if _, err = tx.Exec(ctx, `INSERT INTO watch_history(user_id,media_id,native_event_id,kind,state,observed_at) VALUES($1,$2,$3,$4,$5,$6)`, grant.User.ID, media, e.ID, historyKind, value, e.ObservedAt); err != nil {
			return NativeEventResult{}, err
		}
	}
	lastAt := e.ObservedAt
	if last != nil && last.After(lastAt) {
		lastAt = *last
	}
	if _, err = tx.Exec(ctx, `UPDATE native_playback_sessions SET last_sequence=$2,applied_revision=$3,last_event_at=$4,completed=$5,started=$6,stopped=$7,duration_ms=$8 WHERE id=$1`, e.SessionID, e.Sequence, applied, lastAt, completed, started, e.Kind == "stop" || e.Kind == "complete", duration); err != nil {
		return NativeEventResult{}, err
	}

	return NativeEventResult{e.ID, outcome, false}, nil
}

func saveCurrent(ctx context.Context, tx pgx.Tx, user int64, media string, value []byte, source string, at time.Time) error {
	_, err := tx.Exec(ctx, `INSERT INTO watch_current(user_id,media_id,state,source,observed_at) VALUES($1,$2,$3,$4,$5) ON CONFLICT(user_id,media_id) DO UPDATE SET state=EXCLUDED.state,source=EXCLUDED.source,observed_at=EXCLUDED.observed_at,updated_at=now(),revision=watch_current.revision+1,needs_review=false`, user, media, value, source, at)
	return err
}
