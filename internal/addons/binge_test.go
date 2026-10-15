package addons

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestSourceGroupingIsStableScopedPrivateAndPresentOnSession(t *testing.T) {
	s, a, g, _, provider := fixture(t)
	provider.mu.Lock()
	provider.unsupported = true
	provider.mu.Unlock()
	first, err := s.SelectedSources(context.Background(), a, g, mediaIdentity("episode", "tt1234567:1:1"), "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.SelectedSources(context.Background(), a, g, mediaIdentity("episode", "tt1234567:1:2"), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Addons) != 1 || len(first.Sources) != 1 || len(second.Sources) != 1 {
		t.Fatalf("%+v %+v", first, second)
	}
	choice := first.Sources[0]
	if len(choice.AddonID) != 64 || len(choice.BingeKey) != 64 || second.Sources[0].BingeKey != choice.BingeKey || first.Issues[0].AddonID != choice.AddonID {
		t.Fatalf("%+v", first)
	}
	raw, _ := json.Marshal(first)
	if strings.Contains(string(raw), "fixture-720p") || strings.Contains(string(raw), "configured-secret") || strings.Contains(string(raw), "provider-only") {
		t.Fatal("private provider data escaped")
	}
	session, err := s.CreateSession(context.Background(), a, g, first.Media, choice.ID)
	if err != nil || session.AddonID != choice.AddonID || session.BingeKey != choice.BingeKey {
		t.Fatalf("%+v %v", session, err)
	}
	if bingeKey("other", "fixture-720p") == choice.BingeKey || bingeKey(choice.AddonID, "") != "" {
		t.Fatal("group not scoped")
	}
}
