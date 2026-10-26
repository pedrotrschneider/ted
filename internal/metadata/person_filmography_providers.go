package metadata

import (
	"context"
	"fmt"
)

// A second provider's appearances are admitted only through verified person
// IDs. A biography's heuristic name match never grants a filmography identity.
func (p *Providers) personFilmographyProviders(ctx context.Context, provider string, d Document) (Document, error) {
	other := "Tmdb"
	if provider == "Tmdb" {
		other = "Tvdb"
	}
	if !p.Enabled(other) {
		return d, nil
	}
	imdb := d.Item.ProviderIDs["Imdb"]
	id := d.Item.ProviderIDs[other]
	if id == "" && imdbPersonID.MatchString(imdb) {
		if other == "Tmdb" {
			var found struct {
				People *[]struct{ ID int64 } `json:"person_results"`
			}
			if err := p.get(ctx, other, "/find/"+imdb+"?external_source=imdb_id", &found); err != nil {
				return d, err
			}
			if found.People == nil {
				return d, ErrPartial
			}
			if len(*found.People) == 0 {
				return d, nil
			}
			if len(*found.People) != 1 {
				return d, ErrAmbiguous
			}
			id = fmt.Sprint((*found.People)[0].ID)
		} else {
			var found struct {
				Status string
				Data   *[]struct{ People *struct{ ID int64 } }
			}
			if err := p.get(ctx, other, "/search/remoteid/"+imdb, &found); err != nil {
				return d, err
			}
			if found.Status != "success" || found.Data == nil {
				return d, ErrPartial
			}
			ids := map[int64]bool{}
			for _, v := range *found.Data {
				if v.People != nil && v.People.ID > 0 {
					ids[v.People.ID] = true
				}
			}
			if len(ids) == 0 {
				return d, nil
			}
			if len(ids) != 1 {
				return d, ErrAmbiguous
			}
			for v := range ids {
				id = fmt.Sprint(v)
			}
		}
	}
	if id == "" {
		return d, ErrMissing
	}
	if !positiveID.MatchString(id) {
		return d, ErrAmbiguous
	}
	r := Request{Kind: "person", ProviderID: id}
	var supplemental Document
	var err error
	if other == "Tmdb" {
		supplemental, err = p.tmdb(ctx, r)
	} else {
		supplemental, err = p.tvdb(ctx, r)
	}
	if err != nil {
		return d, err
	}
	if supplemental.ProviderID != id || imdb != "" && supplemental.Item.ProviderIDs["Imdb"] != imdb {
		return d, ErrAmbiguous
	}
	if reciprocal := supplemental.Item.ProviderIDs[provider]; reciprocal != "" && reciprocal != d.ProviderID {
		return d, ErrAmbiguous
	}
	if d.Credits == nil || supplemental.Credits == nil {
		return d, ErrPartial
	}
	d.Credits = append(d.Credits, supplemental.Credits...)
	if len(d.Credits) > 10000 {
		return d, ErrPartial
	}
	return d, nil
}
