package service

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"

	"github.com/formbricks/hub/internal/huberrors"
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
		// IPv4-compatible IPv6 (::/96) is the same trick as NAT64/6to4 — an IPv6 wrapper around an
		// IPv4 destination — and Unmap() does not touch it, since it only collapses ::ffff:0:0/96.
		{"::7f00:1", true, "IPv4-compatible ::127.0.0.1"},
		{"::127.0.0.1", true, "IPv4-compatible, dotted form"},
		{"::a9fe:a9fe", true, "IPv4-compatible ::169.254.169.254 (IMDS)"},
		{"::a00:1", true, "IPv4-compatible ::10.0.0.1"},
		{"2001:0:1234::1", true, "Teredo — tunnels IPv4 like 6to4"},
		{"2001:db8::1", true, "IPv6 documentation range"},
		{"100::1", true, "IPv6 discard-only prefix"},

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
		// ::/96 must not swallow the IPv4-mapped range, whose 6th group is ffff.
		{"::ffff:93.184.216.34", false, "IPv4-mapped PUBLIC address"},
		// 2001::/32 is Teredo; the rest of 2001::/16 is ordinary global unicast.
		{"2001:4860:4860::8888", false, "public IPv6 in 2001::/16 but outside Teredo"},
		{"2001:db9::1", false, "public IPv6, immediately above the documentation range"},
		{"101::1", false, "public IPv6, immediately above the discard prefix"},
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

// TestSSRFPolicy_RejectionReason pins the two rejection reasons apart. They were briefly collapsed
// into one message while refactoring, which told an operator "private/internal" for a host they had
// themselves put in WEBHOOK_BLACKLIST — the opposite of actionable.
func TestSSRFPolicy_RejectionReason(t *testing.T) {
	tests := []struct {
		name    string
		policy  SSRFPolicy
		addr    string
		want    ssrfRejection
		wantMsg string
	}{
		{
			name:    "reserved range",
			policy:  SSRFPolicy{},
			addr:    "100.64.0.1",
			want:    ssrfPrivateOrReserved,
			wantMsg: "private/internal",
		},
		{
			name:    "blacklisted public address",
			policy:  SSRFPolicy{Blacklist: map[string]struct{}{"8.8.8.8": {}}},
			addr:    "8.8.8.8",
			want:    ssrfBlacklisted,
			wantMsg: "blacklisted",
		},
		{
			// The reason must survive the allowlist: the operator denied this address explicitly.
			name: "blacklisted inside an allowlisted range",
			policy: SSRFPolicy{
				Blacklist:    map[string]struct{}{"100.64.0.1": {}},
				AllowedCIDRs: []netip.Prefix{netip.MustParsePrefix("100.64.0.0/10")},
			},
			addr:    "100.64.0.1",
			want:    ssrfBlacklisted,
			wantMsg: "blacklisted",
		},
		{
			name:   "allowed",
			policy: SSRFPolicy{},
			addr:   "8.8.8.8",
			want:   ssrfAllowed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.policy.classify(netip.MustParseAddr(tt.addr))
			if got != tt.want {
				t.Fatalf("classify(%s) = %v, want %v", tt.addr, got, tt.want)
			}

			err := got.validationError()
			if tt.wantMsg == "" {
				if err != nil {
					t.Fatalf("validationError() = %v, want nil", err)
				}

				return
			}

			var verr *huberrors.ValidationError
			if !errors.As(err, &verr) {
				t.Fatalf("validationError() = %v, want a *huberrors.ValidationError", err)
			}

			if !strings.Contains(verr.Message, tt.wantMsg) {
				t.Errorf("message %q does not contain %q", verr.Message, tt.wantMsg)
			}
		})
	}
}

// TestSSRFPolicy_RejectsZonedAddresses covers a bypass of the CIDR list specifically.
//
// netip.Prefix.Contains is documented to return false for an address carrying an IPv6 zone, and
// Go's url.Parse preserves the zone through u.Hostname() — so `[64:ff9b::a9fe:a9fe%25eth0]`
// skipped every entry in blockedPrefixes and reached IMDS, while the netip predicates (loopback,
// RFC1918, link-local) kept working. Only the ranges added as CIDRs were affected.
func TestSSRFPolicy_RejectsZonedAddresses(t *testing.T) {
	zoned := []struct {
		addr string
		// reservedWithoutZone is whether the address is still private/reserved once the zone is
		// stripped. Only those must also be caught by isPrivateOrReserved on its own; a *public*
		// address with a zone is rejected by classify, not by the range classifier.
		reservedWithoutZone bool
	}{
		{"::7f00:1%eth0", true},                            // IPv4-compatible loopback
		{"64:ff9b::a9fe:a9fe%eth0", true},                  // NAT64 -> IMDS
		{"64:ff9b::7f00:1%eth0", true},                     // NAT64 -> loopback
		{"2002:7f00:1::1%eth0", true},                      // 6to4 -> loopback
		{"fec0::1%eth0", true},                             // site-local
		{"2001:db8::1%eth0", true},                         // documentation
		{"fe80::1%eth0", true},                             // link-local (already blocked by the predicate)
		{"2606:2800:220:1:248:1893:25c8:1946%eth0", false}, // public; the zone alone disqualifies it
	}

	for _, tt := range zoned {
		t.Run(tt.addr, func(t *testing.T) {
			addr, err := netip.ParseAddr(tt.addr)
			if err != nil {
				t.Fatalf("bad test address %q: %v", tt.addr, err)
			}

			if addr.Zone() == "" {
				t.Fatalf("test address %q lost its zone; the case no longer covers what it claims", tt.addr)
			}

			if (SSRFPolicy{}).permits(addr) {
				t.Errorf("permits(%s) = true, want false: a zoned address bypasses Prefix.Contains", tt.addr)
			}

			// The range classifier must also hold on its own terms, so stripping the zone cannot
			// fail open for any other caller.
			if got := isPrivateOrReserved(addr); got != tt.reservedWithoutZone {
				t.Errorf("isPrivateOrReserved(%s) = %v, want %v", tt.addr, got, tt.reservedWithoutZone)
			}
		})
	}
}

// TestValidateWebhookURLHost_ZonedIPv6 drives the bypass through the real entry point, since it
// depends on url.Parse keeping the zone.
func TestValidateWebhookURLHost_ZonedIPv6(t *testing.T) {
	ctx := context.Background()

	for _, raw := range []string{
		"https://[64:ff9b::a9fe:a9fe%25eth0]/webhook",
		"https://[::7f00:1%25eth0]/webhook",
		"https://[fec0::1%25eth0]/webhook",
	} {
		t.Run(raw, func(t *testing.T) {
			err := validateWebhookURLHost(ctx, raw, SSRFPolicy{})
			if !errors.Is(err, huberrors.ErrValidation) {
				t.Fatalf("validateWebhookURLHost(%q) = %v, want a validation error", raw, err)
			}
		})
	}
}
