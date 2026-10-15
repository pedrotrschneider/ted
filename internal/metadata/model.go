// Package metadata owns TED's on-demand descriptive metadata and artwork cache.
// Descriptive DTO fields are stable across providers.
// TED identity, provider state and image URLs belong to the surrounding envelope.
package metadata

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var (
	ErrInvalid      = errors.New("invalid metadata request")
	ErrPartial      = errors.New("incomplete metadata response")
	ErrMissing      = errors.New("metadata not found")
	ErrAmbiguous    = errors.New("metadata identity ambiguous")
	ErrUnconfigured = errors.New("metadata provider not configured")
	ErrProvider     = errors.New("metadata provider unavailable")
	ErrImage        = errors.New("metadata image invalid or unsupported")
)
var uuid = regexp.MustCompile(`^[a-f0-9]{8}(-[a-f0-9]{4}){3}-[a-f0-9]{12}$`)
var titleID = regexp.MustCompile(`^tt[0-9]{7,12}$`)
var positiveID = regexp.MustCompile(`^[1-9][0-9]{0,14}$`)

type Person struct {
	Name            string `json:"Name"`
	ID              string `json:"Id"`
	Role            string `json:"Role,omitempty"`
	Type            string `json:"Type"`
	PrimaryImageTag string `json:"PrimaryImageTag,omitempty"`
}
type NameID struct {
	Name string `json:"Name"`
	ID   string `json:"Id"`
}
type URL struct {
	Name string `json:"Name,omitempty"`
	URL  string `json:"Url"`
}
type Item struct {
	ID                           string            `json:"Id,omitempty"`
	Type                         string            `json:"Type"`
	Name                         string            `json:"Name,omitempty"`
	OriginalTitle                string            `json:"OriginalTitle,omitempty"`
	SortName                     string            `json:"SortName,omitempty"`
	Overview                     string            `json:"Overview,omitempty"`
	Taglines                     []string          `json:"Taglines,omitempty"`
	Genres                       []string          `json:"Genres,omitempty"`
	Tags                         []string          `json:"Tags,omitempty"`
	Studios                      []NameID          `json:"Studios,omitempty"`
	ProductionLocations          []string          `json:"ProductionLocations,omitempty"`
	CommunityRating              *float64          `json:"CommunityRating,omitempty"`
	CriticRating                 *float64          `json:"CriticRating,omitempty"`
	OfficialRating               string            `json:"OfficialRating,omitempty"`
	CustomRating                 string            `json:"CustomRating,omitempty"`
	PremiereDate                 *time.Time        `json:"PremiereDate,omitempty"`
	EndDate                      *time.Time        `json:"EndDate,omitempty"`
	ProductionYear               *int              `json:"ProductionYear,omitempty"`
	RunTimeTicks                 *int64            `json:"RunTimeTicks,omitempty"`
	ProviderIDs                  map[string]string `json:"ProviderIds,omitempty"`
	People                       []Person          `json:"People,omitempty"`
	RemoteTrailers               []URL             `json:"RemoteTrailers,omitempty"`
	ExternalURLs                 []URL             `json:"ExternalUrls,omitempty"`
	ParentID                     string            `json:"ParentId,omitempty"`
	SeriesID                     string            `json:"SeriesId,omitempty"`
	SeriesName                   string            `json:"SeriesName,omitempty"`
	SeasonID                     string            `json:"SeasonId,omitempty"`
	SeasonName                   string            `json:"SeasonName,omitempty"`
	IndexNumber                  *int              `json:"IndexNumber,omitempty"`
	ParentIndexNumber            *int              `json:"ParentIndexNumber,omitempty"`
	Status                       string            `json:"Status,omitempty"`
	AirDays                      []string          `json:"AirDays,omitempty"`
	AirTime                      string            `json:"AirTime,omitempty"`
	ImageTags                    map[string]string `json:"ImageTags,omitempty"`
	BackdropImageTags            []string          `json:"BackdropImageTags,omitempty"`
	PreferredMetadataLanguage    string            `json:"PreferredMetadataLanguage,omitempty"`
	PreferredMetadataCountryCode string            `json:"PreferredMetadataCountryCode,omitempty"`
	LockData                     *bool             `json:"LockData,omitempty"`
	LockedFields                 []string          `json:"LockedFields,omitempty"`
}
type Request struct {
	OriginalLanguage         string          `json:"original_language,omitempty"`
	OriginalLanguageChecked  bool            `json:"original_language_checked,omitempty"`
	Kind                     string          `json:"kind"`
	TitleID                  string          `json:"title_id,omitempty"`
	ProviderID               string          `json:"provider_id,omitempty"`
	ParentProviderID         string          `json:"parent_provider_id,omitempty"`
	Season                   int             `json:"season,omitempty"`
	Number                   int             `json:"number,omitempty"`
	EpisodeName              string          `json:"episode_name,omitempty"`
	EpisodeDate              string          `json:"episode_date,omitempty"`
	TMDBBiographyChecked     bool            `json:"tmdb_biography_checked,omitempty"`
	TMDBBiographyNameChecked bool            `json:"tmdb_biography_name_checked,omitempty"`
	TVDBCastTypesChecked     bool            `json:"tvdb_cast_types_checked,omitempty"`
	TVDBAppearanceChecked    bool            `json:"tvdb_appearance_checked,omitempty"`
	BiographyMatch           *BiographyMatch `json:"biography_match,omitempty"`
}

// BiographyMatch describes a heuristic text source, never a verified person ID.
type BiographyMatch struct {
	Provider   string `json:"provider"`
	ProviderID string `json:"provider_id"`
	Method     string `json:"method"`
	Name       string `json:"name"`
}
type PersonSummary struct{ Provider, ID, Name, Role, Type, Image string }
type Image struct {
	Type, URL string
	Index     int
}
type Credit struct {
	Provider, Kind, ID, TitleID string
	Item                        Item
	Poster, CharacterImage      string
	Roles                       []string
}
type Document struct {
	OriginalLanguage         string
	OriginalLanguageChecked  bool
	Item                     Item
	FieldSources             map[string]string
	TMDBBiographyChecked     bool
	TMDBBiographyNameChecked bool
	TVDBCastTypesChecked     bool
	TVDBAppearanceChecked    bool
	BiographyMatch           *BiographyMatch
	ProviderID               string
	People                   []PersonSummary // nil means not fetched; empty means a confirmed complete list.
	Credits                  []Credit
	Images                   []Image
	Children                 []Child
}
type Child struct {
	Request Request
	Image   string
}
type Reader interface {
	Fetch(context.Context, string, Request) (Document, error)
	ResolveCredit(context.Context, string, string, string) (string, error)
	Enabled(string) bool
}
type Asset struct {
	ID           string `json:"id"`
	Type         string `json:"type"`
	Index        int    `json:"index"`
	Status       string `json:"status"`
	Tag          string `json:"tag,omitempty"`
	Error        string `json:"error_code,omitempty"`
	URL          string `json:"url"`
	Generation   int64  `json:"generation"`
	UserModified bool   `json:"user_modified"`
}
type Snapshot struct {
	OriginalLanguage string            `json:"original_language,omitempty"`
	Item             Item              `json:"item"`
	Status           string            `json:"status"`
	Provider         string            `json:"provider"`
	Generation       int64             `json:"generation"`
	FetchedAt        *time.Time        `json:"fetched_at,omitempty"`
	Error            string            `json:"error_code,omitempty"`
	Images           []Asset           `json:"images"`
	FieldSources     map[string]string `json:"field_sources"`
	BiographyMatch   *BiographyMatch   `json:"biography_match,omitempty"`
}

func code(err error) string {
	var rejected unauthorized
	if errors.As(err, &rejected) {
		return "provider_credentials_rejected"
	}
	switch {
	case errors.Is(err, ErrPartial):
		return "incomplete_response"
	case errors.Is(err, ErrMissing):
		return "metadata_not_found"
	case errors.Is(err, ErrAmbiguous):
		return "identity_ambiguous"
	case errors.Is(err, ErrUnconfigured):
		return "provider_not_configured"
	case errors.Is(err, ErrInvalid):
		return "invalid_request"
	case errors.Is(err, ErrImage):
		return "unsupported_artwork"
	default:
		return "provider_unavailable"
	}
}
func guid(key string) string {
	b := sha256.Sum256([]byte("TED metadata\x00" + key))
	b[6] = b[6]&15 | 128
	b[8] = b[8]&63 | 128
	v := hex.EncodeToString(b[:16])
	return fmt.Sprintf("%s-%s-%s-%s-%s", v[:8], v[8:12], v[12:16], v[16:20], v[20:])
}
func date(raw string) *time.Time {
	for _, f := range []string{time.DateOnly, time.RFC3339} {
		if t, e := time.Parse(f, raw); e == nil {
			return &t
		}
	}
	return nil
}
func year(raw string) *int {
	if t := date(raw); t != nil {
		v := t.Year()
		return &v
	}
	return nil
}
func ticks(minutes int) *int64 {
	if minutes <= 0 || minutes > 100000 {
		return nil
	}
	v := int64(minutes) * 60 * 10000000
	return &v
}

func originalLanguage(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if !regexp.MustCompile(`^[a-z]{2,3}([_-][a-z0-9]{2,8})*$`).MatchString(value) {
		return ""
	}
	return strings.Split(strings.Split(value, "-")[0], "_")[0]
}
