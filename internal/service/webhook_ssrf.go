package service

import (
	"net/netip"
	"strings"
)

// SSRFPolicy holds the webhook URL restrictions applied at create/update time and again at dial time.
// The zero value still rejects private/reserved ranges; Blacklist and AllowedCIDRs are both optional.
type SSRFPolicy struct {
	// Blacklist is a set of normalized hostnames/IPs that can never be used as webhook URLs.
	// An entry here is an explicit deny and wins over AllowedCIDRs.
	Blacklist map[string]struct{}

	// AllowedCIDRs re-permits specific private/reserved ranges that isPrivateOrReserved would
	// otherwise block, for operators whose webhook receivers legitimately live on internal
	// addresses (e.g. a tailnet in 100.64.0.0/10). It does not override Blacklist.
	AllowedCIDRs []netip.Prefix
}

// NewSSRFPolicy builds a policy from config primitives. Both arguments may be nil/empty; the
// resulting policy still rejects private/reserved ranges.
func NewSSRFPolicy(blacklist map[string]struct{}, allowedCIDRs []netip.Prefix) SSRFPolicy {
	return SSRFPolicy{Blacklist: blacklist, AllowedCIDRs: allowedCIDRs}
}

// blocked returns true if host (a canonicalized hostname or IP string) is explicitly denied.
func (p SSRFPolicy) blocked(host string) bool {
	if p.Blacklist == nil {
		return false
	}

	_, found := p.Blacklist[host]

	return found
}

// allows returns true if addr falls in an operator-configured allowlist range.
func (p SSRFPolicy) allows(addr netip.Addr) bool {
	addr = addr.Unmap()

	for _, prefix := range p.AllowedCIDRs {
		if prefix.Contains(addr) {
			return true
		}
	}

	return false
}

// permits reports whether addr may be used as a webhook target under this policy.
// The explicit blacklist is checked first so it always wins, then the allowlist, then the ranges.
func (p SSRFPolicy) permits(addr netip.Addr) bool {
	addr = addr.Unmap()

	if p.blocked(addr.String()) {
		return false
	}

	return p.allows(addr) || !isPrivateOrReserved(addr)
}

// blockedPrefixes are private/reserved ranges that Go's netip predicates do not model.
//
// netip covers RFC1918 + fc00::/7 (IsPrivate), 127/8 + ::1 (IsLoopback), 169.254/16 + fe80::/10
// (IsLinkLocalUnicast), 224/4 + ff00::/8 (IsMulticast) and the single address 0.0.0.0 / ::
// (IsUnspecified) — so everything below has to be matched as a CIDR instead. Kept as prefixes
// rather than predicates because a string-prefix or regex classifier is what let these leak in
// the first place (see ENG-2310, ENG-1326).
var blockedPrefixes = []netip.Prefix{
	// IPv4
	netip.MustParsePrefix("0.0.0.0/8"),          // "this network" — IsUnspecified only matches 0.0.0.0 itself
	netip.MustParsePrefix("100.64.0.0/10"),      // CGNAT / shared address space (RFC 6598) — Tailscale tailnets
	netip.MustParsePrefix("192.0.0.0/24"),       // IETF protocol assignments
	netip.MustParsePrefix("192.0.2.0/24"),       // TEST-NET-1 (documentation)
	netip.MustParsePrefix("198.51.100.0/24"),    // TEST-NET-2 (documentation)
	netip.MustParsePrefix("203.0.113.0/24"),     // TEST-NET-3 (documentation)
	netip.MustParsePrefix("198.18.0.0/15"),      // benchmarking (RFC 2544)
	netip.MustParsePrefix("240.0.0.0/4"),        // reserved for future use
	netip.MustParsePrefix("255.255.255.255/32"), // limited broadcast
	netip.MustParsePrefix("168.63.129.16/32"),   // Azure WireServer — sibling of IMDS, outside 169.254/16

	// IPv6 transition ranges: these encode an IPv4 destination the predicates never see.
	netip.MustParsePrefix("64:ff9b::/96"),   // NAT64 well-known (64:ff9b::a9fe:a9fe == 169.254.169.254)
	netip.MustParsePrefix("64:ff9b:1::/48"), // NAT64 local-use (RFC 8215)
	netip.MustParsePrefix("2002::/16"),      // 6to4 (2002:7f00:1::1 == 127.0.0.1)
	netip.MustParsePrefix("fec0::/10"),      // deprecated site-local, just above fe80::/10
}

// isPrivateOrReserved returns true if the IP is loopback, private, link-local, multicast,
// unspecified, or falls in one of the reserved ranges netip has no predicate for.
//
// The netip predicates are kept and the CIDR list only adds to them: IsLinkLocalUnicast covers
// the whole of fe80::/10, which a "fe80:" string-prefix check would not, so replacing the
// predicates with a prefix table would open a gap rather than close one.
func isPrivateOrReserved(addr netip.Addr) bool {
	addr = addr.Unmap()

	// IsMulticast is a superset of IsLinkLocalMulticast (224/4 and ff00::/8 in full), which is why
	// the previous IsLinkLocalMulticast-only check leaked every multicast scope above ff02::.
	if addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() ||
		addr.IsMulticast() || addr.IsUnspecified() {
		return true
	}

	for _, prefix := range blockedPrefixes {
		if prefix.Contains(addr) {
			return true
		}
	}

	return false
}

// canonicalizeHost normalizes host for blacklist lookup (trim trailing dots, lowercase).
func canonicalizeHost(host string) string {
	h := strings.TrimSpace(strings.ToLower(host))
	h = strings.TrimRight(h, ".")

	return h
}
