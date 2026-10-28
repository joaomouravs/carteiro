// Package netguard keeps outbound webhook requests away from internal
// networks.
//
// Endpoint URLs are chosen by producers, so without a guard Carteiro could be
// used to reach services that are only meant to be reachable from inside its
// own network: databases, admin panels, or the cloud metadata service at
// 169.254.169.254, which hands out credentials. This attack is Server-Side
// Request Forgery (SSRF), and the guard is NFR-SEC-03.
package netguard

import "net/netip"

// blockedPrefixes is the deny list from docs/architecture.md, "Outbound
// network safety". It is written out instead of built from netip.Addr helpers
// such as IsPrivate, because none of those covers carrier-grade NAT or
// "this network", and a reviewer can check a literal list against the doc.
var blockedPrefixes = []netip.Prefix{
	// IPv4.
	netip.MustParsePrefix("0.0.0.0/8"),          // "this network", includes unspecified
	netip.MustParsePrefix("10.0.0.0/8"),         // private
	netip.MustParsePrefix("100.64.0.0/10"),      // carrier-grade NAT
	netip.MustParsePrefix("127.0.0.0/8"),        // loopback
	netip.MustParsePrefix("169.254.0.0/16"),     // link-local, includes cloud metadata
	netip.MustParsePrefix("172.16.0.0/12"),      // private
	netip.MustParsePrefix("192.168.0.0/16"),     // private
	netip.MustParsePrefix("224.0.0.0/4"),        // multicast
	netip.MustParsePrefix("255.255.255.255/32"), // broadcast

	// IPv6.
	netip.MustParsePrefix("::/128"),    // unspecified
	netip.MustParsePrefix("::1/128"),   // loopback
	netip.MustParsePrefix("fc00::/7"),  // unique local
	netip.MustParsePrefix("fe80::/10"), // link-local
	netip.MustParsePrefix("ff00::/8"),  // multicast
}

// nat64Prefix is the well-known NAT64 prefix (RFC 6052). A NAT64 gateway
// turns 64:ff9b::a.b.c.d into a connection to the IPv4 address a.b.c.d.
var nat64Prefix = netip.MustParsePrefix("64:ff9b::/96")

// Blocked reports whether Carteiro must refuse to connect to ip.
//
// It must block, at least (docs/architecture.md, "Outbound network safety"):
//
//   - loopback: 127.0.0.0/8 and ::1
//   - private networks: 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16 and the
//     IPv6 unique local range fc00::/7
//   - link-local: 169.254.0.0/16 (includes cloud metadata) and fe80::/10
//   - carrier-grade NAT: 100.64.0.0/10
//   - "this network" and unspecified: 0.0.0.0/8 and ::
//   - multicast (224.0.0.0/4, ff00::/8) and the broadcast 255.255.255.255
//   - IPv6 addresses that carry an IPv4 address inside them, when that IPv4
//     address is blocked: IPv4-mapped (::ffff:127.0.0.1) and NAT64
//     (64:ff9b::7f00:1, which a NAT64 gateway turns into 127.0.0.1)
//   - the zero netip.Addr, which is not an address at all
//
// Anything else is allowed. The tests in netguard_test.go are the full
// specification.
func Blocked(ip netip.Addr) bool {
	// Fail closed: an address that did not parse is never safe.
	if !ip.IsValid() {
		return true
	}

	// Normalize first, so the prefix checks below only ever see plain IPv4
	// or plain IPv6. Prefix.Contains returns false for any address with a
	// zone (fe80::1%eth0), and an IPv4 prefix never contains an IPv6
	// address, so skipping either step would let the address through.
	ip = ip.WithZone("").Unmap()
	if nat64Prefix.Contains(ip) {
		b := ip.As16()
		ip = netip.AddrFrom4([4]byte(b[12:]))
	}

	for _, p := range blockedPrefixes {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}
