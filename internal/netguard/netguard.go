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
	// TODO(joaomouravs): implement. Until then every address is allowed and
	// the tests fail.
	_ = ip
	return false
}
