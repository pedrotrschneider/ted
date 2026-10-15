package addons

import (
	"context"
	"encoding/json"
	"entertainmentdistrict/internal/accounts"
	"testing"
)

func TestNamedAddonVariantsHaveIndependentSharedPolicies(t *testing.T) {
	s, a, g, remote, p := fixture(t)
	ctx := context.Background()
	var plain Descriptor
	if err := json.Unmarshal(descriptorRaw(p.url), &plain); err != nil {
		t.Fatal(err)
	}
	plain.Manifest.Name = "Torrentio"
	rd := plain
	rd.Manifest.Name = "Torrentio RD"
	rd.Transport = p.url + "/rd-private/manifest.json"
	secondRD := rd
	secondRD.Transport = p.url + "/another-private/manifest.json"
	raw := func(d Descriptor) json.RawMessage {
		v, err := json.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	remote.mu.Lock()
	remote.collection = []json.RawMessage{raw(plain), raw(rd), raw(secondRD)}
	remote.mu.Unlock()
	if _, err := s.Collection(ctx, g.User.ID, true); err != nil {
		t.Fatal(err)
	}
	policies, err := s.Policies(ctx)
	if err != nil || len(policies) != 2 || policies[0].Name != "Torrentio" || policies[1].Name != "Torrentio RD" || policies[0].ID == policies[1].ID {
		t.Fatal("named variants merged or private configurations split", policies, err)
	}
	if rd.PolicyKey() != secondRD.PolicyKey() {
		t.Fatal("private configuration changed shared policy")
	}
	actor := accounts.Identity{ID: g.User.ID, Version: g.User.Version}
	if _, err = s.UpdatePolicy(ctx, actor, plain.PolicyKey(), false, 0); err != nil {
		t.Fatal(err)
	}
	page, err := s.SelectedSources(ctx, a, g, fixtureMedia, "")
	if err != nil || len(page.Sources) == 0 {
		t.Fatal("disabling plain variant disabled RD", err)
	}
	for _, addon := range page.Addons {
		if addon.Enabled != (addon.Name == "Torrentio RD") {
			t.Fatal("variant enablement incorrect", addon)
		}
	}
	for _, source := range page.Sources {
		if source.Addon != "Torrentio RD" {
			t.Fatal("disabled variant supplied a source")
		}
	}
	if _, err = s.UpdatePolicy(ctx, actor, rd.PolicyKey(), false, 0); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	before := p.requests
	p.mu.Unlock()
	page, err = s.SelectedSources(ctx, a, g, fixtureMedia, "")
	p.mu.Lock()
	after := p.requests
	p.mu.Unlock()
	if err != nil || len(page.Sources) != 0 || before != after {
		t.Fatal("disabled variants were queried", err)
	}
}
