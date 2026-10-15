package catalog

import "entertainmentdistrict/internal/cinemeta"

// MediaIdentity is public and stable across mappings, sources and local copies.
// Internal UUIDs remain persistence keys and are not native catalog identity.
type MediaIdentity struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

func (m MediaIdentity) Valid() bool {
	return m.Kind == "movie" && cinemeta.ValidID(m.ID) || m.Kind == "episode" && cinemeta.ValidVideoID(m.ID)
}
