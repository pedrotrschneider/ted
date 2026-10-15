package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSecretFileAndSecureDefault(t *testing.T) {
	t.Setenv("TED_DATABASE_URL", "")
	t.Setenv("TED_DATABASE_URL_FILE", "")
	t.Setenv("TED_LISTEN", "")
	t.Setenv("TED_INSECURE_HTTP", "")
	path := filepath.Join(t.TempDir(), "database-url")
	if err := os.WriteFile(path, []byte("postgres://ted:secret@db/ted\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TED_DATABASE_URL_FILE", path)
	c, err := Load()
	if err != nil || c.InsecureHTTP || c.Listen != ":8080" || strings.HasSuffix(c.DatabaseURL, "\n") {
		t.Fatalf("invalid config: %v", err)
	}
	t.Setenv("TED_DATABASE_URL", "sensitive-value")
	if _, err = Load(); err == nil || strings.Contains(err.Error(), "sensitive-value") {
		t.Fatal("ambiguous credentials accepted or disclosed")
	}
}
func TestInvalidConfiguration(t *testing.T) {
	t.Setenv("TED_DATABASE_URL", "postgres://example/ted")
	t.Setenv("TED_DATABASE_URL_FILE", "")
	t.Setenv("TED_LISTEN", ":8080")
	t.Setenv("TED_INSECURE_HTTP", "perhaps")
	if _, err := Load(); err == nil {
		t.Fatal("invalid insecure mode accepted")
	}
	t.Setenv("TED_INSECURE_HTTP", "true")
	t.Setenv("TED_LISTEN", "missing-port")
	if _, err := Load(); err == nil {
		t.Fatal("invalid listen address accepted")
	}
}

func TestPasswordMinimumConfiguration(t *testing.T) {
	t.Setenv("TED_DATABASE_URL", "postgres://example/ted")
	t.Setenv("TED_DATABASE_URL_FILE", "")
	t.Setenv("TED_LISTEN", ":8080")
	t.Setenv("TED_INSECURE_HTTP", "")
	for _, value := range []string{"", "0", "1", "12", "1024"} {
		t.Setenv("TED_MIN_PASSWORD_LENGTH", value)
		c, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if value == "" && c.MinPasswordLength != 0 {
			t.Fatal("passwords must be optional by default")
		}
	}
	for _, value := range []string{"-1", "1025", "one"} {
		t.Setenv("TED_MIN_PASSWORD_LENGTH", value)
		if _, err := Load(); err == nil {
			t.Fatal("invalid minimum accepted")
		}
	}
}

func TestRDKeyOptionalAndInvalidValuesAreRedacted(t *testing.T) {
	t.Setenv("TED_DATABASE_URL", "postgres://example/ted")
	t.Setenv("TED_DATABASE_URL_FILE", "")
	t.Setenv("TED_LISTEN", ":8080")
	t.Setenv("TED_INSECURE_HTTP", "")
	t.Setenv("TED_MIN_PASSWORD_LENGTH", "")
	t.Setenv("TED_REAL_DEBRID_API_KEY", "")
	c, err := Load()
	if err != nil || c.RealDebridAPIKey != "" {
		t.Fatal("optional key required", err)
	}
	t.Setenv("TED_REAL_DEBRID_API_KEY", " private-key ")
	c, err = Load()
	if err != nil || c.RealDebridAPIKey != "private-key" {
		t.Fatal("key not loaded")
	}
	t.Setenv("TED_REAL_DEBRID_API_KEY", "private-key\ninjected")
	if _, err = Load(); err == nil || strings.Contains(err.Error(), "private-key") {
		t.Fatal("invalid key accepted or exposed")
	}
}

func TestMappingWorkerBounds(t *testing.T) {
	t.Setenv("TED_DATABASE_URL", "postgres://example/ted")
	t.Setenv("TED_DATABASE_URL_FILE", "")
	for _, value := range []string{"", "1", "2", "4"} {
		t.Setenv("TED_MAPPING_WORKERS", value)
		c, err := Load()
		if err != nil || c.MappingWorkers < 1 || c.MappingWorkers > 4 || (value == "" && c.MappingWorkers != 4) {
			t.Fatal(c.MappingWorkers, err)
		}
	}
	for _, value := range []string{"0", "5", "-1", "unbounded"} {
		t.Setenv("TED_MAPPING_WORKERS", value)
		if _, err := Load(); err == nil {
			t.Fatal("invalid parallelism accepted", value)
		}
	}
}

func TestMappingBatchSize(t *testing.T) {
	t.Setenv("TED_DATABASE_URL", "postgres://example/ted")
	t.Setenv("TED_DATABASE_URL_FILE", "")
	for value, want := range map[string]int{"": 200, "1": 1, "200": 200, "500": 500} {
		t.Setenv("TED_MAPPING_BATCH_SIZE", value)
		c, err := Load()
		if err != nil || c.MappingBatchSize != want {
			t.Fatal(c.MappingBatchSize, err)
		}
	}
	for _, value := range []string{"0", "501", "-1", "lots"} {
		t.Setenv("TED_MAPPING_BATCH_SIZE", value)
		if _, err := Load(); err == nil {
			t.Fatal("invalid batch size accepted", value)
		}
	}
}

func TestCalendarTimezone(t *testing.T) {
	t.Setenv("TED_DATABASE_URL", "postgres://unused")
	t.Setenv("TED_TIMEZONE", "America/Sao_Paulo")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := time.Date(2026, 10, 10, 1, 0, 0, 0, time.UTC).In(c.CalendarLocation).Format(time.DateOnly); got != "2026-10-09" {
		t.Fatal(got)
	}
	t.Setenv("TED_TIMEZONE", "invalid/zone")
	if _, err = Load(); err == nil {
		t.Fatal("invalid timezone accepted")
	}
}



func TestNativeTokenPolicyConfiguration(t *testing.T) {
	t.Setenv("TED_DATABASE_URL", "postgres://unused/ted")
	t.Setenv("TED_DATABASE_URL_FILE", "")
	t.Setenv("TED_ACCESS_TOKEN_TTL", "")
	t.Setenv("TED_REFRESH_TOKEN_IDLE_TTL", "")
	t.Setenv("TED_TOKEN_FAMILY_TTL", "")
	c, err := Load()
	if err != nil || c.NativeAuth.AccessTTL != 10*time.Minute || c.NativeAuth.RefreshIdleTTL != 720*time.Hour || c.NativeAuth.FamilyTTL != 2160*time.Hour {
		t.Fatal("approved defaults changed", err)
	}
	t.Setenv("TED_ACCESS_TOKEN_TTL", "2m")
	t.Setenv("TED_REFRESH_TOKEN_IDLE_TTL", "48h")
	t.Setenv("TED_TOKEN_FAMILY_TTL", "96h")
	c, err = Load()
	if err != nil || c.NativeAuth.AccessTTL != 2*time.Minute || c.NativeAuth.RefreshIdleTTL != 48*time.Hour || c.NativeAuth.FamilyTTL != 96*time.Hour {
		t.Fatal("configured lifetimes ignored", err)
	}
	for _, value := range []string{"0", "-1s", "1ms", "48h", "secret-invalid-duration"} {
		t.Setenv("TED_ACCESS_TOKEN_TTL", value)
		if _, err = Load(); err == nil || strings.Contains(err.Error(), "secret-invalid-duration") {
			t.Fatal("unsafe lifetime accepted or raw input disclosed")
		}
	}
	t.Setenv("TED_ACCESS_TOKEN_TTL", "2m")
	t.Setenv("TED_TOKEN_FAMILY_TTL", "24h")
	if _, err = Load(); err == nil {
		t.Fatal("idle expiry exceeds family deadline")
	}
}
