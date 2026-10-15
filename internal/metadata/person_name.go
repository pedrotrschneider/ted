package metadata

import (
	"context"
	"net/url"
	"strconv"
	"strings"
)

type personCandidate struct {
	ID         int64
	Name       string
	Department string `json:"known_for_department"`
	Popularity float64
	KnownFor   []struct {
		Title, Name string
		MediaType   string `json:"media_type"`
	} `json:"known_for"`
}

// Search is a bounded heuristic: names, known titles, acting department, then
// popularity; equal ranks retain TMDb's ordering. Search also covers aliases.
// Read at most two pages and fetch details for only the selected candidate.
func (p *Providers) personByName(ctx context.Context, doc Document) (string, string, error) {
	name := strings.TrimSpace(doc.Item.Name)
	if normalized(name) == "" || len(name) > 512 {
		return "", "", ErrPartial
	}
	var selected personCandidate
	var best [4]float64
	seen := map[int64]bool{}
	for page := 1; page <= 2; page++ {
		var response struct {
			Page       int
			TotalPages *int `json:"total_pages"`
			Results    *[]personCandidate
		}
		query := url.Values{"query": {name}, "include_adult": {"false"}, "page": {strconv.Itoa(page)}}
		if err := p.get(ctx, "Tmdb", "/search/person?"+query.Encode(), &response); err != nil {
			return "", "", err
		}
		if response.Results == nil || response.TotalPages == nil || response.Page != page || *response.TotalPages < 0 || len(*response.Results) > 20 || (len(*response.Results) > 0 && *response.TotalPages < page) || (len(*response.Results) == 0 && *response.TotalPages >= page) {
			return "", "", ErrPartial
		}
		for _, candidate := range *response.Results {
			if candidate.ID <= 0 || normalized(candidate.Name) == "" {
				return "", "", ErrPartial
			}
			if seen[candidate.ID] {
				continue
			}
			seen[candidate.ID] = true
			rank := personNameRank(doc, candidate)
			if selected.ID == 0 || betterPersonRank(rank, best) {
				selected, best = candidate, rank
			}
		}
		if *response.TotalPages <= page {
			break
		}
	}
	if selected.ID == 0 {
		return "", "", nil // Confirmed empty search, reusable until refresh.
	}
	return strconv.FormatInt(selected.ID, 10), selected.Name, nil
}

func personNameRank(doc Document, candidate personCandidate) [4]float64 {
	rank := [4]float64{0, 0, 0, candidate.Popularity}
	if normalized(doc.Item.Name) == normalized(candidate.Name) {
		rank[0] = 1
	}
	matched := map[string]bool{}
	for _, known := range candidate.KnownFor {
		name, kind := known.Title, "movie"
		if known.MediaType == "tv" {
			name, kind = known.Name, "series"
		} else if known.MediaType != "movie" {
			continue
		}
		key := kind + ":" + normalized(name)
		for _, credit := range doc.Credits {
			if credit.Kind == kind && normalized(name) != "" && normalized(name) == normalized(credit.Item.Name) && !matched[key] {
				matched[key] = true
				rank[1]++
			}
		}
	}
	if strings.EqualFold(candidate.Department, "Acting") {
		rank[2] = 1
	}
	return rank
}

func betterPersonRank(candidate, previous [4]float64) bool {
	for i, value := range candidate {
		if value != previous[i] {
			return value > previous[i]
		}
	}
	return false
}
