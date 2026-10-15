package addons

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"
)

func TestMediaDialStalledHandshakeUsesFreshConnectionAndCancelsLoser(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var calls atomic.Int32
	stopped := make(chan struct{})
	winner, peer := net.Pipe()
	defer peer.Close()
	dial := func(ctx context.Context, _, _ string) (net.Conn, error) {
		if calls.Add(1) == 1 {
			<-ctx.Done()
			close(stopped)
			return nil, ctx.Err()
		}
		return winner, nil
	}
	conn, e := dialMediaAddresses(ctx, "tcp", []string{"public-ip:443"}, dial, time.Millisecond)
	if e != nil || conn != winner {
		t.Fatal("stalled primary blocked fresh connection", e)
	}
	defer conn.Close()
	select {
	case <-stopped:
	case <-ctx.Done():
		t.Fatal("loser was not cancelled")
	}
	if calls.Load() != 2 {
		t.Fatal("unexpected handshake count", calls.Load())
	}
}

func TestMediaDialClosesLateSuccessfulLoser(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var calls atomic.Int32
	a, ap := net.Pipe()
	defer ap.Close()
	b, bp := net.Pipe()
	defer bp.Close()
	dial := func(ctx context.Context, _, _ string) (net.Conn, error) {
		if calls.Add(1) == 1 {
			<-ctx.Done()
			return a, nil
		}
		return b, nil
	}
	conn, e := dialMediaAddresses(ctx, "tcp", []string{"public-ip:443"}, dial, time.Millisecond)
	if e != nil || conn != b {
		t.Fatal("backup failed", e)
	}
	defer conn.Close()
	_ = ap.SetReadDeadline(time.Now().Add(time.Second))
	if _, e = ap.Read(make([]byte, 1)); e == nil {
		t.Fatal("losing connection stayed open")
	} else if ne, ok := e.(net.Error); ok && ne.Timeout() {
		t.Fatal("losing connection leaked")
	}
}

func TestMediaDialStopsOnFailureAndCancellation(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "failed", true: "cancelled"}[cancelled], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var calls atomic.Int32
			dial := func(ctx context.Context, _, _ string) (net.Conn, error) {
				calls.Add(1)
				if cancelled {
					cancel()
					return nil, ctx.Err()
				}
				return nil, errors.New("connect failed")
			}
			conn, e := dialMediaAddresses(ctx, "tcp", []string{"public-ip:443"}, dial, time.Hour)
			if conn != nil || e == nil {
				t.Fatal("failure accepted")
			}
			if !cancelled && calls.Load() != 2 {
				t.Fatal("failed handshake did not attempt backup")
			}
			if cancelled && !errors.Is(e, context.Canceled) {
				t.Fatal("cancellation lost", e)
			}
		})
	}
}

func TestMediaDialTriesLaterAddressesWithinOriginalBudget(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()
	winner, peer := net.Pipe()
	defer peer.Close()
	var active, maxActive atomic.Int32
	var later atomic.Bool
	dial := func(attempt context.Context, _, address string) (net.Conn, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := maxActive.Load(); n > old && !maxActive.CompareAndSwap(old, n); old = maxActive.Load() {
		}
		if address == "one" || address == "two" {
			<-attempt.Done()
			return nil, attempt.Err()
		}
		later.Store(true)
		if address == "three" {
			return winner, nil
		}
		<-attempt.Done()
		return nil, attempt.Err()
	}
	conn, e := dialMediaAddresses(ctx, "tcp", []string{"one", "two", "three", "four", "five", "six"}, dial, time.Millisecond)
	if e != nil || conn != winner || !later.Load() || ctx.Err() != nil {
		t.Fatal("later address starved", e)
	}
	conn.Close()
	if maxActive.Load() > 2 {
		t.Fatal("connection concurrency exceeded")
	}
}

func TestMediaDialTriesOtherFamilyBeforeStalledFirstFamily(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		ips := []netip.Addr{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("1.0.0.1"), netip.MustParseAddr("2606:4700:4700::1111"), netip.MustParseAddr("2606:4700:4700::1001")}
		if reverse {
			ips = append(ips[2:], ips[:2]...)
		}
		ordered := interleaveIPFamilies(ips)
		firstStarted := make(chan struct{})
		ctx, cancel := context.WithCancel(context.Background())
		winner, peer := net.Pipe()
		conn, err := dialMediaAddresses(ctx, "tcp", []string{ordered[0].String(), ordered[1].String(), ordered[2].String(), ordered[3].String()}, func(c context.Context, _, address string) (net.Conn, error) {
			if address == ips[0].String() {
				close(firstStarted)
				<-c.Done()
				return nil, c.Err()
			}
			<-firstStarted
			if netip.MustParseAddr(address).Is4() == ips[0].Is4() {
				cancel()
				return nil, errors.New("same family attempted before alternate")
			}
			return winner, nil
		}, time.Millisecond)
		cancel()
		peer.Close()
		if err != nil || conn != winner {
			t.Fatal("other family starved", err)
		}
		conn.Close()
	}
}
