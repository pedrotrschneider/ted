package metadata

import (
	"context"
	"fmt"
	"sort"
)

// Artwork is an explicit administrator lookup, separate from lazy descriptions.
// It never fetches biographies or enrolls every returned image in the cache.
func (p *Providers) Artwork(ctx context.Context, provider string, r Request) ([]ArtworkChoice, error) {
	if !positiveID.MatchString(r.ProviderID) {
		return nil, ErrPartial
	}
	result := []ArtworkChoice{}
	add := func(kind, address, preview, language string, width, height int) {
		if !editableImageType(r.Kind, kind) || imageURL(address) == "" {
			return
		}
		if imageURL(preview) == "" {
			preview = address
		}
		result = append(result, ArtworkChoice{Type: kind, URL: address, Preview: preview, Language: language, Width: width, Height: height})
	}
	switch provider {
	case "Tmdb":
		if r.Kind != "movie" {
			return nil, ErrInvalid
		}
		type image struct {
			Path          string `json:"file_path"`
			Language      string `json:"iso_639_1"`
			Width, Height int
			Rating        float64 `json:"vote_average"`
		}
		var v struct {
			ID                        int64
			Posters, Backdrops, Logos *[]image
		}
		if !p.Enabled(provider) {
			return nil, ErrUnconfigured
		}
		// Descriptions stay English; an artwork editor offers every language.
		if err := p.request(ctx, "GET", p.tmdbURL+"/movie/"+r.ProviderID+"/images", p.config.TMDBToken, nil, &v); err != nil {
			return nil, err
		}
		if fmt.Sprint(v.ID) != r.ProviderID || v.Posters == nil || v.Backdrops == nil || v.Logos == nil {
			return nil, ErrPartial
		}
		for _, group := range []struct {
			kind   string
			images []image
		}{{"Primary", *v.Posters}, {"Backdrop", *v.Backdrops}, {"Logo", *v.Logos}} {
			sort.SliceStable(group.images, func(i, j int) bool { return group.images[i].Rating > group.images[j].Rating })
			for _, img := range group.images {
				add(group.kind, tmImage(img.Path), "https://image.tmdb.org/t/p/w300"+img.Path, img.Language, img.Width, img.Height)
			}
		}
	case "Tvdb":
		if r.Kind != "series" && r.Kind != "season" {
			return nil, ErrInvalid
		}
		types, _, err := p.tvTypes(ctx)
		if err != nil {
			return nil, err
		}
		var v struct {
			Status string
			Data   *struct {
				ID                int64
				Image             string
				Artworks, Artwork *[]struct {
					tvImage
					Thumbnail     string
					Width, Height int
				}
			}
		}
		path := "/series/" + r.ProviderID + "/artworks"
		if r.Kind == "season" {
			path = "/seasons/" + r.ProviderID + "/extended"
		}
		if err = p.get(ctx, provider, path, &v); err != nil {
			return nil, err
		}
		if v.Status != "success" || v.Data == nil || fmt.Sprint(v.Data.ID) != r.ProviderID {
			return nil, ErrPartial
		}
		if v.Data.Artworks == nil && v.Data.Artwork == nil && r.Kind == "series" {
			return nil, ErrPartial
		}
		if v.Data.Image != "" {
			add("Primary", v.Data.Image, v.Data.Image, "", 0, 0)
		}
		for _, group := range []bool{true, false} {
			images := v.Data.Artworks
			if !group {
				images = v.Data.Artwork
			}
			if images == nil {
				continue
			}
			sort.SliceStable(*images, func(i, j int) bool { return (*images)[i].Score > (*images)[j].Score })
			for _, img := range *images {
				add(artworkKind(types[img.Type]), img.Image, img.Thumbnail, img.Language, img.Width, img.Height)
			}
		}
	default:
		return nil, ErrInvalid
	}
	return normalizeChoices(result)
}

func (s *StoredProviders) Artwork(ctx context.Context, provider string, r Request) ([]ArtworkChoice, error) {
	p, err := s.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	return p.Artwork(ctx, provider, r)
}
