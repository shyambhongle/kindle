// Package sources fetches external data. Every source has a Dummy() so the backend works with nothing configured.
package sources

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client is shared by the sources whose addresses come from .env: short timeout, few redirects,
// small responses.
var Client = &http.Client{
	Timeout: 12 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return http.ErrUseLastResponse
		}
		return nil
	},
}

const maxBody = 4 << 20 // 4 MB is plenty for any feed, ICS or API reply

// FeedClient fetches RSS and Atom feeds. Feed URLs are the one thing here that can be pointed
// anywhere from the dashboard, so unlike Client this one refuses to connect to the loopback,
// private and link local ranges. Without that, a feed, or a redirect from one, could be used to
// read whatever else is listening on the Mac.
var FeedClient = &http.Client{
	Timeout: 12 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return http.ErrUseLastResponse
		}
		return nil
	},
	Transport: &http.Transport{
		DialContext:           guardedDial,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		MaxIdleConnsPerHost:   2,
	},
}

// AllowPrivateFeedHosts turns the guard off. Only tests that serve a feed from a local
// httptest server set it, the same idea as the *_BASE_URL overrides elsewhere in this package.
var AllowPrivateFeedHosts = false

func guardedDial(ctx context.Context, network, addr string) (net.Conn, error) {
	d := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	if AllowPrivateFeedHosts {
		return d.DialContext(ctx, network, addr)
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for _, ip := range ips {
		if why := internalReason(ip.IP); why != "" {
			lastErr = fmt.Errorf("refusing to connect to %s (%s)", ip.IP, why)
			continue
		}
		// dial the address just vetted rather than the name, so a second DNS answer
		// cannot point the connection somewhere else after the check
		conn, err := d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no usable address for %s", host)
	}
	return nil, lastErr
}

// internalReason names why an address is off limits, or returns "" if it is a normal public one.
func internalReason(ip net.IP) string {
	switch {
	case ip.IsLoopback():
		return "loopback"
	case ip.IsPrivate():
		return "private network"
	case ip.IsLinkLocalUnicast(), ip.IsLinkLocalMulticast():
		return "link local"
	case ip.IsInterfaceLocalMulticast():
		return "interface local"
	case ip.IsMulticast():
		return "multicast"
	case ip.IsUnspecified():
		return "unspecified"
	}
	if v4 := ip.To4(); v4 != nil {
		switch {
		case v4[0] == 100 && v4[1]&0xc0 == 64:
			return "carrier grade NAT" // 100.64.0.0/10
		case v4[0] == 192 && v4[1] == 0 && v4[2] == 0:
			return "IETF protocol assignments" // 192.0.0.0/24
		case v4[0] >= 240:
			return "reserved" // 240.0.0.0/4
		}
	}
	return ""
}

// CheckFeedURL rejects a feed address before it is stored, so the dashboard can say why
// instead of saving a feed that will only ever fail.
func CheckFeedURL(ctx context.Context, raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("that is not a URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("the feed URL must start with http:// or https://")
	}
	if u.Hostname() == "" {
		return fmt.Errorf("the feed URL has no host")
	}
	if AllowPrivateFeedHosts {
		return nil
	}
	lookup, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIPAddr(lookup, u.Hostname())
	if err != nil {
		return fmt.Errorf("could not look up %s: %w", u.Hostname(), err)
	}
	for _, ip := range ips {
		if why := internalReason(ip.IP); why != "" {
			return fmt.Errorf("%s points at %s, which is a %s address; feeds must be on the public internet", u.Hostname(), ip.IP, why)
		}
	}
	return nil
}
