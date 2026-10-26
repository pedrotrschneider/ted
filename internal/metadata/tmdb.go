package metadata

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

type tmPerson struct {
	ID                               int64
	Name, Character, Job, Department string
	Profile                          string `json:"profile_path"`
	Order                            int
}
type tmRecord struct {
	ID                             int64
	Title, Name, Overview, Tagline string
	OriginalTitle                  string `json:"original_title"`
	OriginalLanguage               string `json:"original_language"`
	ReleaseDate                    string `json:"release_date"`
	Runtime                        int
	Rating                         *float64 `json:"vote_average"`
	IMDB                           string   `json:"imdb_id"`
	Homepage                       string
	Poster                         string `json:"poster_path"`
	Backdrop                       string `json:"backdrop_path"`
	Genres                         []struct {
		ID   int
		Name string
	}
	Studios []struct {
		ID   int
		Name string
	} `json:"production_companies"`
	Countries []struct{ Name string } `json:"production_countries"`
	Credits   *struct {
		Cast *[]tmPerson
		Crew *[]tmPerson
	}
	Images *struct {
		Posters []struct {
			Path string `json:"file_path"`
		}
		Backdrops []struct {
			Path string `json:"file_path"`
		}
		Logos []struct {
			Path string `json:"file_path"`
		}
	}
	ReleaseDates *struct {
		Results []struct {
			Country string                           `json:"iso_3166_1"`
			Dates   []struct{ Certification string } `json:"release_dates"`
		}
	} `json:"release_dates"`
	Videos *struct {
		Results []struct {
			Site, Key, Type string
			Official        bool
		}
	}
	Collection *struct {
		ID   int
		Name string
	} `json:"belongs_to_collection"`
	Biography, Birthday, Deathday string
	BirthPlace                    string `json:"place_of_birth"`
	Profile                       string `json:"profile_path"`
	ExternalIDs                   *struct {
		IMDB string `json:"imdb_id"`
	} `json:"external_ids"`
	Combined *struct {
		Cast *[]tmCredit
		Crew *[]tmCredit
	} `json:"combined_credits"`
}
type tmCredit struct {
	ID                                    int64
	Title, Name, Overview, Character, Job string
	MediaType                             string `json:"media_type"`
	ReleaseDate                           string `json:"release_date"`
	FirstAirDate                          string `json:"first_air_date"`
	Poster                                string `json:"poster_path"`
}

func (p *Providers) tmdb(ctx context.Context, r Request) (Document, error) {
	if r.Kind == "series" || r.Kind == "season" || r.Kind == "episode" {
		return p.tmdbTV(ctx, r)
	}
	var d Document
	id := r.ProviderID
	if r.Kind == "movie" && id == "" {
		var found struct {
			Movies *[]struct{ ID int64 } `json:"movie_results"`
		}
		if !titleID.MatchString(r.TitleID) {
			return d, ErrInvalid
		}
		if e := p.get(ctx, "Tmdb", "/find/"+r.TitleID+"?external_source=imdb_id", &found); e != nil {
			return d, e
		}
		if found.Movies == nil {
			return d, ErrPartial
		}
		if len(*found.Movies) == 0 {
			return d, ErrMissing
		}
		if len(*found.Movies) != 1 {
			return d, ErrAmbiguous
		}
		id = strconv.FormatInt((*found.Movies)[0].ID, 10)
	}
	if !positiveID.MatchString(id) || (r.Kind != "movie" && r.Kind != "person") {
		return d, ErrInvalid
	}
	path := "/movie/" + id + "?append_to_response=credits,images,release_dates,videos"
	if r.Kind == "person" {
		path = "/person/" + id + "?append_to_response=combined_credits,external_ids"
	}
	var v tmRecord
	if e := p.get(ctx, "Tmdb", path, &v); e != nil {
		return d, e
	}
	if strconv.FormatInt(v.ID, 10) != id {
		return d, ErrPartial
	}
	d.ProviderID = id
	if r.Kind == "movie" {
		d.OriginalLanguage, d.OriginalLanguageChecked = originalLanguage(v.OriginalLanguage), true
	}
	d.Item = Item{ProviderIDs: map[string]string{"Tmdb": id}, PreferredMetadataLanguage: p.config.Language, PreferredMetadataCountryCode: p.config.Country}
	if r.Kind == "person" {
		if strings.TrimSpace(v.Name) == "" {
			return Document{}, ErrPartial
		}
		d.Item.Type = "Person"
		d.Item.Name = v.Name
		d.Item.Overview = v.Biography
		d.Item.PremiereDate = date(v.Birthday)
		d.Item.EndDate = date(v.Deathday)
		if v.BirthPlace != "" {
			d.Item.ProductionLocations = []string{v.BirthPlace}
		}
		if v.ExternalIDs != nil && strings.HasPrefix(v.ExternalIDs.IMDB, "nm") {
			d.Item.ProviderIDs["Imdb"] = v.ExternalIDs.IMDB
		}
		if img := tmImage(v.Profile); img != "" {
			d.Images = []Image{{Type: "Primary", URL: img}}
		}
		if v.Combined == nil || v.Combined.Cast == nil || v.Combined.Crew == nil {
			return d, ErrPartial
		}
		d.Credits = []Credit{}
		seen := map[string]int{}
		for _, c := range append(*v.Combined.Cast, *v.Combined.Crew...) {
			if c.MediaType != "movie" && c.MediaType != "tv" {
				d.Credits = nil
				return d, ErrPartial
			}
			if c.ID <= 0 {
				d.Credits = nil
				return d, ErrPartial
			}
			kind := "movie"
			name := c.Title
			released := c.ReleaseDate
			itemType := "Movie"
			if c.MediaType == "tv" {
				kind = "series"
				name = c.Name
				released = c.FirstAirDate
				itemType = "Series"
			}
			if strings.TrimSpace(name) == "" {
				d.Credits = nil
				return d, ErrPartial
			}
			sourceID := strconv.FormatInt(c.ID, 10)
			key := kind + ":" + sourceID
			role := tmdbCharacter(c.Character)
			if role == "" {
				role = c.Job
			}
			if i, ok := seen[key]; ok {
				d.Credits[i].Roles = appendRole(d.Credits[i].Roles, role)
				continue
			}
			seen[key] = len(d.Credits)
			item := Item{Type: itemType, Name: name, Overview: c.Overview, PremiereDate: date(released), ProductionYear: year(released), ProviderIDs: map[string]string{"Tmdb": sourceID}}
			d.Credits = append(d.Credits, Credit{Provider: "Tmdb", Kind: kind, ID: sourceID, Item: item, Poster: tmImage(c.Poster), Roles: appendRole(nil, role)})
		}
		if len(d.Credits) > 10000 {
			d.Credits = nil
			return d, ErrPartial
		}
		return d, nil
	}
	if strings.TrimSpace(v.Title) == "" || v.IMDB != r.TitleID {
		return Document{}, ErrPartial
	}
	d.Item.Type = "Movie"
	d.Item.Name = v.Title
	d.Item.OriginalTitle = v.OriginalTitle
	d.Item.Overview = v.Overview
	d.Item.PremiereDate = date(v.ReleaseDate)
	d.Item.ProductionYear = year(v.ReleaseDate)
	d.Item.RunTimeTicks = ticks(v.Runtime)
	d.Item.CommunityRating = v.Rating
	d.Item.ProviderIDs["Imdb"] = v.IMDB
	if v.Tagline != "" {
		d.Item.Taglines = []string{v.Tagline}
	}
	for _, g := range v.Genres {
		d.Item.Genres = append(d.Item.Genres, g.Name)
	}
	for _, s := range v.Studios {
		d.Item.Studios = append(d.Item.Studios, NameID{s.Name, guid(fmt.Sprintf("Tmdb:studio:%d", s.ID))})
	}
	for _, c := range v.Countries {
		d.Item.ProductionLocations = append(d.Item.ProductionLocations, c.Name)
	}
	if u := publicLink(v.Homepage); u != "" {
		d.Item.ExternalURLs = append(d.Item.ExternalURLs, URL{Name: "Homepage", URL: u})
	}
	if v.Collection != nil {
		d.Item.Tags = append(d.Item.Tags, v.Collection.Name)
	}
	if v.ReleaseDates != nil {
		for _, result := range v.ReleaseDates.Results {
			if result.Country == p.config.Country {
				for _, release := range result.Dates {
					if release.Certification != "" {
						d.Item.OfficialRating = release.Certification
						break
					}
				}
			}
		}
	}
	if v.Videos != nil {
		for _, video := range v.Videos.Results {
			if video.Site == "YouTube" && video.Type == "Trailer" && len(video.Key) < 128 && strings.Trim(video.Key, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-") == "" {
				d.Item.RemoteTrailers = append(d.Item.RemoteTrailers, URL{URL: "https://www.youtube.com/watch?v=" + video.Key})
			}
		}
	}
	if v.Credits == nil || v.Credits.Cast == nil || v.Credits.Crew == nil {
		return d, ErrPartial
	}
	d.People = []PersonSummary{}
	for _, person := range *v.Credits.Cast {
		if person.ID <= 0 || person.Name == "" {
			d.People = nil
			return d, ErrPartial
		}
		d.People = append(d.People, PersonSummary{"Tmdb", strconv.FormatInt(person.ID, 10), person.Name, tmdbCharacter(person.Character), "Actor", tmImage(person.Profile)})
	}
	for _, person := range *v.Credits.Crew {
		if person.ID <= 0 || person.Name == "" {
			d.People = nil
			return d, ErrPartial
		}
		kind := "Unknown"
		switch person.Department {
		case "Directing":
			kind = "Director"
		case "Writing":
			kind = "Writer"
		case "Production":
			kind = "Producer"
		case "Sound":
			if person.Job == "Original Music Composer" {
				kind = "Composer"
			}
		}
		if kind != "Unknown" {
			d.People = append(d.People, PersonSummary{"Tmdb", strconv.FormatInt(person.ID, 10), person.Name, person.Job, kind, tmImage(person.Profile)})
		}
	}
	if len(d.People) > 10000 {
		return Document{}, ErrPartial
	}
	if img := tmImage(v.Poster); img != "" {
		d.Images = append(d.Images, Image{Type: "Primary", URL: img})
	}
	if img := tmImage(v.Backdrop); img != "" {
		d.Images = append(d.Images, Image{Type: "Backdrop", URL: img})
	}
	if v.Images != nil {
		if tmImage(v.Backdrop) == "" {
			for _, backdrop := range v.Images.Backdrops {
				if img := tmImage(backdrop.Path); img != "" {
					d.Images = append(d.Images, Image{Type: "Backdrop", URL: img})
					break
				}
			}
		}
		for _, logo := range v.Images.Logos {
			if img := tmImage(logo.Path); img != "" {
				d.Images = append(d.Images, Image{Type: "Logo", URL: img})
				break
			}
		}
	}
	return d, nil
}

// TMDb appends this performance annotation to character names. Keep the
// character itself in customer-facing cast/filmography labels.
func tmdbCharacter(role string) string {
	role = strings.TrimSpace(role)
	const suffix = "(voice)"
	if len(role) >= len(suffix) && strings.EqualFold(role[len(role)-len(suffix):], suffix) {
		role = strings.TrimSpace(role[:len(role)-len(suffix)])
	}
	return role
}

// Apply the same presentation rule to retained data without refetching every
// cached title. The stored provider payload/identity is left intact.
func (v *Snapshot) normalizeTMDBCast() {
	if v.Provider != "Tmdb" && v.FieldSources["People"] != "Tmdb" {
		return
	}
	for i := range v.Item.People {
		if v.Item.People[i].Type == "Actor" {
			v.Item.People[i].Role = tmdbCharacter(v.Item.People[i].Role)
		}
	}
}
