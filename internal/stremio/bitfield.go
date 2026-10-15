package stremio

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"io"
	"strconv"
	"strings"
)

const maxEpisodes = 10000

// DecodeWatched implements the documented anchor:length:base64(zlib(bits)) wire
// format. Unknown anchors are errors, never the upstream client's empty fallback.
func DecodeWatched(value string, ids []string) (map[string]bool, error) {
	if len(ids) > maxEpisodes || len(value) > 32768 {
		return nil, ErrIncomplete
	}
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id == "" {
			return nil, ErrAmbiguous
		}
		if _, ok := out[id]; ok {
			return nil, ErrAmbiguous
		}
		out[id] = false
	}
	if value == "" {
		return out, nil
	}
	end := strings.LastIndexByte(value, ':')
	if end < 0 {
		return nil, ErrIncomplete
	}
	head := value[:end]
	mid := strings.LastIndexByte(head, ':')
	if mid < 0 {
		return nil, ErrIncomplete
	}
	anchor := head[:mid]
	length, err := strconv.Atoi(head[mid+1:])
	if err != nil || length < 1 || length > maxEpisodes {
		return nil, ErrIncomplete
	}
	packed, err := base64.StdEncoding.Strict().DecodeString(value[end+1:])
	if err != nil {
		return nil, ErrIncomplete
	}
	zr, err := zlib.NewReader(bytes.NewReader(packed))
	if err != nil {
		return nil, ErrIncomplete
	}
	defer zr.Close()
	bits, err := io.ReadAll(io.LimitReader(zr, maxEpisodes/8+1))
	if err != nil || len(bits) > maxEpisodes/8 {
		return nil, ErrIncomplete
	}
	anchorIndex := -1
	for i, id := range ids {
		if id == anchor {
			anchorIndex = i
			break
		}
	}
	if anchorIndex < 0 {
		if len(ids) == 0 && anchor == "undefined" && len(bits) == 0 {
			return out, nil
		}
		return nil, ErrAmbiguous
	}
	offset := length - anchorIndex - 1
	// Reject positive bits that cannot be mapped to a known episode. Losing them
	// while writing a smaller/reordered catalog would erase legitimate history.
	for index := 0; index < len(bits)*8; index++ {
		if bits[index/8]&(1<<uint(index%8)) == 0 {
			continue
		}
		mapped := index - offset
		if mapped < 0 || mapped >= len(ids) {
			return nil, ErrAmbiguous
		}
		out[ids[mapped]] = true
	}
	return out, nil
}
func EncodeWatched(ids []string, watched map[string]bool) (string, error) {
	if len(ids) > maxEpisodes {
		return "", ErrIncomplete
	}
	seen := map[string]bool{}
	bits := make([]byte, (len(ids)+7)/8)
	last := 0
	for i, id := range ids {
		if id == "" || seen[id] {
			return "", ErrAmbiguous
		}
		seen[id] = true
		if watched[id] {
			bits[i/8] |= 1 << uint(i%8)
			last = i
		}
	}
	for id, value := range watched {
		if value && !seen[id] {
			return "", ErrAmbiguous
		}
	}
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	if _, err := zw.Write(bits); err != nil {
		return "", err
	}
	if err := zw.Close(); err != nil {
		return "", err
	}
	anchor := "undefined"
	if len(ids) > 0 {
		anchor = ids[last]
	}
	return anchor + ":" + strconv.Itoa(last+1) + ":" + base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}
