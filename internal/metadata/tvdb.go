package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type tvRemote struct{ ID, SourceName string }
type tvTranslation struct {
	Language, Name, Overview string
	IsPrimary, IsAlias       bool
}
type tvTranslations struct{ NameTranslations, OverviewTranslations []tvTranslation }

func (t *tvTranslations) UnmarshalJSON(raw []byte) error {
	if strings.HasPrefix(strings.TrimSpace(string(raw)), "[") {
		var translations []tvTranslation
		if e := json.Unmarshal(raw, &translations); e != nil {
			return e
		}
		t.NameTranslations, t.OverviewTranslations = translations, translations
		return nil
	}
	type object tvTranslations
	return json.Unmarshal(raw, (*object)(t))
}

type tvCompany struct {
	ID   int64
	Name string
}
type tvCompanies []tvCompany

func (c *tvCompanies) UnmarshalJSON(raw []byte) error {
	if strings.HasPrefix(strings.TrimSpace(string(raw)), "{") {
		var groups struct {
			Studio, Network, Production, Distributor []tvCompany
			SpecialEffects                           []tvCompany `json:"special_effects"`
		}
		if e := json.Unmarshal(raw, &groups); e != nil {
			return e
		}
		*c = append(append(append(append(groups.Studio, groups.Network...), groups.Production...), groups.Distributor...), groups.SpecialEffects...)
		return nil
	}
	type list tvCompanies
	return json.Unmarshal(raw, (*list)(c))
}

type tvRecordInfo struct{ Name, Image, Year string }
type tvImage struct {
	Image    string
	Type     int
	Language string
	Score    float64
}
type tvSeason struct {
	ID, Number, SeriesID int64
	Name, Image          string
	Type                 struct {
		ID         int
		Name, Type string
	}
}
type tvCharacter struct {
	ID, PeopleID, MovieID, SeriesID       int64
	Name, PersonName, Image, PersonImgURL string
	Type, Sort                            int
	PeopleType                            string
	Movie, Series                         *tvRecordInfo
}
type tvRecord struct {
	ID                                                                                                                int64
	Name, Overview, Image, FirstAired, LastAired, Year, OriginalLanguage, Country, Birth, Death, BirthPlace, AirsTime string
	Runtime, AverageRuntime, Number, SeasonNumber                                                                     int
	SeriesID                                                                                                          int64
	Aired                                                                                                             string
	DefaultSeasonType                                                                                                 int
	Status                                                                                                            struct{ Name string }
	AirsDays                                                                                                          map[string]bool
	RemoteIDs                                                                                                         []tvRemote `json:"remoteIds"`
	Genres                                                                                                            []struct{ Name string }
	Companies                                                                                                         tvCompanies
	ContentRatings                                                                                                    []struct{ Name, Country string }
	Characters                                                                                                        *[]tvCharacter
	Artworks                                                                                                          []tvImage
	Artwork                                                                                                           []tvImage
	Seasons                                                                                                           []tvSeason
	Episodes                                                                                                          *[]tvRecord
	Trailers                                                                                                          []struct{ Name, URL string }
	Translations                                                                                                      *tvTranslations
	Biographies                                                                                                       []struct{ Biography, Language string }
}

func remoteIDs(remote []tvRemote) map[string]string {
	ids := map[string]string{}
	for _, v := range remote {
		switch strings.ToLower(v.SourceName) {
		case "imdb":
			ids["Imdb"] = v.ID
		case "themoviedb.com", "the movie database", "tmdb", "themoviedb":
			ids["Tmdb"] = v.ID
		}
	}
	return ids
}
func (p *Providers) tvTypes(ctx context.Context) (map[int]string, map[int]string, error) {
	p.mu.Lock()
	if time.Now().Before(p.typesUntil) {
		a, b := p.artworkTypes, p.peopleTypes
		p.mu.Unlock()
		return a, b, nil
	}
	p.mu.Unlock()
	a, b := map[int]string{}, map[int]string{}
	for _, v := range []struct {
		Path   string
		Target map[int]string
	}{{"/artwork/types", a}, {"/people/types", b}} {
		var data struct {
			Status string
			Data   *[]struct {
				ID   int
				Name string
			}
		}
		if e := p.get(ctx, "Tvdb", v.Path, &data); e != nil {
			return nil, nil, e
		}
		if data.Status != "success" || data.Data == nil {
			return nil, nil, ErrPartial
		}
		for _, entry := range *data.Data {
			if entry.ID <= 0 || entry.Name == "" {
				return nil, nil, ErrPartial
			}
			v.Target[entry.ID] = entry.Name
		}
	}
	p.mu.Lock()
	p.artworkTypes = a
	p.peopleTypes = b
	p.typesUntil = time.Now().Add(24 * time.Hour)
	p.mu.Unlock()
	return a, b, nil
}
func (p *Providers) tvdbSeriesID(ctx context.Context, imdb string) (string, error) {
	if !titleID.MatchString(imdb) {
		return "", ErrInvalid
	}
	var found struct {
		Status string
		Data   *[]struct{ Series *struct{ ID int64 } }
	}
	if e := p.get(ctx, "Tvdb", "/search/remoteid/"+imdb, &found); e != nil {
		return "", e
	}
	if found.Status != "success" || found.Data == nil {
		return "", ErrPartial
	}
	ids := map[int64]bool{}
	for _, entry := range *found.Data {
		if entry.Series != nil && entry.Series.ID > 0 {
			ids[entry.Series.ID] = true
		}
	}
	if len(ids) == 0 {
		return "", ErrMissing
	}
	if len(ids) != 1 {
		return "", ErrAmbiguous
	}
	for id := range ids {
		return strconv.FormatInt(id, 10), nil
	}
	return "", ErrMissing
}
func (p *Providers) tvdb(ctx context.Context, r Request) (Document, error) {
	var d Document
	id := r.ProviderID
	parent := r.ParentProviderID
	if r.Kind == "series" && id == "" {
		var e error
		id, e = p.tvdbSeriesID(ctx, r.TitleID)
		if e != nil {
			return d, e
		}
	}
	if (r.Kind == "season" || r.Kind == "episode") && parent == "" {
		var e error
		parent, e = p.tvdbSeriesID(ctx, r.TitleID)
		if e != nil {
			return d, e
		}
	}
	if r.Kind == "season" && id == "" {
		var root struct {
			Status string
			Data   *tvRecord
		}
		if e := p.get(ctx, "Tvdb", "/series/"+parent+"/extended?short=true", &root); e != nil {
			return d, e
		}
		if root.Status != "success" || root.Data == nil {
			return d, ErrPartial
		}
		for _, s := range root.Data.Seasons {
			if int(s.Number) == r.Season && s.Type.Type == "official" {
				if id != "" && id != fmt.Sprint(s.ID) {
					return d, ErrAmbiguous
				}
				id = fmt.Sprint(s.ID)
			}
		}
		if id == "" {
			return d, ErrMissing
		}
	}
	if r.Kind == "episode" && id == "" {
		var found struct {
			Status string
			Data   *struct{ Episodes *[]tvRecord }
		}
		path := fmt.Sprintf("/series/%s/episodes/official?page=0&season=%d&episodeNumber=%d", parent, r.Season, r.Number)
		if e := p.get(ctx, "Tvdb", path, &found); e != nil {
			return d, e
		}
		if found.Status != "success" || found.Data == nil || found.Data.Episodes == nil {
			return d, ErrPartial
		}
		for _, ep := range *found.Data.Episodes {
			if ep.SeasonNumber == r.Season && ep.Number == r.Number {
				if id != "" && id != fmt.Sprint(ep.ID) {
					return d, ErrAmbiguous
				}
				id = fmt.Sprint(ep.ID)
			}
		}
		if id == "" {
			return d, ErrMissing
		}
	}
	if !positiveID.MatchString(id) {
		return d, ErrInvalid
	}
	endpoint := ""
	switch r.Kind {
	case "series":
		endpoint = "series"
	case "season":
		endpoint = "seasons"
	case "episode":
		endpoint = "episodes"
	case "person":
		endpoint = "people"
	default:
		return d, ErrInvalid
	}
	var result struct {
		Status string
		Data   *tvRecord
	}
	path := "/" + endpoint + "/" + id + "/extended"
	if r.Kind != "season" {
		path += "?meta=translations"
	}
	if e := p.get(ctx, "Tvdb", path, &result); e != nil {
		return d, e
	}
	if result.Status != "success" || result.Data == nil || fmt.Sprint(result.Data.ID) != id {
		return d, ErrPartial
	}
	v := result.Data
	// TVDB seasons may have no name; their verified canonical number supplies the label.
	if r.Kind == "season" && strings.TrimSpace(v.Name) == "" {
		v.Name = fmt.Sprintf("Season %d", r.Season)
	}
	if strings.TrimSpace(v.Name) == "" {
		return d, ErrPartial
	}
	if r.Kind == "series" && remoteIDs(v.RemoteIDs)["Imdb"] != r.TitleID {
		return d, ErrAmbiguous
	}
	if (r.Kind == "season" || r.Kind == "episode") && fmt.Sprint(v.SeriesID) != parent {
		return d, ErrAmbiguous
	}
	if r.Kind == "season" && v.Number != r.Season {
		return d, ErrAmbiguous
	}
	// Provider identity and the official episode tuple govern enrichment.
	// Names and release dates are descriptive values, not identity anchors.
	if r.Kind == "episode" && (v.SeasonNumber != r.Season || v.Number != r.Number) {
		return d, ErrAmbiguous
	}
	d.ProviderID = id
	if r.Kind == "series" {
		d.OriginalLanguage, d.OriginalLanguageChecked = originalLanguage(v.OriginalLanguage), true
	}
	d.Item = Item{Name: v.Name, OriginalTitle: v.Name, Overview: v.Overview, ProviderIDs: remoteIDs(v.RemoteIDs), PreferredMetadataLanguage: p.config.Language, PreferredMetadataCountryCode: p.config.Country}
	d.Item.ProviderIDs["Tvdb"] = id
	if r.Kind == "person" {
		ids, err := personRemoteIDs(v.RemoteIDs)
		if err != nil {
			d.Item.ProviderIDs = nil // Preserve retained IDs instead of publishing conflicting links.
			return d, err
		}
		ids["Tvdb"] = id
		d.Item.ProviderIDs = ids
	}
	language := tvLanguage(p.config.Language)
	if v.Translations != nil {
		var name, overview *tvTranslation
		for _, tr := range v.Translations.NameTranslations {
			if tr.Language == language && tr.Name != "" && !tr.IsAlias && (name == nil || tr.IsPrimary && !name.IsPrimary) {
				name = &tr
			}
		}
		for _, tr := range v.Translations.OverviewTranslations {
			if tr.Language == language && tr.Overview != "" && !tr.IsAlias && (overview == nil || tr.IsPrimary && !overview.IsPrimary) {
				overview = &tr
			}
		}
		if name != nil {
			d.Item.Name = name.Name
		}
		if overview != nil {
			d.Item.Overview = overview.Overview
		}
	}
	switch r.Kind {
	case "series":
		d.Item.Type = "Series"
		d.Item.PremiereDate = date(v.FirstAired)
		d.Item.ProductionYear = year(v.FirstAired)
		d.Item.RunTimeTicks = ticks(v.AverageRuntime)
		d.Item.Status = v.Status.Name
		if strings.EqualFold(v.Status.Name, "Ended") {
			d.Item.EndDate = date(v.LastAired)
		}
		d.Item.AirTime = v.AirsTime
		for k, on := range v.AirsDays {
			if on && k != "" {
				d.Item.AirDays = append(d.Item.AirDays, strings.ToUpper(k[:1])+strings.ToLower(k[1:]))
			}
		}
		sort.Strings(d.Item.AirDays)
		for _, s := range v.Seasons {
			if s.Type.Type == "official" && s.ID > 0 {
				d.Children = append(d.Children, Child{Request: Request{Kind: "season", TitleID: r.TitleID, ProviderID: fmt.Sprint(s.ID), ParentProviderID: id, Season: int(s.Number)}})
			}
		}
	case "season":
		d.Item.Type = "Season"
		d.Item.IndexNumber = &r.Season
		if v.Episodes == nil {
			return Document{}, ErrPartial
		}
		seen := map[int]bool{}
		for _, ep := range *v.Episodes {
			if ep.ID <= 0 || ep.SeriesID != v.SeriesID || ep.SeasonNumber != r.Season || seen[ep.Number] {
				return Document{}, ErrPartial
			}
			seen[ep.Number] = true
			if released := date(ep.Aired); released != nil && (d.Item.PremiereDate == nil || released.Before(*d.Item.PremiereDate)) {
				d.Item.PremiereDate = released
				d.Item.ProductionYear = year(ep.Aired)
			}
			d.Children = append(d.Children, Child{Request: Request{Kind: "episode", TitleID: r.TitleID, ProviderID: fmt.Sprint(ep.ID), ParentProviderID: parent, Season: ep.SeasonNumber, Number: ep.Number, EpisodeName: ep.Name, EpisodeDate: ep.Aired}, Image: imageURL(ep.Image)})
		}
	case "episode":
		d.Item.Type = "Episode"
		d.Item.IndexNumber = &r.Number
		d.Item.ParentIndexNumber = &r.Season
		d.Item.PremiereDate = date(v.Aired)
		d.Item.ProductionYear = year(v.Aired)
		d.Item.RunTimeTicks = ticks(v.Runtime)
	case "person":
		d.Item.Type = "Person"
		d.Item.PremiereDate = date(v.Birth)
		d.Item.EndDate = date(v.Death)
		if v.BirthPlace != "" {
			d.Item.ProductionLocations = []string{v.BirthPlace}
		}
		for _, bio := range v.Biographies {
			if bio.Language == language && strings.TrimSpace(bio.Biography) != "" {
				d.Item.Overview = bio.Biography
				break
			}
		}
		if v.Characters == nil {
			return d, ErrPartial
		}
		d.Credits = []Credit{}
		d.TVDBAppearanceChecked = true
		seen := map[string]int{}
		for _, c := range *v.Characters {
			for _, entry := range []struct {
				Kind   string
				Record *tvRecordInfo
				ID     int64
			}{{"movie", c.Movie, c.MovieID}, {"series", c.Series, c.SeriesID}} {
				if entry.ID == 0 {
					continue
				}
				if entry.ID < 0 || entry.Record == nil || entry.Record.Name == "" {
					d.Credits = nil
					return d, ErrPartial
				}
				key := entry.Kind + ":" + fmt.Sprint(entry.ID)
				if i, ok := seen[key]; ok {
					d.Credits[i].Roles = appendRole(d.Credits[i].Roles, c.Name)
					if d.Credits[i].CharacterImage == "" {
						d.Credits[i].CharacterImage = imageURL(c.Image)
					}
					continue
				}
				seen[key] = len(d.Credits)
				t := "Movie"
				if entry.Kind == "series" {
					t = "Series"
				}
				var produced *int
				if n, e := strconv.Atoi(entry.Record.Year); e == nil && n > 0 {
					produced = &n
				}
				d.Credits = append(d.Credits, Credit{Provider: "Tvdb", Kind: entry.Kind, ID: fmt.Sprint(entry.ID), Poster: imageURL(entry.Record.Image), CharacterImage: imageURL(c.Image), Roles: appendRole(nil, c.Name), Item: Item{Type: t, Name: entry.Record.Name, ProductionYear: produced, ProviderIDs: map[string]string{"Tvdb": fmt.Sprint(entry.ID)}}})
			}
		}
	}
	for _, g := range v.Genres {
		d.Item.Genres = append(d.Item.Genres, g.Name)
	}
	for _, c := range v.Companies {
		d.Item.Studios = append(d.Item.Studios, NameID{c.Name, guid(fmt.Sprintf("Tvdb:studio:%d", c.ID))})
	}
	for _, rating := range v.ContentRatings {
		if strings.EqualFold(rating.Country, p.config.Country) || (p.config.Country == "US" && strings.EqualFold(rating.Country, "usa")) {
			d.Item.OfficialRating = rating.Name
			break
		}
	}
	for _, tr := range v.Trailers {
		if u := publicLink(tr.URL); u != "" {
			d.Item.RemoteTrailers = append(d.Item.RemoteTrailers, URL{Name: tr.Name, URL: u})
		}
	}
	if img := imageURL(v.Image); img != "" {
		d.Images = append(d.Images, Image{Type: "Primary", URL: img})
	}
	if r.Kind == "person" {
		if len(d.Credits) > 10000 {
			return Document{}, ErrPartial
		}
		return d, nil
	}
	artworkTypes, peopleTypes, e := p.tvTypes(ctx)
	if e != nil {
		return d, e
	}
	if r.Kind != "season" {
		if v.Characters == nil {
			return d, ErrPartial
		}
		d.People = []PersonSummary{}
		for _, c := range *v.Characters {
			if c.PeopleID <= 0 || c.PersonName == "" {
				d.People = nil
				return d, ErrPartial
			}
			kind := personKind(c.PeopleType)
			if kind == "Unknown" {
				kind = personKind(peopleTypes[c.Type])
			}
			d.People = append(d.People, PersonSummary{"Tvdb", fmt.Sprint(c.PeopleID), c.PersonName, c.Name, kind, imageURL(c.PersonImgURL)})
		}
		d.TVDBCastTypesChecked = true
	}
	counters := map[string]int{}
	for _, img := range d.Images {
		counters[img.Type]++
	}
	images := append(v.Artworks, v.Artwork...)
	sort.SliceStable(images, func(i, j int) bool { return images[i].Score > images[j].Score })
	for _, img := range images {
		kind := artworkKind(artworkTypes[img.Type])
		u := imageURL(img.Image)
		if kind == "" || u == "" {
			continue
		}
		if (r.Kind == "series" || r.Kind == "season") && !editableImageType(r.Kind, kind) {
			continue
		}
		if counters[kind] > 0 {
			continue
		}
		d.Images = append(d.Images, Image{Type: kind, URL: u})
		counters[kind]++
	}
	if len(d.People) > 10000 || len(d.Children) > 5000 {
		return Document{}, ErrPartial
	}
	return d, nil
}
func tvLanguage(language string) string {
	switch strings.Split(language, "-")[0] {
	case "en":
		return "eng"
	case "pt":
		return "por"
	case "es":
		return "spa"
	case "fr":
		return "fra"
	case "de":
		return "deu"
	case "ja":
		return "jpn"
	case "zh":
		return "zho"
	case "ko":
		return "kor"
	default:
		return language
	}
}
func personKind(name string) string {
	s := strings.ToLower(name)
	compact := strings.NewReplacer(" ", "", "-", "", "_", "").Replace(s)
	switch {
	case strings.Contains(s, "actor") || compact == "gueststar":
		return "Actor"
	case strings.Contains(s, "director"):
		return "Director"
	case strings.Contains(s, "writer"):
		return "Writer"
	case strings.Contains(s, "producer"):
		return "Producer"
	case strings.Contains(s, "composer"):
		return "Composer"
	default:
		return "Unknown"
	}
}
func artworkKind(name string) string {
	s := strings.ToLower(name)
	switch {
	case strings.Contains(s, "poster"):
		return "Primary"
	case strings.Contains(s, "background"):
		return "Backdrop"
	case strings.Contains(s, "banner"):
		return "Banner"
	case strings.Contains(s, "logo"):
		return "Logo"
	case strings.Contains(s, "clearart"):
		return "Art"
	case strings.Contains(s, "clearlogo"):
		return "Logo"
	case strings.Contains(s, "thumb"):
		return "Thumb"
	default:
		return ""
	}
}
func normalized(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, s)
}
