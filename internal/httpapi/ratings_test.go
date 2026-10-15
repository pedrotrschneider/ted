package httpapi

import (
	"encoding/json"
	"entertainmentdistrict/internal/catalog"
	"testing"
)

func TestCatalogRatingsAuthorizationAndValidation(t *testing.T) {
	f, exec := posterFixture(t)
	h := f.api.Handler()
	requireStatus(t, call(t, h, "GET", "/api/catalog-ratings?kind=movie&ids=tt0289043", nil, nil), 401)
	cookie := login(t, h, "admin")
	for _, q := range []string{"kind=bad&ids=tt0289043", "kind=movie&ids=bad", "kind=movie&ids=tt0289043,tt0289043"} {
		requireStatus(t, call(t, h, "GET", "/api/catalog-ratings?"+q, nil, cookie), 400)
	}
	exec(`INSERT INTO catalog_ratings VALUES('tt0289043','movie',7.5,now())`)
	r := call(t, h, "GET", "/api/catalog-ratings?kind=movie&ids=tt0289043", nil, cookie)
	requireStatus(t, r, 200)
	var v struct {
		Ratings []catalog.Rating `json:"ratings"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if len(v.Ratings) != 1 || v.Ratings[0].Value == nil || *v.Ratings[0].Value != 7.5 {
		t.Fatal(v)
	}
}
