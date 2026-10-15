package addons

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// A bounded continuation of the very same checked head response. Readers see
// immutable snapshots; an incomplete body ends as a short transfer, never EOF
// manufactured by appending a JSON error or bytes from an unverified release.
type startupStream struct {
	mu      sync.Mutex
	data    []byte
	done    bool
	changed chan struct{}
	cancel  context.CancelFunc
}

func (s *startupStream) append(data []byte, done bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data = append(s.data, data...)
	s.done = done
	close(s.changed)
	s.changed = make(chan struct{})
}
func (s *startupStream) read(ctx context.Context, start, end int64) []byte {
	for {
		s.mu.Lock()
		if start < int64(len(s.data)) {
			b := append([]byte(nil), s.data[start:min(end+1, int64(len(s.data)))]...)
			s.mu.Unlock()
			return b
		}
		done, changed := s.done, s.changed
		s.mu.Unlock()
		if done {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-changed:
		}
	}
}
func (s *Service) progressiveHead(ctx context.Context, source Source, end int64) (startupPart, *startupStream, error) {
	req, err := sourceRequest(ctx, source, fmt.Sprintf("bytes=0-%d", end))
	if err != nil {
		return startupPart{}, nil, err
	}
	res, err := s.mediaHTTP().Do(req)
	if err != nil {
		return startupPart{}, nil, ErrUnavailable
	}
	a, b, n, e := contentRange(res.Header.Get("Content-Range"))
	if res.StatusCode != http.StatusPartialContent || e != nil || a != 0 || b != end || n != source.VideoSize || !fileContentType(res.Header.Get("Content-Type")) || !identityEncoding(res.Header.Get("Content-Encoding")) {
		res.Body.Close()
		return startupPart{}, nil, ErrUnsupported
	}
	prefix := make([]byte, 4096)
	if _, err = io.ReadFull(res.Body, prefix); err != nil {
		res.Body.Close()
		return startupPart{}, nil, ErrUnsupported
	}
	stream := &startupStream{data: append(make([]byte, 0, end+1), prefix...), changed: make(chan struct{})}
	go func() {
		defer res.Body.Close()
		remaining := end + 1 - int64(len(prefix))
		buf := make([]byte, 64<<10)
		for remaining > 0 {
			n, err := res.Body.Read(buf[:min(int64(len(buf)), remaining)])
			if n > 0 {
				stream.append(buf[:n], false)
				remaining -= int64(n)
			}
			if err != nil {
				break
			}
		}
		stream.append(nil, true)
	}()
	return startupPart{prefix, res.Header.Get("ETag"), res.Header.Get("Content-Type")}, stream, nil
}

func detachedStartup(ctx context.Context) (context.Context, context.CancelFunc, func() bool) {
	// The parent owns preparation until the validated result is handed off.
	// After that only the bounded remainder is detached from the JSON request.
	background, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	stop := context.AfterFunc(ctx, cancel)
	return background, cancel, stop
}
