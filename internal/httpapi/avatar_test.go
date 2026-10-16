package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/profileimage"
)

func avatarUpload(t *testing.T, h http.Handler, data []byte, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("PUT", "https://ted.example/api/account/avatar", bytes.NewReader(data))
	r.Header.Set("Content-Type", "image/png")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func avatarPicture(t *testing.T, c color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 48, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 48; x++ {
			img.Set(x, y, c)
		}
	}
	var data bytes.Buffer
	if err := png.Encode(&data, img); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func TestAccountAvatarUploadNativeDeliveryAndAccountIsolation(t *testing.T) {
	f := setup(t)
	h := f.api.Handler()
	admin := login(t, h, "admin")
	created, err := f.service.Create(context.Background(), identity(f.admin), "viewer", "Viewer", testPassword, "user")
	if err != nil {
		t.Fatal(err)
	}
	viewer := login(t, h, "viewer")
	input := avatarPicture(t, color.RGBA{G: 255, A: 255})
	requireStatus(t, avatarUpload(t, h, input, nil), 401)
	requireStatus(t, call(t, h, "GET", "/api/account/avatar", nil, viewer), 204)
	r := avatarUpload(t, h, input, viewer)
	requireStatus(t, r, 200)
	var updated accounts.User
	if err := json.Unmarshal(r.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.ID != created.ID || len(updated.AvatarVersion) != 64 {
		t.Fatal("missing own account version")
	}
	web := call(t, h, "GET", "/api/account/avatar", nil, viewer)
	requireStatus(t, web, 200)
	image, err := png.Decode(bytes.NewReader(web.Body.Bytes()))
	if err != nil || image.Bounds().Dx() != 256 {
		t.Fatal("not a normalized picture", err)
	}
	if web.Header().Get("Content-Type") != "image/png" || web.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("unsafe image headers")
	}
	// Even a target ID cannot read another account's picture.
	requireStatus(t, call(t, h, "GET", "/api/account/avatar?user_id="+strconv.FormatInt(created.ID, 10), nil, admin), 204)
	pair, err := f.api.NativeAuth.Login(context.Background(), "viewer", testPassword, "127.0.0.1", "avatar-device", "Avatar test")
	if err != nil {
		t.Fatal(err)
	}
	native := nativeCall(t, h, "GET", "/api/client/v1/account/avatar", pair.AccessToken, nil, nil)
	me := nativeCall(t, h, "GET", "/api/client/v1/auth/me", pair.AccessToken, nil, nil)
	requireStatus(t, me, 200)
	var account struct {
		User accounts.User `json:"user"`
	}
	if err := json.Unmarshal(me.Body.Bytes(), &account); err != nil {
		t.Fatal(err)
	}
	if account.User.AvatarVersion != updated.AvatarVersion || pair.User.AvatarVersion != updated.AvatarVersion {
		t.Fatal("native account metadata omitted picture version")
	}
	requireStatus(t, native, 200)
	if !bytes.Equal(web.Body.Bytes(), native.Body.Bytes()) {
		t.Fatal("native picture differs")
	}
	requireStatus(t, nativeCall(t, h, "GET", "/api/client/v1/account/avatar", "", nil, viewer), 401)
}

func TestFailedAvatarReplacementPreservesPictureAndRevocation(t *testing.T) {
	f := setup(t)
	h := f.api.Handler()
	cookie := login(t, h, "admin")
	input := avatarPicture(t, color.RGBA{R: 255, A: 255})
	requireStatus(t, avatarUpload(t, h, input, cookie), 200)
	before := call(t, h, "GET", "/api/account/avatar", nil, cookie).Body.Bytes()
	requireStatus(t, avatarUpload(t, h, []byte("not an image"), cookie), 400)
	requireStatus(t, avatarUpload(t, h, make([]byte, profileimage.MaxUpload+1), cookie), 413)
	after := call(t, h, "GET", "/api/account/avatar", nil, cookie).Body.Bytes()
	if !bytes.Equal(before, after) {
		t.Fatal("failed replacement discarded picture")
	}
	requireStatus(t, call(t, h, "POST", "/api/auth/logout", nil, cookie), 200)
	if _, err := f.service.SetAvatar(context.Background(), identity(f.admin), input); err != accounts.ErrUnauthorized {
		t.Fatalf("revoked upload: %v", err)
	}
	fresh := login(t, h, "admin")
	if !bytes.Equal(before, call(t, h, "GET", "/api/account/avatar", nil, fresh).Body.Bytes()) {
		t.Fatal("revocation replaced picture")
	}
}
