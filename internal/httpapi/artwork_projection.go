package httpapi

import (
	"context"
	"entertainmentdistrict/internal/catalog"
	"entertainmentdistrict/internal/cinemeta"
)

// Browser discovery keeps Cinemeta descriptions; only explicit administrator
// artwork overrides are projected. Never persist a browser-session image URL.
func (a *API) webArtwork(ctx context.Context, titles []cinemeta.Movie) error {
	if a.Metadata == nil || len(titles) == 0 {
		return nil
	}
	media := make([]catalog.MediaIdentity, len(titles))
	for i, t := range titles {
		media[i] = catalog.MediaIdentity{Kind: t.Type, ID: t.ID}
	}
	cached, err := a.Metadata.CatalogSnapshots(ctx, media)
	if err != nil {
		return err
	}
	for i, m := range media {
		for _, img := range cached[m].Images {
			if !img.UserModified || img.Tag == "" || img.Index != 0 {
				continue
			}
			address := "/api/metadata/images/" + img.ID + "?v=" + img.Tag
			switch img.Type {
			case "Primary":
				titles[i].Poster = address
			case "Backdrop":
				titles[i].Background = address
			case "Logo":
				titles[i].Logo = address
			}
		}
	}
	return nil
}
