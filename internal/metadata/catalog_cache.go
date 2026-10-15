package metadata

import (
	"context"

	"entertainmentdistrict/internal/catalog"
)

// CatalogSnapshots reads only retained movie/show enrichment in two batched
// queries. Browsing never creates entities or schedules provider/people jobs.
func (s *Service) CatalogSnapshots(ctx context.Context, media []catalog.MediaIdentity) (map[catalog.MediaIdentity]Snapshot, error) {
	keys := []string{}
	identities := map[string]catalog.MediaIdentity{}
	for _, m := range media {
		if m.Kind != "movie" && m.Kind != "series" {
			continue
		}
		key, err := identity("Tvdb", Request{Kind: m.Kind, TitleID: m.ID})
		if err != nil {
			return nil, err
		}
		id := guid(key)
		if _, exists := identities[id]; !exists {
			keys = append(keys, id)
			identities[id] = m
		}
	}
	result := map[catalog.MediaIdentity]Snapshot{}
	if len(keys) == 0 {
		return result, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT id::text,item,status,provider,generation,fetched_at,error_code,field_sources FROM metadata_entities WHERE id=ANY($1::uuid[])`, keys)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		v := Snapshot{Images: []Asset{}}
		if err = rows.Scan(&id, &v.Item, &v.Status, &v.Provider, &v.Generation, &v.FetchedAt, &v.Error, &v.FieldSources); err != nil {
			break
		}
		v.Item.ImageTags = map[string]string{}
		v.Item.BackdropImageTags = []string{}
		result[identities[id]] = v
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil || len(result) == 0 {
		return result, err
	}
	rows, err = s.pool.Query(ctx, `SELECT entity_id::text,id::text,image_type,image_index,status,digest,error_code,generation,user_modified FROM metadata_artwork WHERE entity_id=ANY($1::uuid[]) AND image_index=0 ORDER BY entity_id,image_type`, keys)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var a Asset
		if err = rows.Scan(&id, &a.ID, &a.Type, &a.Index, &a.Status, &a.Tag, &a.Error, &a.Generation, &a.UserModified); err != nil {
			return nil, err
		}
		m := identities[id]
		v := result[m]
		a.URL = "/api/client/v1/metadata/images/" + a.ID
		v.Images = append(v.Images, a)
		if a.Tag != "" {
			if a.Type == "Backdrop" {
				v.Item.BackdropImageTags = append(v.Item.BackdropImageTags, a.Tag)
			} else {
				v.Item.ImageTags[a.Type] = a.Tag
			}
		}
		result[m] = v
	}
	return result, rows.Err()
}
