package pilot

import "sort"

// Ordering shortens the search; every selected method must still pass the
// complete configured confirmation. No method is excluded solely by history.
func tuningStrategies(c Config, p Profile, a AdaptiveSnapshot, now int64) []Strategy {
	pool := StrategiesFor(c)
	scores := map[string]int{}
	belongs := func(host string) bool {
		for _, domain := range p.Domains {
			if hostMatches(host, domain) {
				return true
			}
		}
		return false
	}
	// Recent own failures come after untried fallbacks, unless this is the
	// saved profile strategy. All matching traffic contexts remain independent.
	for _, r := range a.History {
		if belongs(r.Host) && r.Network == c.WAN && r.Network != "-" && r.Port == 443 && now >= r.Updated && now-r.Updated < 600 && r.Status == "failed" {
			scores[r.Strategy] = -1
		}
	}
	if c.AdaptiveReuse {
		for _, b := range a.Boxes {
			if b.Network != c.WAN || b.Network == "-" || b.Port != 443 || (b.TLS != "tls12" && b.TLS != "tls13") || (b.Family != "ipv4" && !(c.IPv6 && b.Family == "ipv6")) {
				continue
			}
			relevant := false
			for _, r := range b.Hosts {
				if belongs(r.Host) && !r.Paused && r.Pinned == "" {
					relevant = true
					break
				}
			}
			if !relevant {
				continue
			}
			for _, m := range b.Methods {
				if m.Reusable && m.ID != "none" && now >= m.Latest && now-m.Latest <= 7*24*60*60 && scores[m.ID] >= 0 {
					scores[m.ID] = 1
				}
			}
		}
	}
	scores[p.Strategy] = 2
	sort.SliceStable(pool, func(i, j int) bool { return scores[pool[i].ID] > scores[pool[j].ID] })
	return pool
}
