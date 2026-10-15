package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const identifier = "tv.plex.provider.ted.cinemeta"

type obj = map[string]any

type provider struct {
	client                                  *http.Client
	upstream, public, internal, media, data string
	mu                                      sync.Mutex
	states                                  map[string]obj
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	p := &provider{client: &http.Client{Timeout: 20 * time.Second}, upstream: env("CINEMETA_URL", "https://v3-cinemeta.strem.io"),
		public: env("PROVIDER_PUBLIC_URL", "http://127.0.0.1:42390"), internal: env("PROVIDER_INTERNAL_URL", "http://provider:42390"),
		media: env("PROVIDER_MEDIA_FILE", "runtime/media/demo.mp4"), data: env("PROVIDER_DATA_DIR", "runtime/provider"), states: map[string]obj{}}
	if err := os.MkdirAll(p.data, 0700); err != nil {
		log.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(p.data, "watch.json")); err == nil {
		if err := json.Unmarshal(b, &p.states); err != nil {
			log.Fatalf("invalid prototype watch state: %v", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		log.Fatal(err)
	}
	srv := &http.Server{Addr: env("PROVIDER_ADDR", ":42390"), Handler: http.HandlerFunc(p.serve), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		deadline, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(deadline)
	}()
	log.Printf("prototype provider listening on %s", srv.Addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func (p *provider) manifest() obj {
	return obj{"identifier": identifier, "title": "TED Cinemeta Prototype", "version": "1.0", "types": "video", "protocols": "stream", "online": true,
		"Types": []obj{{"type": 1}, {"type": 2}, {"type": 3}, {"type": 4}},
		"Feature": []obj{
			{"type": "content", "key": "/library/sections", "Directory": p.sections()},
			{"type": "search", "key": "/hubs/search"},
			{"type": "metadata", "key": "/library/metadata"},
			{"type": "timeline", "key": "/:/timeline", "scrobbleKey": "/:/scrobble", "unscrobbleKey": "/:/unscrobble"},
			{"type": "continuewatching", "key": "/hubs/continueWatching"},
			{"type": "promoted", "key": "/hubs"},
			{"type": "playqueue", "key": "/playQueues", "flavor": "full"},
		}}
}

func (p *provider) sections() []obj {
	var out []obj
	for _, kind := range []string{"movies", "shows"} {
		typ, title, n := "movie", "Cinemeta Movies", "1"
		if kind == "shows" {
			typ, title, n = "show", "Cinemeta Shows", "2"
		}
		key := "/library/sections/" + kind
		out = append(out, obj{"id": kind, "key": key, "hubKey": "/hubs/sections/" + kind, "title": title, "type": typ, "uuid": "ted-cinemeta-" + kind,
			"Pivot": []obj{{"id": "recommended", "key": "/hubs/sections/" + kind, "type": "hub", "title": "Recommended", "context": "content.discover", "symbol": "star"},
				{"id": "library", "key": key + "/all?type=" + n, "type": "list", "title": "Library", "context": "content.library", "symbol": "library"}}})
	}
	return out
}

type statusWriter struct {
	http.ResponseWriter
	code int
}

func (w *statusWriter) WriteHeader(code int) { w.code = code; w.ResponseWriter.WriteHeader(code) }
func (w *statusWriter) Write(b []byte) (int, error) {
	if w.code == 0 {
		w.WriteHeader(200)
	}
	return w.ResponseWriter.Write(b)
}

func (p *provider) serve(base http.ResponseWriter, r *http.Request) {
	w := &statusWriter{ResponseWriter: base}
	started := time.Now()
	defer func() {
		code := w.code
		if code == 0 {
			code = 200
		}
		b, _ := json.Marshal(obj{"time": time.Now().UTC(), "method": r.Method, "path": r.URL.Path, "status": code, "accept": r.Header.Get("Accept"), "product": r.Header.Get("X-Plex-Product"), "ms": time.Since(started).Milliseconds()})
		log.Print(string(b))
	}()
	w.Header().Set("Access-Control-Allow-Origin", p.public)
	w.Header().Set("Access-Control-Allow-Headers", "Range, Content-Type, X-Plex-Client-Identifier")
	w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, POST, PUT, OPTIONS")
	if r.Method == "OPTIONS" {
		w.WriteHeader(204)
		return
	}
	// PMS appends proxy paths to the registered base URL, rather than replacing it.
	if strings.HasPrefix(r.URL.Path, "/provider/") {
		copy := r.Clone(r.Context())
		copy.URL.Path = strings.TrimPrefix(r.URL.Path, "/provider")
		r = copy
	}
	path := r.URL.Path
	if path == "/health" {
		p.reply(w, r, obj{"ok": true})
		return
	}
	if path == "/" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, landing)
		return
	}
	if path == "/stream/demo.mp4" {
		http.ServeFile(w, r, p.media)
		return
	}
	if path == "/poster.svg" {
		w.Header().Set("Content-Type", "image/svg+xml")
		io.WriteString(w, `<svg xmlns="http://www.w3.org/2000/svg" width="300" height="450"><rect width="300" height="450" fill="#173451"/><text x="25" y="200" fill="white" font-size="27">TED Playback Test</text><text x="25" y="240" fill="white" font-size="20">Generated fixture</text></svg>`)
		return
	}
	if path == "/provider" || path == "/provider.json" {
		p.reply(w, r, obj{"MediaProvider": p.manifest()})
		return
	}
	if path == "/provider-container" {
		p.reply(w, r, obj{"MediaContainer": obj{"size": 1, "MediaProvider": []obj{p.manifest()}}})
		return
	}
	if path == "/library/sections" {
		p.reply(w, r, obj{"MediaContainer": obj{"size": 2, "Directory": p.sections()}})
		return
	}
	if path == "/prototype/state" {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.reply(w, r, obj{"states": p.states})
		return
	}
	if path == "/prototype/connection" {
		b, err := os.ReadFile(filepath.Join(p.data, "connection.json"))
		if err != nil {
			http.Error(w, "Run scripts/probe.mjs to discover the prototype connection", 503)
			return
		}
		var connection obj
		if err := json.Unmarshal(b, &connection); err != nil {
			http.Error(w, "Invalid prototype connection", 500)
			return
		}
		p.reply(w, r, connection)
		return
	}
	if strings.HasPrefix(path, "/:") {
		p.watch(w, r)
		return
	}
	if path == "/playQueues" || path == "/playQueues/1" {
		p.queue(w, r)
		return
	}
	if path == "/hubs/continueWatching" {
		p.mu.Lock()
		ids := []string{}
		for id, state := range p.states {
			if !truth(state["watched"]) && number(state["viewOffset"]) > 0 && strings.HasPrefix(id, "demo-") {
				ids = append(ids, id)
			}
		}
		p.mu.Unlock()
		items := []obj{}
		for _, id := range ids {
			items = append(items, p.fixture(id))
		}
		p.reply(w, r, obj{"MediaContainer": obj{"size": 1, "Hub": []obj{hub("Continue Watching", "movie", items)}}})
		return
	}
	if path == "/hubs/search" {
		query := r.URL.Query().Get("query")
		if query == "" {
			query = r.URL.Query().Get("title")
		}
		if len(query) > 200 {
			http.Error(w, "query too long", 400)
			return
		}
		hubs := []obj{}
		for _, kind := range []string{"movie", "series"} {
			items, err := p.catalog(r.Context(), kind, query)
			if err != nil {
				p.fail(w, err)
				return
			}
			typ := "movie"
			if kind == "series" {
				typ = "show"
			}
			hubs = append(hubs, hub("Cinemeta "+typ, typ, items))
		}
		p.reply(w, r, obj{"MediaContainer": obj{"size": len(hubs), "Hub": hubs}})
		return
	}
	if strings.HasPrefix(path, "/library/sections/") || path == "/hubs" || strings.HasPrefix(path, "/hubs/sections/") {
		kind := "movie"
		if strings.Contains(path, "shows") {
			kind = "series"
		}
		items, err := p.catalog(r.Context(), kind, "")
		if err != nil {
			p.fail(w, err)
			return
		}
		if strings.HasPrefix(path, "/hubs") {
			typ := "movie"
			if kind == "series" {
				typ = "show"
			}
			p.reply(w, r, obj{"MediaContainer": obj{"size": 1, "Hub": []obj{hub("Cinemeta Top 50", typ, items)}}})
			return
		}
		response := container(page(items, r), len(items), r)
		c := response["MediaContainer"].(map[string]any)
		typ := "movie"
		if kind == "series" {
			typ = "show"
		}
		c["Type"] = []obj{{"type": typ, "key": "/library/sections/" + map[string]string{"movie": "movies", "series": "shows"}[kind] + "/all", "title": "Titles"}}
		p.reply(w, r, response)
		return
	}
	if strings.HasPrefix(path, "/library/metadata/") {
		id := strings.TrimPrefix(path, "/library/metadata/")
		items, err := p.metadata(r.Context(), id)
		if err != nil {
			p.fail(w, err)
			return
		}
		p.reply(w, r, container(page(items, r), len(items), r))
		return
	}
	http.NotFound(w, r)
}

func (p *provider) fail(w http.ResponseWriter, err error) {
	log.Printf("upstream operation failed: %v", err)
	http.Error(w, "Cinemeta or provider operation failed; see provider log", 502)
}
func number(v any) int64 {
	switch x := v.(type) {
	case float64:
		return int64(x)
	case int:
		return int64(x)
	case int64:
		return x
	case string:
		n, _ := strconv.ParseInt(x, 10, 64)
		return n
	}
	return 0
}
func truth(v any) bool { b, _ := v.(bool); return b }
func page(items []obj, r *http.Request) []obj {
	start := int(number(r.URL.Query().Get("X-Plex-Container-Start")))
	size := int(number(r.URL.Query().Get("X-Plex-Container-Size")))
	if size == 0 {
		size = int(number(r.Header.Get("X-Plex-Container-Size")))
	}
	if size <= 0 || size > 50 {
		size = 50
	}
	if start < 0 {
		start = 0
	}
	if start > len(items) {
		start = len(items)
	}
	end := start + size
	if end > len(items) {
		end = len(items)
	}
	return items[start:end]
}
func container(items []obj, total int, r *http.Request) obj {
	return obj{"MediaContainer": obj{"identifier": identifier, "size": len(items), "totalSize": total, "offset": number(r.URL.Query().Get("X-Plex-Container-Start")), "Metadata": items}}
}
func hub(title, typ string, items []obj) obj {
	return obj{"title": title, "type": typ, "size": len(items), "hubIdentifier": "ted.cinemeta." + typ, "key": "/library/sections/movies/all", "Metadata": items}
}

func (p *provider) upstreamJSON(ctx context.Context, path string, target any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", p.upstream+path, nil)
	if err != nil {
		return err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("Cinemeta returned %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 12<<20+1))
	if err != nil {
		return err
	}
	if len(b) > 12<<20 {
		return errors.New("Cinemeta response too large")
	}
	return json.Unmarshal(b, target)
}

type cineMeta struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Poster      string      `json:"poster"`
	Background  string      `json:"background"`
	Description string      `json:"description"`
	Year        string      `json:"releaseInfo"`
	Videos      []cineVideo `json:"videos"`
}
type cineVideo struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Season  int    `json:"season"`
	Episode int    `json:"episode"`
}

func (p *provider) catalog(ctx context.Context, kind, query string) ([]obj, error) {
	path := "/catalog/" + kind + "/top.json"
	if query != "" {
		path = "/catalog/" + kind + "/top/search=" + url.PathEscape(query) + ".json"
	}
	var response struct {
		Metas []cineMeta `json:"metas"`
	}
	if err := p.upstreamJSON(ctx, path, &response); err != nil {
		return nil, err
	}
	id := "demo-movie"
	if kind == "series" {
		id = "demo-show"
	}
	items := []obj{}
	if query == "" || strings.Contains(strings.ToLower("TED Playback Test"), strings.ToLower(query)) {
		items = append(items, p.fixture(id))
	}
	for _, m := range response.Metas {
		if len(items) == 50 {
			break
		}
		if !validIMDb(m.ID) {
			continue
		}
		items = append(items, p.cineItem(kind, m))
	}
	return items, nil
}
func validIMDb(id string) bool {
	if !strings.HasPrefix(id, "tt") || len(id) < 3 || len(id) > 16 {
		return false
	}
	for _, c := range id[2:] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
func (p *provider) cineItem(kind string, m cineMeta) obj {
	typ, prefix := "movie", "movie-"
	if kind == "series" {
		typ, prefix = "show", "show-"
	}
	id := prefix + m.ID
	year, _ := strconv.Atoi(strings.Split(m.Year, "–")[0])
	return obj{"ratingKey": id, "key": "/library/metadata/" + id, "guid": identifier + "://" + id, "type": typ, "title": m.Name, "year": year, "summary": m.Description + "\n\nCatalog-only prototype entry: no media source is mapped.", "thumb": m.Poster, "art": m.Background, "Guid": []obj{{"id": "imdb://" + m.ID}}}
}

func (p *provider) metadata(ctx context.Context, id string) ([]obj, error) {
	if strings.HasSuffix(id, "/related") {
		return []obj{}, nil
	}
	if strings.HasPrefix(id, "demo-") {
		switch id {
		case "demo-show/children":
			return []obj{p.fixture("demo-season")}, nil
		case "demo-show/grandchildren", "demo-season/children", "demo-season/grandchildren":
			return []obj{p.fixture("demo-episode")}, nil
		case "demo-movie", "demo-show", "demo-season", "demo-episode":
			return []obj{p.fixture(id)}, nil
		}
		return nil, errors.New("unknown demo identity")
	}
	parts := strings.Split(id, "/")
	key := parts[0]
	bits := strings.Split(key, "-")
	if len(bits) < 2 || !validIMDb(bits[1]) {
		return nil, errors.New("invalid catalog identity")
	}
	kind := "movie"
	if bits[0] != "movie" {
		kind = "series"
	}
	var response struct {
		Meta cineMeta `json:"meta"`
	}
	if err := p.upstreamJSON(ctx, "/meta/"+kind+"/"+bits[1]+".json", &response); err != nil {
		return nil, err
	}
	m := response.Meta
	if m.ID == "" {
		return nil, errors.New("metadata absent")
	}
	if bits[0] == "episode" {
		if len(parts) != 1 || len(bits) != 4 {
			return nil, errors.New("invalid episode identity")
		}
		season, seasonErr := strconv.Atoi(bits[2])
		episode, episodeErr := strconv.Atoi(bits[3])
		if seasonErr != nil || episodeErr != nil || season < 0 || episode < 0 {
			return nil, errors.New("invalid episode numbering")
		}
		for _, v := range m.Videos {
			if v.Season == season && v.Episode == episode {
				return []obj{p.cineEpisode(m, v)}, nil
			}
		}
		return nil, errors.New("episode absent from catalog")
	}
	if len(parts) == 1 && bits[0] != "season" {
		return []obj{p.cineItem(kind, m)}, nil
	}
	items := []obj{}
	if strings.HasSuffix(id, "/grandchildren") || bits[0] == "season" {
		season := -1
		if len(bits) == 3 {
			season, _ = strconv.Atoi(bits[2])
		}
		for _, v := range m.Videos {
			if season >= 0 && v.Season != season {
				continue
			}
			items = append(items, p.cineEpisode(m, v))
		}
	} else {
		seen := map[int]bool{}
		for _, v := range m.Videos {
			if seen[v.Season] {
				continue
			}
			seen[v.Season] = true
			s := fmt.Sprintf("season-%s-%d", m.ID, v.Season)
			items = append(items, obj{"ratingKey": s, "key": "/library/metadata/" + s, "type": "season", "title": fmt.Sprintf("Season %d", v.Season), "index": v.Season, "parentTitle": m.Name})
		}
		sort.Slice(items, func(i, j int) bool { return number(items[i]["index"]) < number(items[j]["index"]) })
	}
	return items, nil
}

func (p *provider) cineEpisode(m cineMeta, v cineVideo) obj {
	id := fmt.Sprintf("episode-%s-%d-%d", m.ID, v.Season, v.Episode)
	return obj{"ratingKey": id, "key": "/library/metadata/" + id, "guid": identifier + "://" + id, "type": "episode", "title": v.Title, "index": v.Episode, "parentIndex": v.Season, "grandparentTitle": m.Name, "grandparentKey": "/library/metadata/show-" + m.ID, "parentKey": fmt.Sprintf("/library/metadata/season-%s-%d", m.ID, v.Season), "summary": "Catalog-only: no source mapped."}
}

func (p *provider) fixture(id string) obj {
	typ, title := "movie", "TED Playback Test (generated video)"
	switch id {
	case "demo-show":
		typ, title = "show", "TED Playback Test Series"
	case "demo-season":
		typ, title = "season", "Season 1"
	case "demo-episode":
		typ, title = "episode", "Generated test episode"
	}
	m := obj{"ratingKey": id, "key": "/library/metadata/" + id, "guid": identifier + "://" + id, "type": typ, "title": title, "year": 2026, "summary": "Original 60-second generated test pattern with a tone. Not a movie or episode from Cinemeta.", "thumb": p.public + "/poster.svg", "duration": 60000}
	if typ == "show" {
		m["childCount"] = 1
		m["leafCount"] = 1
	}
	if typ == "season" || typ == "episode" {
		m["index"] = 1
		m["parentIndex"] = 1
		m["grandparentTitle"] = "TED Playback Test Series"
		m["parentKey"] = "/library/metadata/demo-season"
		m["grandparentKey"] = "/library/metadata/demo-show"
	}
	if typ == "movie" || typ == "episode" {
		size := int64(0)
		if f, err := os.Stat(p.media); err == nil {
			size = f.Size()
		}
		m["Media"] = []obj{{"id": 1, "duration": 60000, "container": "mp4", "videoCodec": "h264", "audioCodec": "aac", "videoResolution": "sd", "width": 640, "height": 360, "audioChannels": 1, "videoFrameRate": "24p", "protocol": "http", "Part": []obj{{"id": 1, "key": "/stream/demo.mp4", "file": p.internal + "/stream/demo.mp4", "size": size, "duration": 60000, "container": "mp4", "Stream": []obj{{"id": 1, "streamType": 1, "codec": "h264", "width": 640, "height": 360, "frameRate": 24, "selected": true}, {"id": 2, "streamType": 2, "codec": "aac", "channels": 1, "language": "English", "languageCode": "eng", "selected": true}}}}}}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if state := p.states[id]; state != nil {
		m["viewOffset"] = state["viewOffset"]
		if truth(state["watched"]) {
			m["viewCount"] = 1
		} else {
			m["viewCount"] = 0
		}
	}
	return m
}

func (p *provider) watch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	id := q.Get("ratingKey")
	if id == "" {
		id = q.Get("key")
		id = strings.TrimPrefix(id, "/library/metadata/")
	}
	if id != "demo-movie" && id != "demo-episode" {
		http.Error(w, "prototype watch writes only support test fixtures", 400)
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	state := obj{}
	for k, v := range p.states[id] {
		state[k] = v
	}
	state["updatedAt"] = time.Now().UTC().Format(time.RFC3339Nano)
	switch r.URL.Path {
	case "/:/scrobble":
		state["watched"] = true
		state["viewOffset"] = 0
	case "/:/unscrobble":
		state["watched"] = false
		state["viewOffset"] = 0
	case "/:/timeline":
		offset := number(q.Get("time"))
		if offset < 0 || offset > 60000 {
			http.Error(w, "invalid test playback offset", 400)
			return
		}
		state["viewOffset"] = offset
		state["watched"] = offset >= 54000
		state["playbackState"] = q.Get("state")
	default:
		http.NotFound(w, r)
		return
	}
	next := map[string]obj{}
	for k, v := range p.states {
		next[k] = v
	}
	next[id] = state
	b, err := json.MarshalIndent(next, "", "  ")
	if err == nil {
		err = os.WriteFile(filepath.Join(p.data, "watch.json.tmp"), b, 0600)
	}
	if err == nil {
		err = os.Rename(filepath.Join(p.data, "watch.json.tmp"), filepath.Join(p.data, "watch.json"))
	}
	if err != nil {
		p.fail(w, err)
		return
	}
	p.states = next
	w.WriteHeader(200)
}

func (p *provider) queue(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	id := strings.TrimPrefix(q.Get("key"), "/library/metadata/")
	if id == "" {
		id = "demo-movie"
	}
	if id != "demo-movie" && id != "demo-episode" {
		http.Error(w, "only generated fixtures have sources", 400)
		return
	}
	item := p.fixture(id)
	item["playQueueItemID"] = 1
	p.reply(w, r, obj{"MediaContainer": obj{"size": 1, "playQueueID": 1, "playQueueVersion": 1, "playQueueTotalCount": 1, "playQueueSelectedItemID": 1, "playQueueSelectedItemOffset": 0, "Metadata": []obj{item}}})
}

func (p *provider) reply(w http.ResponseWriter, r *http.Request, v obj) {
	if strings.Contains(r.Header.Get("Accept"), "json") || strings.HasSuffix(r.URL.Path, ".json") {
		b, err := json.Marshal(v)
		if err != nil {
			http.Error(w, "encoding failed", 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		// PMS 1.43.4's provider proxy forwards chunked headers with decoded bodies.
		// Give it an explicit length so both JSON and XML remain valid over HTTP.
		w.Header().Set("Content-Length", strconv.Itoa(len(b)))
		_, _ = w.Write(b)
		return
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	var b bytes.Buffer
	b.WriteString(xml.Header)
	enc := xml.NewEncoder(&b)
	for name, value := range v {
		writeXML(enc, name, value)
	}
	_ = enc.Flush()
	w.Header().Set("Content-Length", strconv.Itoa(b.Len()))
	_, _ = w.Write(b.Bytes())
}
func writeXML(enc *xml.Encoder, name string, value any) {
	if items, ok := value.([]obj); ok {
		for _, item := range items {
			writeXML(enc, name, item)
		}
		return
	}
	m, ok := value.(map[string]any)
	if !ok {
		return
	}
	// Plex's JSON Metadata array maps to typed XML elements, not <Metadata>.
	if name == "Metadata" {
		switch m["type"] {
		case "movie", "episode", "clip":
			name = "Video"
		case "show", "season":
			name = "Directory"
		}
	}
	start := xml.StartElement{Name: xml.Name{Local: name}}
	keys := []string{}
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		switch m[k].(type) {
		case map[string]any, []obj:
		default:
			if m[k] != nil {
				start.Attr = append(start.Attr, xml.Attr{Name: xml.Name{Local: k}, Value: fmt.Sprint(m[k])})
			}
		}
	}
	_ = enc.EncodeToken(start)
	for _, k := range keys {
		writeXML(enc, k, m[k])
	}
	_ = enc.EncodeToken(start.End())
}

//go:embed index.html
var landing string
