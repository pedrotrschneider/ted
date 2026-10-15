package config

import (
	"errors"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"

	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/metadata"
)

type Config struct {
	CalendarLocation    *time.Location
	DatabaseURL, Listen string
	InsecureHTTP        bool
	MinPasswordLength   int
	RealDebridAPIKey    string
	MediaDir            string
	MappingWorkers      int
	MappingBatchSize    int
	NativeAuth          accounts.NativePolicy
	Metadata            metadata.ProviderConfig
	MetadataDir         string
}

func Load() (Config, error) {
	c := Config{MappingWorkers: 4, MappingBatchSize: 200, NativeAuth: accounts.DefaultNativePolicy(), DatabaseURL: os.Getenv("TED_DATABASE_URL"), Listen: os.Getenv("TED_LISTEN")}
	for _, field := range []struct {
		name  string
		value *time.Duration
	}{{"TED_ACCESS_TOKEN_TTL", &c.NativeAuth.AccessTTL}, {"TED_REFRESH_TOKEN_IDLE_TTL", &c.NativeAuth.RefreshIdleTTL}, {"TED_TOKEN_FAMILY_TTL", &c.NativeAuth.FamilyTTL}} {
		if raw := os.Getenv(field.name); raw != "" {
			value, err := time.ParseDuration(raw)
			if err != nil {
				return c, errors.New(field.name + " must be a Go duration (for example 10m or 720h)")
			}
			*field.value = value
		}
	}
	if err := c.NativeAuth.Validate(); err != nil {
		return c, err
	}
	if file := os.Getenv("TED_DATABASE_URL_FILE"); file != "" {
		if c.DatabaseURL != "" {
			return c, errors.New("set only one of TED_DATABASE_URL and TED_DATABASE_URL_FILE")
		}
		b, err := os.ReadFile(file)
		if err != nil {
			return c, errors.New("cannot read TED_DATABASE_URL_FILE")
		}
		c.DatabaseURL = strings.TrimSpace(string(b))
	}
	zone := os.Getenv("TED_TIMEZONE")
	if zone == "" {
		zone = "UTC"
	}
	var zoneErr error
	c.CalendarLocation, zoneErr = time.LoadLocation(zone)
	if zoneErr != nil {
		return c, errors.New("TED_TIMEZONE must be an IANA timezone")
	}
	if c.DatabaseURL == "" {
		return c, errors.New("TED_DATABASE_URL or TED_DATABASE_URL_FILE is required")
	}
	if c.Listen == "" {
		c.Listen = ":8080"
	}
	if _, _, err := net.SplitHostPort(c.Listen); err != nil {
		return c, errors.New("TED_LISTEN must be host:port")
	}
	if value := os.Getenv("TED_INSECURE_HTTP"); value != "" {
		var err error
		c.InsecureHTTP, err = strconv.ParseBool(value)
		if err != nil {
			return c, errors.New("TED_INSECURE_HTTP must be true or false")
		}
	}
	if value := os.Getenv("TED_MIN_PASSWORD_LENGTH"); value != "" {
		var err error
		c.MinPasswordLength, err = strconv.Atoi(value)
		if err != nil || c.MinPasswordLength < 0 || c.MinPasswordLength > 1024 {
			return c, errors.New("TED_MIN_PASSWORD_LENGTH must be between 0 and 1024")
		}
	}
	if value := os.Getenv("TED_MAPPING_WORKERS"); value != "" {
		var err error
		c.MappingWorkers, err = strconv.Atoi(value)
		if err != nil || c.MappingWorkers < 1 || c.MappingWorkers > 4 {
			return c, errors.New("TED_MAPPING_WORKERS must be between 1 and 4")
		}
	}
	if value := os.Getenv("TED_MAPPING_BATCH_SIZE"); value != "" {
		var err error
		c.MappingBatchSize, err = strconv.Atoi(value)
		if err != nil || c.MappingBatchSize < 1 || c.MappingBatchSize > 500 {
			return c, errors.New("TED_MAPPING_BATCH_SIZE must be between 1 and 500")
		}
	}
	c.RealDebridAPIKey = strings.TrimSpace(os.Getenv("TED_REAL_DEBRID_API_KEY"))
	c.MediaDir = os.Getenv("TED_MEDIA_DIR")
	c.MetadataDir = os.Getenv("TED_METADATA_DIR")
	c.Metadata = metadata.ProviderConfig{TMDBToken: strings.TrimSpace(os.Getenv("TED_TMDB_ACCESS_TOKEN")), TVDBKey: strings.TrimSpace(os.Getenv("TED_TVDB_API_KEY")), TVDBPIN: strings.TrimSpace(os.Getenv("TED_TVDB_PIN")), Language: os.Getenv("TED_METADATA_LANGUAGE"), Country: os.Getenv("TED_METADATA_COUNTRY")}
	for _, field := range []struct{ name, value string }{{"TED_TMDB_ACCESS_TOKEN", c.Metadata.TMDBToken}, {"TED_TVDB_API_KEY", c.Metadata.TVDBKey}, {"TED_TVDB_PIN", c.Metadata.TVDBPIN}} {
		if len(field.value) > 8192 || strings.ContainsAny(field.value, "\r\n\t ") {
			return c, errors.New(field.name + " has an invalid format")
		}
	}
	if c.Metadata.Language == "" {
		c.Metadata.Language = "en"
	}
	if c.Metadata.Country == "" {
		c.Metadata.Country = "US"
	}
	if !validLanguage(c.Metadata.Language) {
		return c, errors.New("TED_METADATA_LANGUAGE must be a supported language code (en, pt, es, fr, de, ja, zh, ko), optionally followed by a region")
	}
	if len(c.Metadata.Country) != 2 || strings.Trim(c.Metadata.Country, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") != "" {
		return c, errors.New("TED_METADATA_COUNTRY must be a two-letter uppercase country code")
	}
	if len(c.RealDebridAPIKey) > 4096 || strings.ContainsAny(c.RealDebridAPIKey, "\r\n\t ") {
		return c, errors.New("TED_REAL_DEBRID_API_KEY has an invalid format")
	}
	return c, nil
}

func validLanguage(value string) bool {
	parts := strings.Split(value, "-")
	if len(parts) > 2 || len(parts) == 2 && (len(parts[1]) != 2 || strings.Trim(parts[1], "ABCDEFGHIJKLMNOPQRSTUVWXYZ") != "") {
		return false
	}
	switch parts[0] {
	case "en", "pt", "es", "fr", "de", "ja", "zh", "ko":
		return true
	}
	return false
}
