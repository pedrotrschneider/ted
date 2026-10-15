package realdebrid

import (
	"context"
	"net/url"
	"strconv"
)

// Read a complete prefix with a stable total, without mistaking partial reads
// for removal. Inventory ordering is the order returned by RD, never a title guess.
func (c *Client) inventoryPrefix(ctx context.Context, count int) ([]Torrent, int, error) {
	values := []Torrent{}
	total := -1
	seen := map[string]bool{}
	limit := min(1000, count)
	for offset, page := 0, 1; offset < count; page++ {
		var raw []rawTorrent
		// RD currently returns 204 for offset=0 even on nonempty accounts.
		// Keep a fixed page size for the scan; changing it mid-scan shifts pages.
		headers, e := c.get(ctx, "torrents?"+url.Values{"limit": {strconv.Itoa(limit)}, "page": {strconv.Itoa(page)}}.Encode(), &raw)
		if e != nil {
			return nil, 0, e
		}
		n, e := strconv.Atoi(headers.Get("X-Total-Count"))
		if e != nil || n < 0 || n > 1000000 || raw == nil || len(raw) != min(limit, max(0, n-offset)) || total >= 0 && n != total {
			return nil, 0, ErrIncomplete
		}
		total = n
		for _, r := range raw {
			t, e := r.public()
			if e != nil || seen[t.ID] || !hashPattern.MatchString(t.Hash) {
				return nil, 0, ErrIncomplete
			}
			seen[t.ID] = true
			values = append(values, t)
		}
		offset += len(raw)
		if offset >= total {
			break
		}
	}
	return values, total, nil
}

// An unchanged overlap is usable only when the reconstructed total also agrees.
// This detects a tail deletion even if the recent prefix itself is unchanged.
func mergeInventoryPrefix(old, head []Torrent, total int) ([]Torrent, bool) {
	if len(head) == total {
		return head, true
	}
	if len(head) == 0 {
		return nil, false
	}
	index := map[string]int{}
	for i, t := range old {
		index[t.ID] = i
	}
	for start := 0; start < len(head); start++ {
		i, exists := index[head[start].ID]
		if !exists {
			continue
		}
		overlap := len(head) - start
		if overlap < min(10, len(head)) || i+overlap > len(old) {
			continue
		}
		equal := true
		for j := 0; j < overlap; j++ {
			if !sameInventoryTorrent(head[start+j], old[i+j]) {
				equal = false
				break
			}
		}
		if !equal {
			continue
		}
		tail := old[i+overlap:]
		if len(head)+len(tail) != total {
			continue
		}
		merged := append(append([]Torrent{}, head...), tail...)
		seen := map[string]bool{}
		for _, t := range merged {
			if seen[t.ID] {
				equal = false
				break
			}
			seen[t.ID] = true
		}
		if equal {
			return merged, true
		}
	}
	return nil, false
}
func sameInventoryTorrent(a, b Torrent) bool {
	return a.ID == b.ID && a.Hash == b.Hash && a.Name == b.Name && a.Bytes == b.Bytes && a.Status == b.Status && a.Progress == b.Progress && a.Added.Equal(b.Added)
}

func (c *Client) scanInventory(ctx context.Context, old []Torrent, full bool) ([]Torrent, bool, error) {
	count := 10
	for {
		head, total, e := c.inventoryPrefix(ctx, count)
		if e != nil {
			return nil, false, e
		}
		complete := len(head) == total
		var merged []Torrent
		var matched bool
		if !full {
			merged, matched = mergeInventoryPrefix(old, head, total)
		}
		if complete {
			merged = head
			matched = true
		}
		if matched {
			// RD pagination is not a snapshot. Recheck the head/total before publishing.
			check, n, e := c.inventoryPrefix(ctx, min(10, max(1, total)))
			if e != nil || n != total || len(check) != min(10, total) {
				return nil, false, ErrIncomplete
			}
			for i := range check {
				if !sameInventoryTorrent(check[i], head[i]) {
					return nil, false, ErrIncomplete
				}
			}
			return merged, complete, nil
		}
		if full {
			count = max(1, total)
		} else if count == 10 {
			count = 100
		} else {
			count = min(total, count*2)
		}
	}
}
