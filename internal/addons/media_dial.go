package addons

import (
	"context"
	"net"
	"time"
)

// Addresses have already passed the complete public-address check. Two TCP
// handshakes at most are in flight; no request bytes reach a losing connection.
func dialMediaAddresses(ctx context.Context, network string, addresses []string, dial func(context.Context, string, string) (net.Conn, error), delay time.Duration) (net.Conn, error) {
	if len(addresses) == 0 {
		return nil, ErrUnavailable
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type result struct {
		conn net.Conn
		err  error
	}
	results := make(chan result)
	start := func(offset int) {
		go func() {
			var last error
			// With multiple addresses each lane tries a disjoint set. Divide
			// the remaining budget so a blackholed first address cannot
			// prevent trying the later addresses returned by DNS.
			step := 2
			if len(addresses) == 1 {
				step = 1
			}
			for i := offset; i < len(addresses); i += step {
				attempt := ctx
				stop := func() {}
				if deadline, ok := ctx.Deadline(); ok && len(addresses) > 2 {
					remaining := (len(addresses) - i + step - 1) / step
					attempt, stop = context.WithTimeout(ctx, time.Until(deadline)/time.Duration(remaining))
				}
				conn, err := dial(attempt, network, addresses[i])
				stop()
				if err == nil {
					select {
					case results <- result{conn: conn}:
					case <-ctx.Done():
						conn.Close()
					}
					return
				}
				last = err
				if ctx.Err() != nil {
					break
				}
			}
			select {
			case results <- result{err: last}:
			case <-ctx.Done():
			}
		}()
	}
	start(0)
	timer := time.NewTimer(delay)
	defer timer.Stop()
	backup, failed := false, 0
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
			if !backup {
				backup = true
				start(1 % len(addresses))
			}
		case v := <-results:
			if v.err == nil {
				return v.conn, nil
			}
			failed++
			if failed == 2 {
				return nil, v.err
			}
			if !backup {
				backup = true
				timer.Stop()
				start(1 % len(addresses))
			}
		}
	}
}
