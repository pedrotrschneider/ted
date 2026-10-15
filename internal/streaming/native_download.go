package streaming

import (
	"context"
	"crypto/sha256"
	"entertainmentdistrict/internal/accounts"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
)

type NativeDownload struct {
	SessionID          string `json:"session_id"`
	MediaURL           string `json:"media_url"`
	Bytes              int64  `json:"bytes"`
	ETag               string `json:"etag"`
	DurationMS         int64  `json:"duration_ms,omitempty"`
	ExpectedDurationMS int64  `json:"expected_duration_ms,omitempty"`
}

func DownloadDescriptor(session string, size int64, etag string) NativeDownload {
	return NativeDownload{SessionID: session, MediaURL: "/api/client/v1/downloads/" + session + "/media", Bytes: size, ETag: etag}
}

// The mapping version pins immutable file identity; recovery must already
// verify that saved hash/path/size before serving any replacement RD link.
func (s *Service) NativeDownload(ctx context.Context, g accounts.NativeGrant, session string) (NativeDownload, error) {
	if !uuidPattern.MatchString(session) {
		return NativeDownload{}, ErrInvalid
	}
	var media, mapping string
	err := s.pool.QueryRow(ctx, `SELECT media_id::text,mapping_id::text FROM native_playback_sessions WHERE id=$1 AND user_id=$2 AND device_id=$3 AND origin='live' AND mapping_id IS NOT NULL`, session, g.User.ID, g.DeviceID).Scan(&media, &mapping)
	if errors.Is(err, pgx.ErrNoRows) {
		return NativeDownload{}, accounts.ErrNotFound
	}
	if err != nil {
		return NativeDownload{}, err
	}
	m, err := s.media(ctx, media, true)
	if err != nil {
		return NativeDownload{}, err
	}
	if m.MappingID != mapping {
		return NativeDownload{}, ErrReleaseChanged
	}
	if m.Bytes <= 0 {
		return NativeDownload{}, ErrProbeInvalid
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", mapping, m.Bytes)))
	return DownloadDescriptor(session, m.Bytes, fmt.Sprintf(`"ted-%x"`, sum)), nil
}
