package service

import (
	"net/netip"
	"testing"
)

// TestIsPrivateOrReserved covers the classifier directly, including every range that Go's netip
// predicates do not model (ENG-2310) and the public controls that must keep working — an
// over-broad prefix is as much a bug as a missing one.
func TestIsPrivateOrReserved(t *testing.T) {
	tests := []struct {
		addr string
		want bool
		why  string
	}{
		// Covered by the netip predicates. These are regression cases: the predicates must be
		// kept alongside the CIDR list, not replaced by it.
		{"127.0.0.1", true, "loopback"},
		{"127.5.5.5", true, "loopback, whole /8"},
		{"10.0.0.5", true, "RFC1918 10/8"},
		{"172.16.0.1", true, "RFC1918 172.16/12"},
		{"192.168.1.1", true, "RFC1918 192.168/16"},
		{"169.254.169.254", true, "link-local, cloud metadata (IMDS)"},
		{"0.0.0.0", true, "unspecified"},
		{"::1", true, "IPv6 loopback"},
		{"fd00::1", true, "IPv6 ULA fc00::/7"},
		{"fe80::1", true, "IPv6 link-local, bottom of fe80::/10"},
		// fe80::/10 spans fe80:: through febf::. A "fe80:" string-prefix classifier (as used on the
		// Formbricks side) covers only fe80::/16 and would let these two through; IsLinkLocalUnicast
		// covers the full range. Guards against "porting" that classifier over here.
		{"fe9f::1", true, "IPv6 link-local, middle of fe80::/10"},
		{"febf::1", true, "IPv6 link-local, top of fe80::/10"},
		{"::ffff:127.0.0.1", true, "IPv4-mapped loopback"},
		{"::ffff:10.0.0.1", true, "IPv4-mapped RFC1918"},

		// Multicast: IsLinkLocalMulticast alone only caught ff02:: and 224.0.0.0/24, so every
		// other scope leaked. IsMulticast covers 224/4 and ff00::/8 in full.
		{"224.0.0.1", true, "IPv4 multicast, link-local scope"},
		{"224.0.1.1", true, "IPv4 multicast, outside 224.0.0.0/24"},
		{"225.0.0.1", true, "IPv4 multicast, outside 224/8"},
		{"239.255.255.250", true, "IPv4 multicast, SSDP"},
		{"ff01::1", true, "IPv6 multicast, interface-local scope"},
		{"ff02::1", true, "IPv6 multicast, link-local scope"},
		{"ff05::1", true, "IPv6 multicast, site-local scope"},
		{"ff0e::1", true, "IPv6 multicast, global scope"},

		// The CIDR list: ranges netip has no predicate for.
		{"0.1.2.3", true, "0.0.0.0/8 — IsUnspecified only matches 0.0.0.0 itself"},
		{"100.64.0.1", true, "CGNAT bottom — Tailscale tailnets"},
		{"100.100.100.100", true, "CGNAT — Tailscale MagicDNS"},
		{"100.127.255.254", true, "CGNAT top"},
		{"192.0.0.1", true, "IETF protocol assignments"},
		{"192.0.2.5", true, "TEST-NET-1"},
		{"198.51.100.5", true, "TEST-NET-2"},
		{"203.0.113.5", true, "TEST-NET-3"},
		{"198.18.0.1", true, "benchmarking 198.18/15"},
		{"198.19.255.254", true, "benchmarking, top of /15"},
		{"240.0.0.1", true, "reserved 240/4"},
		{"250.1.2.3", true, "reserved 240/4, mid-range"},
		{"255.255.255.255", true, "limited broadcast"},
		{"168.63.129.16", true, "Azure WireServer, outside 169.254/16"},
		{"64:ff9b::a9fe:a9fe", true, "NAT64 encoding of 169.254.169.254"},
		{"64:ff9b::7f00:1", true, "NAT64 encoding of 127.0.0.1"},
		{"64:ff9b:1::1", true, "NAT64 local-use (RFC 8215)"},
		{"2002:7f00:1::1", true, "6to4 encoding of 127.0.0.1"},
		{"fec0::1", true, "deprecated IPv6 site-local"},

		// Must stay reachable. These catch a prefix written one bit too wide.
		{"8.8.8.8", false, "public DNS"},
		{"93.184.216.34", false, "public (example.com)"},
		{"100.128.0.1", false, "public, immediately above CGNAT"},
		{"100.63.255.255", false, "public, immediately below CGNAT"},
		{"172.32.0.1", false, "public, immediately above RFC1918 172.16/12"},
		{"192.0.3.1", false, "public, immediately above TEST-NET-1"},
		{"198.20.0.1", false, "public, immediately above benchmarking"},
		{"239.255.255.249", true, "still multicast (boundary sanity)"},
		{"2606:2800:220:1:248:1893:25c8:1946", false, "public IPv6 (example.com)"},
		{"2003::1", false, "public IPv6, immediately above 6to4"},
		{"64:ff9c::1", false, "public IPv6, immediately above NAT64 well-known"},
	}

	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			addr, err := netip.ParseAddr(tt.addr)
			if err != nil {
				t.Fatalf("bad test address %q: %v", tt.addr, err)
			}

			if got := isPrivateOrReserved(addr); got != tt.want {
				t.Errorf("isPrivateOrReserved(%s) = %v, want %v (%s)", tt.addr, got, tt.want, tt.why)
			}
		})
	}
}

// TestSSRFPolicy_Permits covers how the blacklist, the allowlist and the range check compose.
func TestSSRFPolicy_Permits(t *testing.T) {
	tailnet := netip.MustParsePrefix("100.64.0.0/10")

	tests := []struct {
		name   string
		policy SSRFPolicy
		addr   string
		want   bool
	}{
		{
			name:   "zero policy still blocks reserved ranges",
			policy: SSRFPolicy{},
			addr:   "100.64.0.1",
			want:   false,
		},
		{
			name:   "zero policy allows public",
			policy: SSRFPolicy{},
			addr:   "8.8.8.8",
			want:   true,
		},
		{
			name:   "allowlist re-permits a configured range",
			policy: SSRFPolicy{AllowedCIDRs: []netip.Prefix{tailnet}},
			addr:   "100.64.0.1",
			want:   true,
		},
		{
			name:   "allowlist does not leak beyond its range",
			policy: SSRFPolicy{AllowedCIDRs: []netip.Prefix{tailnet}},
			addr:   "10.0.0.1",
			want:   false,
		},
		{
			// An operator-set denylist entry is an explicit deny and must win, otherwise a broad
			// allowlist would silently re-open a host the operator named.
			name: "blacklist beats allowlist",
			policy: SSRFPolicy{
				Blacklist:    map[string]struct{}{"100.64.0.1": {}},
				AllowedCIDRs: []netip.Prefix{tailnet},
			},
			addr: "100.64.0.1",
			want: false,
		},
		{
			name: "blacklist blocks an otherwise-public address",
			policy: SSRFPolicy{
				Blacklist: map[string]struct{}{"8.8.8.8": {}},
			},
			addr: "8.8.8.8",
			want: false,
		},
		{
			// The allowlist is matched after Unmap, so a mapped form of an allowed address resolves
			// the same way its IPv4 form does.
			name:   "allowlist matches IPv4-mapped form",
			policy: SSRFPolicy{AllowedCIDRs: []netip.Prefix{tailnet}},
			addr:   "::ffff:100.64.0.1",
			want:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr := netip.MustParseAddr(tt.addr)

			if got := tt.policy.permits(addr); got != tt.want {
				t.Errorf("permits(%s) = %v, want %v", tt.addr, got, tt.want)
			}
		})
	}
}
