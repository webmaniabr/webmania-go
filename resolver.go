package webmania

import (
	"context"
	"net"
	"sync"
)

// LookupHost deliberately uses the native Go synchronous host lookup path.
// LookupIPAddr/DialContext(hostname) use a shared singleflight lookup whose
// internal work can outlive the caller. LookupHost drains its A/AAAA workers.
// Each DNS socket is additionally bound to this exact request context, even
// when the resolver supplies a context with a later timeout.
func resolveOwned(ctx context.Context, host string) ([]string, error) {
	if ip := net.ParseIP(host); ip != nil {
		return []string{host}, nil
	}
	resolver := &net.Resolver{PreferGo: true, StrictErrors: true}
	resolver.Dial = func(_ context.Context, network, address string) (net.Conn, error) {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		// The system resolver gives numeric nameserver addresses. Reject a name
		// rather than recursively starting an unowned lookup.
		h, _, e := net.SplitHostPort(address)
		if e != nil || net.ParseIP(h) == nil {
			return nil, newError("transport")
		}
		conn, e := (&net.Dialer{}).DialContext(ctx, network, address)
		if e != nil {
			return nil, e
		}
		owned := &dnsOwnedConn{Conn: conn, done: make(chan struct{})}
		owned.stop = context.AfterFunc(ctx, func() { _ = conn.Close(); close(owned.done) })
		if packet, ok := conn.(net.PacketConn); ok {
			return &dnsOwnedPacketConn{dnsOwnedConn: owned, packet: packet}, nil
		}
		return owned, nil
	}
	return resolver.LookupHost(ctx, host)
}

type dnsOwnedConn struct {
	net.Conn
	stop func() bool
	done chan struct{}
	once sync.Once
}

func (c *dnsOwnedConn) Close() error {
	var err error
	c.once.Do(func() {
		if !c.stop() {
			<-c.done
		}
		err = c.Conn.Close()
	})
	return err
}

func dialOwned(ctx context.Context, host, port string) (net.Conn, error) {
	addrs, e := resolveOwned(ctx, host)
	if e != nil {
		return nil, e
	}
	// Address selection is TCP connection establishment only. No HTTP bytes have
	// been written and no application request is retried across addresses.
	var last error
	for _, ip := range addrs {
		if e = ctx.Err(); e != nil {
			return nil, e
		}
		c, e := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(ip, port))
		if e == nil {
			return c, nil
		}
		last = e
	}
	if last == nil {
		last = newError("transport")
	}
	return nil, last
}

// Preserve PacketConn on UDP wrappers: net.Resolver uses this interface to
// select DNS datagram framing rather than the two-byte TCP length prefix.
type dnsOwnedPacketConn struct {
	*dnsOwnedConn
	packet net.PacketConn
}

func (c *dnsOwnedPacketConn) ReadFrom(b []byte) (int, net.Addr, error) { return c.packet.ReadFrom(b) }
func (c *dnsOwnedPacketConn) WriteTo(b []byte, a net.Addr) (int, error) {
	return c.packet.WriteTo(b, a)
}
