package metadata

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"entertainmentdistrict/internal/cinemeta"
)

type seriesStructureReader interface {
	SeriesStructure(context.Context, string, cinemeta.Series, Document) (cinemeta.Series, error)
}

// Provider ordering is presentation/scheduling. Every playable episode keeps
// an existing Cinemeta identity, verified by an exact external episode ID.
func (p *Providers) SeriesStructure(ctx context.Context, provider string, canonical cinemeta.Series, doc Document) (cinemeta.Series, error) {
	result := cinemeta.Series{Movie: canonical.Movie, Episodes: []cinemeta.Episode{}}
	byTVDB, byTMDB := map[int64]cinemeta.Episode{}, map[int64]cinemeta.Episode{}
	for _, ep := range canonical.Episodes {
		for _, v := range []struct {
			id    int64
			index map[int64]cinemeta.Episode
		}{{ep.TVDBID, byTVDB}, {ep.TMDBID, byTMDB}} {
			if v.id <= 0 {
				continue
			}
			if old, ok := v.index[v.id]; ok && old.ID != ep.ID {
				return result, ErrAmbiguous
			}
			v.index[v.id] = ep
		}
	}
	type fetched struct {
		episodes []cinemeta.Episode
		err      error
	}
	out := make([]fetched, len(doc.Children))
	var wg sync.WaitGroup
	gate := make(chan struct{}, 4)
	for i, child := range doc.Children {
		if child.Request.Kind != "season" {
			continue
		}
		select {
		case gate <- struct{}{}:
		case <-ctx.Done():
			wg.Wait()
			return result, ctx.Err()
		}
		wg.Go(func() {
			defer func() { <-gate }()
			out[i].episodes, out[i].err = p.structureSeason(ctx, provider, doc.ProviderID, child.Request.Season, child.Request.ProviderID, byTVDB, byTMDB)
		})
	}
	wg.Wait()
	ids, tuples := map[string]bool{}, map[string]bool{}
	for _, part := range out {
		if part.err != nil {
			return result, part.err
		}
		for _, ep := range part.episodes {
			tuple := fmt.Sprintf("%d:%d", ep.Season, ep.Number)
			if ids[ep.ID] || tuples[tuple] {
				return result, ErrAmbiguous
			}
			ids[ep.ID], tuples[tuple] = true, true
			result.Episodes = append(result.Episodes, ep)
			if len(result.Episodes) > 5000 {
				return result, ErrPartial
			}
		}
	}
	sort.Slice(result.Episodes, func(i, j int) bool {
		a, b := result.Episodes[i], result.Episodes[j]
		return a.Season < b.Season || a.Season == b.Season && a.Number < b.Number
	})
	return result, nil
}

func (p *Providers) structureSeason(ctx context.Context, provider, parent string, number int, id string, byTVDB, byTMDB map[int64]cinemeta.Episode) ([]cinemeta.Episode, error) {
	result := []cinemeta.Episode{}
	if !positiveID.MatchString(parent) || !positiveID.MatchString(id) {
		return nil, ErrInvalid
	}
	if provider == "Tvdb" {
		var response struct {
			Status string
			Data   *tvRecord
		}
		if err := p.get(ctx, provider, "/seasons/"+id+"/extended", &response); err != nil {
			return nil, err
		}
		v := response.Data
		if response.Status != "success" || v == nil || fmt.Sprint(v.ID) != id || fmt.Sprint(v.SeriesID) != parent || v.Number != number || v.Episodes == nil {
			return nil, ErrAmbiguous
		}
		for _, ep := range *v.Episodes {
			if ep.ID <= 0 || fmt.Sprint(ep.SeriesID) != parent || ep.SeasonNumber != number {
				return nil, ErrAmbiguous
			}
			original, ok := byTVDB[ep.ID]
			if !ok && p.Enabled("Tmdb") {
				linked, err := p.tmdbEpisodeLink(ctx, ep.ID)
				if err != nil {
					return nil, err
				}
				original, ok = byTMDB[linked.ID]
			}
			if !ok {
				return nil, ErrAmbiguous
			}
			original.Season, original.Number = number, ep.Number
			original.Name, original.Description, original.ReleaseDate = ep.Name, ep.Overview, ep.Aired
			if original.Name == "" {
				original.Name = fmt.Sprintf("Episode %d", ep.Number)
			}
			original.Image = imageURL(ep.Image)
			original.TVDBID = ep.ID
			result = append(result, original)
		}
		return result, nil
	}
	if provider != "Tmdb" {
		return nil, ErrInvalid
	}
	var v tmTVSeason
	if err := p.get(ctx, provider, fmt.Sprintf("/tv/%s/season/%d", parent, number), &v); err != nil {
		return nil, err
	}
	if fmt.Sprint(v.ID) != id || v.Number != number || v.Episodes == nil {
		return nil, ErrAmbiguous
	}
	for _, ep := range *v.Episodes {
		if ep.ID <= 0 || ep.Season != number {
			return nil, ErrAmbiguous
		}
		original, ok := byTMDB[ep.ID]
		if !ok {
			var external struct {
				ID   int64
				TVDB int64 `json:"tvdb_id"`
			}
			if err := p.get(ctx, provider, fmt.Sprintf("/tv/%s/season/%d/episode/%d/external_ids", parent, number, ep.Number), &external); err != nil {
				return nil, err
			}
			if external.ID != ep.ID || external.TVDB <= 0 {
				return nil, ErrAmbiguous
			}
			original, ok = byTVDB[external.TVDB]
			if ok && original.TMDBID > 0 && original.TMDBID != ep.ID {
				return nil, ErrAmbiguous
			}
		}
		if !ok {
			return nil, ErrAmbiguous
		}
		original.Season, original.Number = number, ep.Number
		original.Name, original.Description, original.ReleaseDate = ep.Name, ep.Overview, ep.AirDate
		if original.Name == "" {
			original.Name = fmt.Sprintf("Episode %d", ep.Number)
		}
		original.Image = tmImage(ep.Still)
		original.TMDBID = ep.ID
		result = append(result, original)
	}
	return result, nil
}

func (s *StoredProviders) SeriesStructure(ctx context.Context, provider string, canonical cinemeta.Series, doc Document) (cinemeta.Series, error) {
	p, err := s.snapshot(ctx)
	if err != nil {
		return cinemeta.Series{}, err
	}
	return p.SeriesStructure(ctx, provider, canonical, doc)
}
