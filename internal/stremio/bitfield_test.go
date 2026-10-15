package stremio

import (
	"errors"
	"reflect"
	"testing"
)

func TestWatchedWireCompatibilityAndRoundTrip(t *testing.T) {
	ids := []string{"tt2934286:1:1", "tt2934286:1:2", "tt2934286:1:3", "tt2934286:1:4", "tt2934286:1:5", "tt2934286:1:6"}
	// Public upstream protocol example: low five bits set, remaining bits clear.
	flags, err := DecodeWatched("tt2934286:1:5:5:eJyTZwAAAEAAIA==", ids)
	if err != nil {
		t.Fatal(err)
	}
	for i, id := range ids {
		if flags[id] != (i < 5) {
			t.Fatal(id, flags)
		}
	}
	flags[ids[2]] = false
	encoded, err := EncodeWatched(ids, flags)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeWatched(encoded, ids)
	if err != nil || !reflect.DeepEqual(decoded, flags) {
		t.Fatal(decoded, err)
	}
	empty, err := DecodeWatched("undefined:1:eJwDAAAAAAE=", nil)
	if err != nil || len(empty) != 0 {
		t.Fatal(empty, err)
	}
}
func TestWatchedAnchorShiftAndUnknownIdentity(t *testing.T) {
	old := []string{"tt1234567:1:1", "tt1234567:1:2"}
	encoded, err := EncodeWatched(old, map[string]bool{old[1]: true})
	if err != nil {
		t.Fatal(err)
	}
	ids := append([]string{"tt1234567:0:1"}, old...)
	decoded, err := DecodeWatched(encoded, ids)
	if err != nil || !decoded[old[1]] || decoded[ids[0]] {
		t.Fatal(decoded, err)
	}
	if _, err = DecodeWatched(encoded, old[:1]); !errors.Is(err, ErrAmbiguous) {
		t.Fatal("unknown anchor became empty", err)
	}
	if _, err = EncodeWatched(old, map[string]bool{"unknown": true}); !errors.Is(err, ErrAmbiguous) {
		t.Fatal(err)
	}
}
func TestMalformedWatchedDataFailsClosed(t *testing.T) {
	for _, s := range []string{"bad", "tt1234567:1:1:0:eJwDAAAAAAE=", "tt1234567:1:1:1:not-base64", "tt1234567:1:1:10001:eJwDAAAAAAE="} {
		if _, err := DecodeWatched(s, []string{"tt1234567:1:1"}); err == nil {
			t.Fatal(s)
		}
	}
	if _, err := DecodeWatched("", []string{"duplicate", "duplicate"}); !errors.Is(err, ErrAmbiguous) {
		t.Fatal(err)
	}
}
