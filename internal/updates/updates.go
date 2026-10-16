// Package updates reads the published container channel, not reserved Git tags.
package updates

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

var versionPattern = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-rc([1-9][0-9]*))?$`)

func parts(v string) ([]int, bool) {
	m := versionPattern.FindStringSubmatch(v)
	if m == nil {
		return nil, false
	}
	p := make([]int, 4)
	p[3] = int(^uint(0) >> 1)
	for i := 0; i < 4; i++ {
		if m[i+1] != "" {
			n, err := strconv.Atoi(m[i+1])
			if err != nil {
				return nil, false
			}
			p[i] = n
		}
	}
	return p, true
}
func Newer(candidate, current string) bool {
	a, ok := parts(candidate)
	if !ok {
		return false
	}
	b, ok := parts(current)
	if !ok {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}

type Checker struct {
	mu      sync.Mutex
	client  *http.Client
	until   time.Time
	version string
	err     error
	channel string
}

func NewChecker() *Checker { return &Checker{client: &http.Client{Timeout: 8 * time.Second}} }
func (c *Checker) Version(ctx context.Context, current string) (string, error) {
	channel := "latest"
	if strings.Contains(current, "-rc") {
		channel = "develop"
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.channel == channel && time.Now().Before(c.until) {
		return c.version, c.err
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	v, err := c.fetch(ctx, channel)
	c.channel = channel
	c.err = err
	c.version = v
	ttl := 6 * time.Hour
	if err != nil {
		ttl = 5 * time.Minute
	}
	c.until = time.Now().Add(ttl)
	return v, err
}
func (c *Checker) get(ctx context.Context, url, token string, target any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "TED-update-check")
	req.Header.Set("Accept", "application/vnd.oci.image.index.v1+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json, application/vnd.docker.distribution.manifest.list.v2+json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("update lookup failed")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return fmt.Errorf("update lookup status %d", response.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(target)
}

var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func (c *Checker) fetch(ctx context.Context, channel string) (string, error) {
	var auth struct {
		Token string `json:"token"`
	}
	if err := c.get(ctx, "https://ghcr.io/token?service=ghcr.io&scope=repository:pedrotrschneider/ted:pull", "", &auth); err != nil {
		return "", err
	}
	if auth.Token == "" {
		return "", fmt.Errorf("missing registry authorization")
	}
	base := "https://ghcr.io/v2/pedrotrschneider/ted/"
	type descriptor struct {
		Digest   string
		Platform struct{ OS, Architecture string }
	}
	var manifest struct {
		Manifests []descriptor
		Config    descriptor
	}
	if err := c.get(ctx, base+"manifests/"+channel, auth.Token, &manifest); err != nil {
		return "", err
	}
	if len(manifest.Manifests) > 0 {
		digest := ""
		for _, m := range manifest.Manifests {
			if m.Platform.OS == "linux" && m.Platform.Architecture == "amd64" {
				digest = m.Digest
				break
			}
		}
		if !digestPattern.MatchString(digest) {
			return "", fmt.Errorf("missing image manifest")
		}
		manifest.Manifests = nil
		if err := c.get(ctx, base+"manifests/"+digest, auth.Token, &manifest); err != nil {
			return "", err
		}
	}
	if !digestPattern.MatchString(manifest.Config.Digest) {
		return "", fmt.Errorf("missing image config")
	}
	var config struct {
		Config struct{ Labels map[string]string }
	}
	if err := c.get(ctx, base+"blobs/"+manifest.Config.Digest, auth.Token, &config); err != nil {
		return "", err
	}
	version := config.Config.Labels["org.opencontainers.image.version"]
	if _, ok := parts(version); !ok {
		return "", fmt.Errorf("invalid published version")
	}
	return version, nil
}
