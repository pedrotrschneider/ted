package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"entertainmentdistrict/internal/metadata"
)

func TestNativeFilmographyCursorContract(t *testing.T) {
	for _, count := range []int{0, 1, 21} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			f := setup(t)
			installMetadata(t, f)
			ctx := context.Background()
			provider := "Tmdb"
			if count == 1 {
				provider = "Tvdb"
			}
			person, err := f.api.Metadata.Ensure(ctx, provider, metadata.Request{Kind: "person", ProviderID: "10"}, metadata.Item{Type: "Person", Name: "Actor"})
			if err != nil {
				t.Fatal(err)
			}
			for i := 1; i <= count; i++ {
				ref := fmt.Sprintf("22222222-2222-4222-8222-%012d", i)
				_, err = f.pool.Exec(ctx, `INSERT INTO metadata_credits(person_id,provider,kind,provider_id,reference_id,summary,identity_status,roles,generation) VALUES($1,$2,'movie',$3,$4::uuid,jsonb_build_object('Id',$4::text,'Type','Movie','Name','Credit'),'missing','["Character"]',1)`, person.Item.ID, provider, fmt.Sprint(i), ref)
				if err != nil {
					t.Fatal(err)
				}
			}
			h := f.api.Handler()
			pair := nativeHTTPLogin(t, h)
			base := "/api/client/v1/metadata/people/" + person.Item.ID
			requireStatus(t, nativeCall(t, h, "GET", base, pair.AccessToken, nil, nil), 200)
			total, after := 0, ""
			seen := map[string]bool{}
			for {
				result := nativeCall(t, h, "GET", base+"/filmography"+after, pair.AccessToken, nil, nil)
				requireStatus(t, result, 200)
				var page struct {
					Items []metadata.FilmographyEntry `json:"items"`
					Next  *string                     `json:"next_after"`
				}
				if err := json.Unmarshal(result.Body.Bytes(), &page); err != nil {
					t.Fatal(err)
				}
				if page.Items == nil || len(page.Items) > 20 {
					t.Fatal("invalid items", page)
				}
				for _, entry := range page.Items {
					if seen[entry.Item.ID] {
						t.Fatal("duplicate credit", entry.Item.ID)
					}
					seen[entry.Item.ID] = true
				}
				total += len(page.Items)
				if total == count {
					var raw map[string]json.RawMessage
					if err := json.Unmarshal(result.Body.Bytes(), &raw); err != nil {
						t.Fatal(err)
					}
					if _, present := raw["next_after"]; present {
						t.Fatalf("terminal page must omit next_after, got %s", raw["next_after"])
					}
					break
				}
				if total > count || page.Next == nil || *page.Next == "" || len(page.Items) != 20 || *page.Next != page.Items[len(page.Items)-1].Item.ID {
					t.Fatalf("invalid continuation: %+v", page)
				}
				after = "?after=" + *page.Next
			}
		})
	}
}
