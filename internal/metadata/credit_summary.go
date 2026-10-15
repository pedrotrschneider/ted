package metadata

import (
	"context"
	"fmt"
	"strings"
)

func appendRole(roles []string, role string) []string {
	role = strings.TrimSpace(role)
	if roles == nil {
		roles = []string{}
	}
	if role == "" {
		return roles
	}
	for _, previous := range roles {
		if previous == role {
			return roles
		}
	}
	return append(roles, role)
}

// A credit summary fetch resolves identifiers and a translated name, without
// enriching the title, its seasons, artwork, cast or other credits.
type creditSummaryReader interface {
	ResolveCreditSummary(context.Context, string, string, string) (string, Item, error)
}

func (p *Providers) ResolveCreditSummary(ctx context.Context, provider, kind, id string) (string, Item, error) {
	if provider != "Tvdb" {
		imdb, err := p.ResolveCredit(ctx, provider, kind, id)
		return imdb, Item{}, err
	}
	if !positiveID.MatchString(id) || (kind != "movie" && kind != "series") {
		return "", Item{}, ErrInvalid
	}
	endpoint := "/movies/"
	if kind == "series" {
		endpoint = "/series/"
	}
	var v struct {
		Status string
		Data   *tvRecord
	}
	if err := p.get(ctx, provider, endpoint+id+"/extended?short=true&meta=translations", &v); err != nil {
		return "", Item{}, err
	}
	if v.Status != "success" || v.Data == nil || fmt.Sprint(v.Data.ID) != id || strings.TrimSpace(v.Data.Name) == "" {
		return "", Item{}, ErrPartial
	}
	item := Item{Name: v.Data.Name, PreferredMetadataLanguage: "en"}
	if v.Data.Translations != nil {
		var best *tvTranslation
		for _, tr := range v.Data.Translations.NameTranslations {
			if tr.Language == "eng" && strings.TrimSpace(tr.Name) != "" && !tr.IsAlias && (best == nil || tr.IsPrimary && !best.IsPrimary) {
				copy := tr
				best = &copy
			}
		}
		if best != nil {
			item.Name = best.Name
		}
	}
	imdb := remoteIDs(v.Data.RemoteIDs)["Imdb"]
	if !titleID.MatchString(imdb) {
		return "", item, ErrMissing
	}
	return imdb, item, nil
}

func (s *StoredProviders) ResolveCreditSummary(ctx context.Context, provider, kind, id string) (string, Item, error) {
	p, err := s.snapshot(ctx)
	if err != nil {
		return "", Item{}, err
	}
	return p.ResolveCreditSummary(ctx, provider, kind, id)
}
