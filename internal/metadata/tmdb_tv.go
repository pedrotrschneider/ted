package metadata

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"entertainmentdistrict/internal/cinemeta"
)

type tmTVEpisode struct {
	ID             int64
	Name, Overview string
	Season         int    `json:"season_number"`
	Number         int    `json:"episode_number"`
	Show           int64  `json:"show_id"`
	AirDate        string `json:"air_date"`
	Still          string `json:"still_path"`
	Runtime        int
	Rating         *float64 `json:"vote_average"`
	External       *struct {
		TVDB int64  `json:"tvdb_id"`
		IMDB string `json:"imdb_id"`
	} `json:"external_ids"`
	Credits *struct {
		Cast   *[]tmPerson
		Guests *[]tmPerson `json:"guest_stars"`
	} `json:"credits"`
}
type tmTVSeason struct {
	ID             int64
	Name, Overview string
	Number         int    `json:"season_number"`
	AirDate        string `json:"air_date"`
	Poster         string `json:"poster_path"`
	Episodes       *[]tmTVEpisode
}
type tmTVSeries struct {
	ID                     int64
	Name, Overview, Status string
	OriginalName           string   `json:"original_name"`
	OriginalLanguage       string   `json:"original_language"`
	FirstAirDate           string   `json:"first_air_date"`
	LastAirDate            string   `json:"last_air_date"`
	Poster                 string   `json:"poster_path"`
	Backdrop               string   `json:"backdrop_path"`
	Rating                 *float64 `json:"vote_average"`
	Genres                 []struct{ Name string }
	Seasons                *[]tmTVSeason
	External               *struct {
		IMDB string `json:"imdb_id"`
	} `json:"external_ids"`
	Credits *struct {
		Cast *[]tmPerson
		Crew *[]tmPerson
	}
	Images *struct {
		Logos []struct {
			Path string `json:"file_path"`
		}
	}
}

func (p *Providers) tmdbTVSeries(ctx context.Context, imdb, id string) (tmTVSeries, error) {
	var root tmTVSeries
	if !titleID.MatchString(imdb) {
		return root, ErrInvalid
	}
	if id == "" {
		var found struct {
			Series *[]struct{ ID int64 } `json:"tv_results"`
		}
		if err := p.get(ctx, "Tmdb", "/find/"+imdb+"?external_source=imdb_id", &found); err != nil {
			return root, err
		}
		if found.Series == nil {
			return root, ErrPartial
		}
		if len(*found.Series) == 0 {
			return root, ErrMissing
		}
		if len(*found.Series) != 1 {
			return root, ErrAmbiguous
		}
		id = fmt.Sprint((*found.Series)[0].ID)
	}
	if !positiveID.MatchString(id) {
		return root, ErrInvalid
	}
	if err := p.get(ctx, "Tmdb", "/tv/"+id+"?append_to_response=external_ids,credits,images", &root); err != nil {
		return root, err
	}
	if fmt.Sprint(root.ID) != id || root.External == nil || root.Seasons == nil || strings.TrimSpace(root.Name) == "" {
		return root, ErrPartial
	}
	if root.External.IMDB != imdb {
		return root, ErrAmbiguous
	}
	return root, nil
}

func castPeople(cast, guests []tmPerson) ([]PersonSummary, error) {
	people := []PersonSummary{}
	seen := map[string]bool{}
	for _, group := range [][]tmPerson{cast, guests} {
		for _, p := range group {
			if p.ID <= 0 || strings.TrimSpace(p.Name) == "" {
				return nil, ErrPartial
			}
			role := tmdbCharacter(p.Character)
			key := fmt.Sprint(p.ID) + ":" + role
			if !seen[key] {
				seen[key] = true
				people = append(people, PersonSummary{"Tmdb", fmt.Sprint(p.ID), p.Name, role, "Actor", tmImage(p.Profile)})
			}
		}
	}
	if len(people) > 10000 {
		return nil, ErrPartial
	}
	return people, nil
}

// The returned tuple belongs to TMDb. Never require it to equal TVDB's tuple.
func (p *Providers) tmdbEpisodeLink(ctx context.Context, tvdb int64) (tmTVEpisode, error) {
	var found struct {
		Episodes *[]tmTVEpisode `json:"tv_episode_results"`
	}
	if tvdb <= 0 {
		return tmTVEpisode{}, ErrAmbiguous
	}
	if err := p.get(ctx, "Tmdb", fmt.Sprintf("/find/%d?external_source=tvdb_id", tvdb), &found); err != nil {
		return tmTVEpisode{}, err
	}
	if found.Episodes == nil {
		return tmTVEpisode{}, ErrPartial
	}
	if len(*found.Episodes) == 0 {
		return tmTVEpisode{}, ErrMissing
	}
	if len(*found.Episodes) != 1 {
		return tmTVEpisode{}, ErrAmbiguous
	}
	v := (*found.Episodes)[0]
	if v.ID <= 0 || v.Show <= 0 || v.Season < 0 || v.Number < 0 {
		return tmTVEpisode{}, ErrPartial
	}
	return v, nil
}
func (p *Providers) tmdbEpisodeRead(ctx context.Context, link tmTVEpisode, tvdb int64) (tmTVEpisode, []PersonSummary, error) {
	var v tmTVEpisode
	path := fmt.Sprintf("/tv/%d/season/%d/episode/%d?append_to_response=external_ids,credits", link.Show, link.Season, link.Number)
	if err := p.get(ctx, "Tmdb", path, &v); err != nil {
		return v, nil, err
	}
	if v.ID != link.ID || v.Season != link.Season || v.Number != link.Number || v.External == nil {
		return v, nil, ErrAmbiguous
	}
	if tvdb > 0 && v.External.TVDB != tvdb {
		return v, nil, ErrAmbiguous
	}
	if v.Credits == nil || v.Credits.Cast == nil || v.Credits.Guests == nil {
		return v, nil, ErrPartial
	}
	people, err := castPeople(*v.Credits.Cast, *v.Credits.Guests)
	v.Show = link.Show
	return v, people, err
}

func (p *Providers) tmdbSeason(ctx context.Context, parent int64, number int) (tmTVSeason, error) {
	var v tmTVSeason
	if err := p.get(ctx, "Tmdb", fmt.Sprintf("/tv/%d/season/%d", parent, number), &v); err != nil {
		return v, err
	}
	if v.ID <= 0 || v.Number != number || v.Episodes == nil {
		return v, ErrPartial
	}
	return v, nil
}

func (p *Providers) tmdbCanonicalLink(ctx context.Context, root tmTVSeries, ep cinemeta.Episode, cache map[int]tmTVSeason) (tmTVEpisode, error) {
	if ep.TVDBID > 0 {
		v, err := p.tmdbEpisodeLink(ctx, ep.TVDBID)
		if err != nil {
			return v, err
		}
		if v.Show != root.ID || ep.TMDBID > 0 && v.ID != ep.TMDBID {
			return v, ErrAmbiguous
		}
		return v, nil
	}
	if ep.TMDBID <= 0 {
		return tmTVEpisode{}, ErrAmbiguous
	}
	// Locate a supplied exact TMDb episode ID, rather than guessing its tuple.
	numbers := []int{ep.Season}
	for _, s := range *root.Seasons {
		if s.Number != ep.Season {
			numbers = append(numbers, s.Number)
		}
	}
	for _, number := range numbers {
		season, ok := cache[number]
		if !ok {
			var err error
			season, err = p.tmdbSeason(ctx, root.ID, number)
			if err != nil {
				if err == ErrMissing {
					continue
				}
				return tmTVEpisode{}, err
			}
			cache[number] = season
		}
		for _, v := range *season.Episodes {
			if v.ID == ep.TMDBID {
				v.Show = root.ID
				return v, nil
			}
		}
	}
	return tmTVEpisode{}, ErrMissing
}

func (p *Providers) tmdbTV(ctx context.Context, r Request) (Document, error) {
	rootID := r.ParentProviderID
	if r.Kind == "series" {
		rootID = r.ProviderID
	}
	root, err := p.tmdbTVSeries(ctx, r.TitleID, rootID)
	if err != nil {
		return Document{}, err
	}
	d := Document{ProviderID: fmt.Sprint(root.ID), Item: Item{Type: "Series", Name: root.Name, OriginalTitle: root.OriginalName, Overview: root.Overview, Status: root.Status, PremiereDate: date(root.FirstAirDate), EndDate: date(root.LastAirDate), ProductionYear: year(root.FirstAirDate), CommunityRating: root.Rating, ProviderIDs: map[string]string{"Tmdb": fmt.Sprint(root.ID), "Imdb": r.TitleID}, PreferredMetadataLanguage: p.config.Language, PreferredMetadataCountryCode: p.config.Country}}
	if r.Kind == "series" {
		d.OriginalLanguage, d.OriginalLanguageChecked = originalLanguage(root.OriginalLanguage), true
		if root.Credits == nil || root.Credits.Cast == nil || root.Credits.Crew == nil {
			return d, ErrPartial
		}
		d.People, err = castPeople(*root.Credits.Cast, nil)
		if err != nil {
			return d, err
		}
		for _, g := range root.Genres {
			d.Item.Genres = append(d.Item.Genres, g.Name)
		}
		for _, s := range *root.Seasons {
			if s.ID <= 0 {
				return d, ErrPartial
			}
			d.Children = append(d.Children, Child{Request: Request{Kind: "season", TitleID: r.TitleID, ProviderID: fmt.Sprint(s.ID), ParentProviderID: fmt.Sprint(root.ID), Season: s.Number}, Image: tmImage(s.Poster)})
		}
		for _, v := range []Image{{Type: "Primary", URL: tmImage(root.Poster)}, {Type: "Backdrop", URL: tmImage(root.Backdrop)}} {
			if v.URL != "" {
				d.Images = append(d.Images, v)
			}
		}
		if root.Images != nil {
			for _, v := range root.Images.Logos {
				if img := tmImage(v.Path); img != "" {
					d.Images = append(d.Images, Image{Type: "Logo", URL: img})
					break
				}
			}
		}
		return d, nil
	}
	if len(r.CanonicalEpisodes) == 0 {
		return Document{}, ErrAmbiguous
	}
	cache := map[int]tmTVSeason{}
	links := []tmTVEpisode{}
	for _, ep := range r.CanonicalEpisodes {
		link, e := p.tmdbCanonicalLink(ctx, root, ep, cache)
		if e != nil {
			return Document{}, e
		}
		links = append(links, link)
	}
	if r.Kind == "episode" {
		if len(links) != 1 {
			return Document{}, ErrAmbiguous
		}
		v, people, e := p.tmdbEpisodeRead(ctx, links[0], r.CanonicalEpisodes[0].TVDBID)
		if e != nil {
			return Document{}, e
		}
		d = Document{ProviderID: fmt.Sprint(v.ID), ScopedCastChecked: true, People: people, Item: Item{Type: "Episode", Name: v.Name, Overview: v.Overview, PremiereDate: date(v.AirDate), ProductionYear: year(v.AirDate), RunTimeTicks: ticks(v.Runtime), CommunityRating: v.Rating, ProviderIDs: map[string]string{"Tmdb": fmt.Sprint(v.ID)}, PreferredMetadataLanguage: p.config.Language}}
		if img := tmImage(v.Still); img != "" {
			d.Images = []Image{{Type: "Primary", URL: img}}
		}
		return d, nil
	}
	if r.Kind != "season" {
		return Document{}, ErrInvalid
	}
	number := links[0].Season
	for _, link := range links {
		if link.Season != number {
			return Document{}, ErrAmbiguous
		}
	}
	season, ok := cache[number]
	if !ok {
		season, err = p.tmdbSeason(ctx, root.ID, number)
		if err != nil {
			return Document{}, err
		}
	}
	// A season's external ID is insufficient: the complete episode set must agree.
	wanted := map[int64]bool{}
	for _, link := range links {
		wanted[link.ID] = true
	}
	if len(wanted) != len(*season.Episodes) {
		return Document{}, ErrAmbiguous
	}
	for _, ep := range *season.Episodes {
		if !wanted[ep.ID] {
			return Document{}, ErrAmbiguous
		}
	}
	var people []PersonSummary
	if !r.SkipCast {
		people, err = p.tmdbUnionCast(ctx, links, r.CanonicalEpisodes)
		if err != nil {
			return Document{}, err
		}
	}
	d = Document{ProviderID: fmt.Sprint(season.ID), ScopedCastChecked: !r.SkipCast, People: people, Item: Item{Type: "Season", Name: season.Name, Overview: season.Overview, PremiereDate: date(season.AirDate), ProductionYear: year(season.AirDate), ProviderIDs: map[string]string{"Tmdb": fmt.Sprint(season.ID)}, PreferredMetadataLanguage: p.config.Language}}
	if d.Item.Name == "" {
		d.Item.Name = fmt.Sprintf("Season %d", r.Season)
	}
	if img := tmImage(season.Poster); img != "" {
		d.Images = []Image{{Type: "Primary", URL: img}}
	}
	for i, ep := range r.CanonicalEpisodes {
		for _, v := range *season.Episodes {
			if v.ID == links[i].ID {
				d.Children = append(d.Children, Child{Request: Request{Kind: "episode", TitleID: r.TitleID, VideoID: ep.ID, Season: ep.Season, Number: ep.Number, ProviderID: fmt.Sprint(v.ID), ParentProviderID: fmt.Sprint(root.ID)}, Image: tmImage(v.Still)})
			}
		}
	}
	return d, nil
}

// Bounded workers collect only credits for an explicitly opened season. River
// owns cancellation/retry; no biographies or discovery enrichment are fetched.
func (p *Providers) tmdbUnionCast(ctx context.Context, links []tmTVEpisode, eps []cinemeta.Episode) ([]PersonSummary, error) {
	if len(links) == 0 || len(links) > 200 || len(links) != len(eps) {
		return nil, ErrPartial
	}
	type result struct {
		people []PersonSummary
		err    error
	}
	out := make([]result, len(links))
	var wg sync.WaitGroup
	gate := make(chan struct{}, 4)
	for i := range links {
		select {
		case gate <- struct{}{}:
		case <-ctx.Done():
			wg.Wait()
			return nil, ctx.Err()
		}
		wg.Go(func() {
			defer func() { <-gate }()
			_, out[i].people, out[i].err = p.tmdbEpisodeRead(ctx, links[i], eps[i].TVDBID)
		})
	}
	wg.Wait()
	people := []PersonSummary{}
	seen := map[string]bool{}
	for _, v := range out {
		if v.err != nil {
			return nil, v.err
		}
		for _, person := range v.people {
			key := person.ID + ":" + person.Role
			if !seen[key] {
				seen[key] = true
				people = append(people, person)
			}
		}
	}
	return people, nil
}

func (p *Providers) tmdbTVDBCast(ctx context.Context, r Request, d Document) (Document, error) {
	d.People = nil
	if !p.Enabled("Tmdb") {
		d.People = nil
		return d, ErrUnconfigured
	}
	eps := r.CanonicalEpisodes
	if r.Kind == "season" {
		// Future/undated appearances may not exist in TMDb yet. They must not
		// block the cast of the season's already released episodes.
		eps = nil
		today := time.Now().UTC().Format(time.DateOnly)
		for _, ep := range r.CanonicalEpisodes {
			if ep.ReleaseDate != "" && ep.ReleaseDate <= today {
				eps = append(eps, ep)
			}
		}
	}
	if len(eps) == 0 && r.Kind == "episode" {
		id, _ := strconv.ParseInt(d.ProviderID, 10, 64)
		eps = []cinemeta.Episode{{Number: r.Number, Season: r.Season, TVDBID: id}}
	}
	if len(eps) == 0 {
		return d, ErrAmbiguous
	}
	if len(eps) > 200 {
		return d, ErrPartial
	}
	links := make([]tmTVEpisode, len(eps))
	for i, ep := range eps {
		v, err := p.tmdbEpisodeLink(ctx, ep.TVDBID)
		if err != nil {
			d.People = nil
			return d, err
		}
		if ep.TMDBID > 0 && ep.TMDBID != v.ID {
			d.People = nil
			return d, ErrAmbiguous
		}
		links[i] = v
	}
	people, err := p.tmdbUnionCast(ctx, links, eps)
	if err != nil {
		d.People = nil
		return d, err
	}
	d.People = people
	d.ScopedCastChecked = true
	if d.FieldSources == nil {
		d.FieldSources = map[string]string{}
	}
	d.FieldSources["People"] = "Tmdb"
	return d, nil
}
