package netguard

import (
	"net/netip"
	"testing"
)

// Each case names why the address is blocked or allowed. Boundary addresses
// (the first and last of a range, and their neighbors just outside) catch
// off-by-one prefix lengths, such as 172.16.0.0/16 written instead of /12.
var cases = []struct {
	addr    string
	blocked bool
	why     string
}{
	// Public addresses.
	{"8.8.8.8", false, "public IPv4"},
	{"1.1.1.1", false, "public IPv4"},
	{"2001:4860:4860::8888", false, "public IPv6"},
	{"2606:4700:4700::1111", false, "public IPv6"},

	// Loopback.
	{"127.0.0.1", true, "loopback"},
	{"127.255.255.255", true, "loopback, last address of 127.0.0.0/8"},
	{"128.0.0.0", false, "just past 127.0.0.0/8"},
	{"::1", true, "IPv6 loopback"},

	// Private networks (RFC 1918).
	{"10.0.0.1", true, "private 10/8"},
	{"10.255.255.255", true, "private 10/8, last address"},
	{"11.0.0.0", false, "just past 10/8"},
	{"9.255.255.255", false, "just before 10/8"},
	{"172.16.0.0", true, "private 172.16/12, first address"},
	{"172.31.255.255", true, "private 172.16/12, last address"},
	{"172.15.255.255", false, "just before 172.16/12"},
	{"172.32.0.0", false, "just past 172.16/12"},
	{"192.168.0.1", true, "private 192.168/16"},
	{"192.168.255.255", true, "private 192.168/16, last address"},
	{"192.169.0.0", false, "just past 192.168/16"},

	// IPv6 unique local addresses (fc00::/7 covers fc00:: to fdff::).
	{"fc00::1", true, "unique local"},
	{"fd12:3456:789a::1", true, "unique local, fd00::/8 half"},
	{"fe00::1", false, "just past fc00::/7"},

	// Link-local, including the cloud metadata service.
	{"169.254.169.254", true, "cloud metadata service (AWS, GCP, Azure)"},
	{"169.254.0.0", true, "link-local, first address"},
	{"169.255.0.0", false, "just past 169.254/16"},
	{"fe80::1", true, "IPv6 link-local"},
	{"fe80::1%eth0", true, "IPv6 link-local with a zone"},
	{"febf:ffff::1", true, "IPv6 link-local, end of fe80::/10"},

	// Carrier-grade NAT (RFC 6598).
	{"100.64.0.0", true, "CGNAT, first address"},
	{"100.127.255.255", true, "CGNAT, last address"},
	{"100.63.255.255", false, "just before 100.64/10"},
	{"100.128.0.0", false, "just past 100.64/10"},

	// "This network" and unspecified: connecting to 0.0.0.0 reaches the
	// local host on most systems.
	{"0.0.0.0", true, "unspecified IPv4"},
	{"0.1.2.3", true, "0.0.0.0/8"},
	{"::", true, "unspecified IPv6"},

	// Multicast and broadcast.
	{"224.0.0.1", true, "multicast"},
	{"239.255.255.255", true, "multicast, last address"},
	{"223.255.255.255", false, "just before multicast"},
	{"255.255.255.255", true, "broadcast"},
	{"ff02::1", true, "IPv6 multicast"},

	// IPv6 addresses that embed an IPv4 address. Forgetting these is a
	// classic bypass: the attacker writes the internal address in IPv6 form.
	{"::ffff:127.0.0.1", true, "IPv4-mapped loopback"},
	{"::ffff:169.254.169.254", true, "IPv4-mapped metadata service"},
	{"::ffff:10.0.0.1", true, "IPv4-mapped private"},
	{"::ffff:8.8.8.8", false, "IPv4-mapped public address"},
	{"64:ff9b::7f00:1", true, "NAT64 form of 127.0.0.1"},
	{"64:ff9b::a9fe:a9fe", true, "NAT64 form of 169.254.169.254"},
	{"64:ff9b::808:808", false, "NAT64 form of 8.8.8.8"},
}

func TestBlocked(t *testing.T) {
	for _, tc := range cases {
		t.Run(tc.addr, func(t *testing.T) {
			ip := netip.MustParseAddr(tc.addr)
			if got := Blocked(ip); got != tc.blocked {
				t.Errorf("Blocked(%s) = %v, want %v (%s)", tc.addr, got, tc.blocked, tc.why)
			}
		})
	}
}

// TestBlockedZeroAddr checks that the guard fails closed: an address that
// failed to parse must never count as safe.
func TestBlockedZeroAddr(t *testing.T) {
	if !Blocked(netip.Addr{}) {
		t.Error("Blocked(netip.Addr{}) = false, want true")
	}
}

// TestMappedMatchesIPv4 checks every IPv4 case again in its IPv4-mapped IPv6
// form (::ffff:a.b.c.d). The answer must not depend on how the address is
// written.
func TestMappedMatchesIPv4(t *testing.T) {
	for _, tc := range cases {
		ip := netip.MustParseAddr(tc.addr)
		if !ip.Is4() {
			continue
		}
		mapped := netip.AddrFrom16(ip.As16())
		if got := Blocked(mapped); got != tc.blocked {
			t.Errorf("Blocked(%s) = %v, but Blocked(%s) = %v", mapped, got, tc.addr, tc.blocked)
		}
	}
}
