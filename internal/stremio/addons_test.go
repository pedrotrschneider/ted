package stremio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"
)

func TestAddonCollectionProtocolPreservesEmptyAndFailure(t *testing.T) {
	for _, mode := range []string{"valid", "empty", "missing", "null", "credentials", "rejected-session", "server-error"} {
		t.Run(mode, func(t *testing.T) {
			client := fake(t, func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if json.NewDecoder(r.Body).Decode(&body) != nil || r.URL.Path != "/addonCollectionGet" || body["authKey"] != "private-account-key" || body["update"] != false {
					t.Error("wrong installed collection request")
				}
				switch mode {
				case "valid":
					fmt.Fprint(w, `{"result":{"addons":[{"transportUrl":"https://example.com/private/manifest.json","manifest":{"id":"fixture","name":"Fixture"}}]}}`)
				case "empty":
					fmt.Fprint(w, `{"result":{"addons":[]}}`)
				case "missing":
					fmt.Fprint(w, `{"result":{}}`)
				case "null":
					fmt.Fprint(w, `{"result":{"addons":null}}`)
				case "credentials":
					w.WriteHeader(401)
				case "rejected-session":
					fmt.Fprint(w, `{"error":{"code":1,"message":"Session does not exist"}}`)
				case "server-error":
					fmt.Fprint(w, `{"error":{"code":500,"message":"Unavailable"}}`)
				}
			})
			collection, err := client.AddonCollection(context.Background(), "private-account-key")
			switch mode {
			case "valid":
				if err != nil || len(collection) != 1 {
					t.Fatal("valid collection failed", err)
				}
			case "empty":
				if err != nil || collection == nil || len(collection) != 0 {
					t.Fatal("empty became failure", err)
				}
			case "credentials", "rejected-session":
				if !errors.Is(err, ErrCredentials) {
					t.Fatal("credential error was not preserved", err)
				}
			case "server-error":
				if !errors.Is(err, ErrUnavailable) {
					t.Fatal("server error became empty or credentials", err)
				}
			default:
				if !errors.Is(err, ErrIncomplete) {
					t.Fatal("incomplete became empty", err)
				}
			}
		})
	}
}
