package realdebrid

import (
	"net/http"
	"strconv"
	"time"
)

type limitedRetry struct{ delay time.Duration }

func (e limitedRetry) Error() string             { return ErrLimited.Error() }
func (e limitedRetry) Unwrap() error             { return ErrLimited }
func (e limitedRetry) RetryAfter() time.Duration { return e.delay }
func rateLimit(header http.Header) error {
	delay := time.Duration(0)
	if seconds, err := strconv.ParseInt(header.Get("Retry-After"), 10, 64); err == nil && seconds > 0 && seconds <= 31536000 {
		delay = time.Duration(seconds) * time.Second
	} else if at, err := http.ParseTime(header.Get("Retry-After")); err == nil {
		delay = time.Until(at)
	}
	if delay > 0 {
		return limitedRetry{delay}
	}
	return ErrLimited
}
