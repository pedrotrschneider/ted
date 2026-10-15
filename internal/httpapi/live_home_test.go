package httpapi

import (
	"context"
	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/cinemeta"
	"entertainmentdistrict/internal/database"
	"entertainmentdistrict/internal/metadata"
	"log/slog"
	"os"
	"testing"
	"time"
)

// Explicit, read-only timing of the owner's deployed Home workloads.
func TestLiveHomeTimings(t *testing.T) {
	if os.Getenv("TED_LIVE_HOME_TIMINGS") != "1" {
		t.Skip("live diagnosis not selected")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, os.Getenv("TED_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var user int64
	if err = pool.QueryRow(ctx, "SELECT user_id FROM native_playback_sessions ORDER BY created_at DESC LIMIT 1").Scan(&user); err != nil {
		t.Fatal(err)
	}
	a := New(pool, accounts.New(pool, nil), false, slog.Default(), nil)
	a.Metadata, err = metadata.New(pool, nil, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Metadata.Close()
	for repeat := 0; repeat < 2; repeat++ {
		for _, row := range []string{"continue-watching", "new-releases", "next-up"} {
			start := time.Now()
			page, e := a.personalNative(ctx, user, row, "")
			t.Logf("row=%s count=%d elapsed=%s success=%t", row, len(page.Titles), time.Since(start), e == nil)
			if e != nil {
				t.Fatal(e)
			}
		}
		for _, kind := range []string{"movie", "series"} {
			start := time.Now()
			page, e := a.browseNative(ctx, user, kind, "", 0, cinemeta.Filters{})
			t.Logf("popular=%s count=%d elapsed=%s success=%t", kind, len(page.Titles), time.Since(start), e == nil)
			if e != nil {
				t.Log("discovery unavailable")
			}
		}
	}
}
