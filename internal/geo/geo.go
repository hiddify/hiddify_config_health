// Package geo resolves a proxy server IP to coarse network intelligence —
// ASN, country and a "flagged" indicator for ranges known to be commonly
// blocked or burned — using an OFFLINE embedded table. No network calls, so it
// works in CI/airgapped and never leaks the looked-up IP to a third party.
//
// The embedded table is intentionally small and curated (datacenter/hosting
// ASNs and known-problematic ranges). It is a coarse signal, not a substitute
// for a full GeoIP database; extend asn-ranges.dat / the table as needed.
package geo

import (
	"net"
	"sort"
)

// Info is the resolved network intelligence for an IP.
type Info struct {
	IP      string `json:"ip,omitempty"`
	ASN     string `json:"asn,omitempty"`
	Org     string `json:"org,omitempty"`
	Country string `json:"country,omitempty"`
	Flagged bool   `json:"flagged"` // range commonly blocked / burned
}

// rangeEntry is one CIDR → metadata mapping.
type rangeEntry struct {
	start, end uint32 // inclusive IPv4 range
	asn        string
	org        string
	country    string
	flagged    bool
}

// table is the embedded, curated range set. Kept small; sorted by start at
// init for binary search. Add entries here or load from asn-ranges.dat.
var table = buildTable([]rawRange{
	// Major hosting/CDN ASNs (useful org/country context; not flagged).
	{"104.16.0.0/13", "AS13335", "Cloudflare", "US", false},
	{"172.64.0.0/13", "AS13335", "Cloudflare", "US", false},
	{"185.112.32.0/22", "AS200000", "Hosting", "NL", false},
	{"34.0.0.0/8", "AS15169", "Google Cloud", "US", false},
	{"35.190.0.0/15", "AS15169", "Google Cloud", "US", false},
	{"3.0.0.0/8", "AS16509", "Amazon AWS", "US", false},
	{"13.32.0.0/15", "AS16509", "Amazon CloudFront", "US", false},
	// Example flagged ranges (ASNs frequently null-routed by censors). These
	// are placeholders to demonstrate the flag mechanism — curate for real use.
	{"5.61.16.0/21", "AS-FLAGGED", "Known-blocked", "IR", true},
})

func init() {
	sort.Slice(table, func(i, j int) bool { return table[i].start < table[j].start })
}

type rawRange struct {
	cidr, asn, org, country string
	flagged                 bool
}

func buildTable(rs []rawRange) []rangeEntry {
	var out []rangeEntry
	for _, r := range rs {
		_, ipnet, err := net.ParseCIDR(r.cidr)
		if err != nil {
			continue
		}
		start := ipToU32(ipnet.IP)
		ones, bits := ipnet.Mask.Size()
		if bits != 32 {
			continue // IPv4 only in this table
		}
		size := uint32(1) << uint(32-ones)
		out = append(out, rangeEntry{
			start: start, end: start + size - 1,
			asn: r.asn, org: r.org, country: r.country, flagged: r.flagged,
		})
	}
	return out
}

// Lookup resolves an IP (or host that is an IP literal) to Info. For hostnames
// it returns minimal Info (no DNS resolution — we avoid network and avoid
// leaking the host). Callers that already have the server IP pass it directly.
func Lookup(ipOrHost string) Info {
	info := Info{IP: ipOrHost}
	ip := net.ParseIP(ipOrHost)
	if ip == nil {
		return info // not an IP literal; skip (no network lookup)
	}
	v4 := ip.To4()
	if v4 == nil {
		return info // IPv6 not in the embedded table
	}
	key := ipToU32(v4)
	// Binary search for the range whose start <= key, then check end.
	i := sort.Search(len(table), func(i int) bool { return table[i].start > key })
	if i > 0 {
		e := table[i-1]
		if key >= e.start && key <= e.end {
			info.ASN, info.Org, info.Country, info.Flagged = e.asn, e.org, e.country, e.flagged
		}
	}
	return info
}

func ipToU32(ip net.IP) uint32 {
	v4 := ip.To4()
	if v4 == nil {
		return 0
	}
	return uint32(v4[0])<<24 | uint32(v4[1])<<16 | uint32(v4[2])<<8 | uint32(v4[3])
}
