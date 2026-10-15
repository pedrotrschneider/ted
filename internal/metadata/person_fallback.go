package metadata

import (
	"context"
	"regexp"
	"strconv"
	"strings"
)

var imdbPersonID = regexp.MustCompile(`^nm[0-9]{7,12}$`)

func personRemoteIDs(remote []tvRemote) (map[string]string, error) {
	ids := map[string]string{}
	for _, ref := range remote {
		for provider, id := range remoteIDs([]tvRemote{ref}) {
			if provider == "Imdb" && !imdbPersonID.MatchString(id) || provider == "Tmdb" && !positiveID.MatchString(id) {
				return nil, ErrAmbiguous
			}
			if previous := ids[provider]; previous != "" && previous != id {
				return nil, ErrAmbiguous
			}
			ids[provider] = id
		}
	}
	return ids, nil
}

// Fetch only a missing biography for a TVDB person. Keep that
// person's stable identity, name, portraits, credited roles and filmography.
func (p *Providers) personBiographyFallback(ctx context.Context, doc Document) (Document, error) {
	if strings.TrimSpace(doc.Item.Overview) != "" || !p.Enabled("Tmdb") {
		return doc, nil
	}
	doc.TMDBBiographyChecked = true
	id, imdb := doc.Item.ProviderIDs["Tmdb"], doc.Item.ProviderIDs["Imdb"]
	matchedName := ""
	if id == "" && imdb == "" {
		doc.TMDBBiographyNameChecked = true
		var err error
		id, matchedName, err = p.personByName(ctx, doc)
		if err != nil || id == "" {
			return doc, err
		}
	}
	if id == "" {
		var found struct {
			People *[]struct{ ID int64 } `json:"person_results"`
		}
		if err := p.get(ctx, "Tmdb", "/find/"+imdb+"?external_source=imdb_id", &found); err != nil {
			return doc, err
		}
		if found.People == nil {
			return doc, ErrPartial
		}
		if len(*found.People) == 0 {
			return doc, nil
		}
		if len(*found.People) != 1 {
			return doc, ErrAmbiguous
		}
		id = strconv.FormatInt((*found.People)[0].ID, 10)
	}
	if !positiveID.MatchString(id) {
		return doc, ErrAmbiguous
	}
	var person tmRecord
	if err := p.get(ctx, "Tmdb", "/person/"+id+"?append_to_response=external_ids", &person); err != nil {
		return doc, err
	}
	if strconv.FormatInt(person.ID, 10) != id || strings.TrimSpace(person.Name) == "" {
		return doc, ErrPartial
	}
	if matchedName != "" && normalized(person.Name) != normalized(matchedName) {
		return doc, ErrAmbiguous
	}
	if imdb != "" {
		if person.ExternalIDs == nil {
			return doc, ErrPartial
		}
		if person.ExternalIDs.IMDB != imdb {
			return doc, ErrAmbiguous
		}
	}
	doc.FieldSources = map[string]string{}
	if matchedName == "" {
		doc.Item.ProviderIDs["Tmdb"] = id
		doc.FieldSources["ProviderIds"] = "Tvdb/Tmdb"
	}
	if biography := strings.TrimSpace(person.Biography); biography != "" {
		doc.Item.Overview = biography
		doc.FieldSources["Overview"] = "Tmdb"
		if matchedName != "" {
			doc.BiographyMatch = &BiographyMatch{Provider: "Tmdb", ProviderID: id, Method: "name", Name: person.Name}
		}
	}
	return doc, nil
}
