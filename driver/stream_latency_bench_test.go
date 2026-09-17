//go:build cgo && typedb && integration

package driver

import (
	"fmt"
	"net"
	"sync"
	"testing"
	"time"
)

// BenchmarkLiveStreamLatency routes reads through a benchmark-local TCP proxy
// with a fixed delay on each socket read. This is a controlled IO-delay
// comparison, not a claim of exact RTT or a substitute for WAN testing.
func BenchmarkLiveStreamLatency(b *testing.B) {
	for _, shape := range []streamBenchShape{
		{"narrow-64", 64, 16, `match $p isa person, has name $n; fetch { "name": $n };`},
		{"large-512", 512, 512, `match $p isa person, has name $n; fetch { "name": $n };`},
	} {
		for _, delay := range []time.Duration{0, 2 * time.Millisecond} {
			b.Run(fmt.Sprintf("%s/delay=%dms", shape.name, delay.Milliseconds()), func(b *testing.B) {
				_, dbName := setupStreamBenchFixture(b, shape)
				proxy := startStreamDelayProxy(b, testAddr(), delay)
				address := proxy.listener.Addr().String()
				privateAddress := testAddr()
				translation, err := resolveAddressTranslation(privateAddress, DriverOptions{})
				if err != nil {
					b.Fatal(err)
				}
				if translation != nil {
					privateAddress = translation[privateAddress]
				}
				conn, err := OpenWithAddressTranslation(map[string]string{address: privateAddress}, "admin", "password", DriverOptions{})
				if err != nil {
					b.Fatal(err)
				}
				b.Cleanup(conn.Close)
				for _, chunk := range []int{32, 256} {
					for _, prefetch := range []struct {
						name string
						size int64
					}{{"default", -1}, {"prefetch-1", 1}, {"prefetch-256", 256}} {
						b.Run(fmt.Sprintf("chunk=%d/%s", chunk, prefetch.name), func(b *testing.B) {
							benchmarkStreamCase(b, conn, dbName, shape, chunk, prefetch.size, false)
						})
					}
				}
			})
		}
	}
}

type streamDelayProxy struct {
	listener net.Listener
	mu       sync.Mutex
	conns    map[net.Conn]struct{}
	closed   bool
	workers  sync.WaitGroup
}

func startStreamDelayProxy(b *testing.B, target string, delay time.Duration) *streamDelayProxy {
	b.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
	}
	p := &streamDelayProxy{listener: listener, conns: make(map[net.Conn]struct{})}
	p.workers.Add(1)
	go func() {
		defer p.workers.Done()
		for {
			client, err := listener.Accept()
			if err != nil {
				return
			}
			p.workers.Add(1)
			go func() {
				defer p.workers.Done()
				server, dialErr := net.DialTimeout("tcp", target, 5*time.Second)
				if dialErr != nil {
					client.Close()
					return
				}
				p.mu.Lock()
				if p.closed {
					p.mu.Unlock()
					client.Close()
					server.Close()
					return
				}
				p.conns[client] = struct{}{}
				p.conns[server] = struct{}{}
				p.mu.Unlock()
				var copies sync.WaitGroup
				copies.Add(2)
				go func() { defer copies.Done(); copyStreamDelayed(server, client, delay); server.Close(); client.Close() }()
				go func() { defer copies.Done(); copyStreamDelayed(client, server, delay); client.Close(); server.Close() }()
				copies.Wait()
				p.mu.Lock()
				delete(p.conns, client)
				delete(p.conns, server)
				p.mu.Unlock()
			}()
		}
	}()
	b.Cleanup(func() {
		listener.Close()
		p.mu.Lock()
		p.closed = true
		for conn := range p.conns {
			conn.Close()
		}
		p.mu.Unlock()
		p.workers.Wait()
	})
	return p
}

func copyStreamDelayed(dst, src net.Conn, delay time.Duration) {
	buf := make([]byte, 64*1024)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if delay > 0 {
				time.Sleep(delay)
			}
			for written := 0; written < n; {
				m, writeErr := dst.Write(buf[written:n])
				written += m
				if writeErr != nil || m == 0 {
					return
				}
			}
		}
		if err != nil {
			return
		}
	}
}
