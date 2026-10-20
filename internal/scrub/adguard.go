package scrub

import (
	"fmt"
	"regexp"
	"strings"
)

// QueryLogEntries is how many of AdGuard Home's newest query log entries a
// recorded /control/querylog keeps.
const QueryLogEntries = 20

// adguard trims what AdGuard Home's answers say about browsing: the query
// log keeps its newest entries, and every queried domain there and in the
// stats' top lists becomes siteN.example. It runs before the text pass, so
// the homelab's own names still read as such and become home.example.
func (s *Scrubber) adguard(file string, v any) any {
	switch {
	case strings.HasSuffix(file, "control/querylog.json"):
		return s.queryLog(v)
	case strings.HasSuffix(file, "control/stats.json"):
		return s.stats(v)
	}
	return v
}

func (s *Scrubber) queryLog(v any) any {
	log, ok := v.(object)
	if !ok {
		return v
	}
	data, _ := log.get("data").([]any)
	if len(data) > QueryLogEntries {
		data = data[:QueryLogEntries]
		log.set("data", data)
		if last, ok := data[len(data)-1].(object); ok && last.get("time") != nil {
			log.set("oldest", last.get("time"))
		}
	}
	for _, e := range data {
		entry, ok := e.(object)
		if !ok {
			continue
		}
		renamed := map[string]string{}
		rename := func(o object, key string) {
			if name, ok := o.get(key).(string); ok {
				site := s.site(name)
				o.set(key, site)
				renamed[strings.TrimSuffix(name, ".")] = strings.TrimSuffix(site, ".")
			}
		}
		if q, ok := entry.get("question").(object); ok {
			rename(q, "name")
			rename(q, "unicode_name")
		}
		for _, key := range []string{"answer", "original_answer"} {
			answers, _ := entry.get(key).([]any)
			for _, a := range answers {
				if a, ok := a.(object); ok && a.get("type") != "A" && a.get("type") != "AAAA" {
					rename(a, "value")
				}
			}
		}
		// The same names elsewhere in the entry, e.g. in a rule's text.
		for name, site := range renamed {
			if name == site || name == "" {
				continue
			}
			re := regexp.MustCompile(`(?i)` + regexp.QuoteMeta(name))
			walkStrings(entry, func(t string) string {
				return replace(re, t, domainBoundary, func(string) string { return site })
			})
		}
	}
	return log
}

func (s *Scrubber) stats(v any) any {
	stats, ok := v.(object)
	if !ok {
		return v
	}
	for _, key := range []string{"top_queried_domains", "top_blocked_domains"} {
		top, _ := stats.get(key).([]any)
		for _, e := range top {
			if e, ok := e.(object); ok {
				for i := range e {
					e[i].key = s.site(e[i].key)
				}
			}
		}
	}
	return stats
}

// site is the siteN.example a queried domain becomes, numbered in the order
// this Scrubber first met each. The homelab's own names and names already
// scrubbed (….example) are kept for the text pass.
func (s *Scrubber) site(name string) string {
	dot := strings.HasSuffix(name, ".")
	n := strings.ToLower(strings.TrimSuffix(name, "."))
	if n == "" || n == "example" || strings.HasSuffix(n, ".example") {
		return name
	}
	for _, re := range s.domains {
		if m := re.FindStringIndex(n); m != nil && m[1] == len(n) && !domainBoundary(n, m[0]-1, m[1]) {
			return name
		}
	}
	s.mu.Lock()
	site, ok := s.sites[n]
	if !ok {
		site = fmt.Sprintf("site%d.example", len(s.sites)+1)
		s.sites[n] = site
	}
	s.mu.Unlock()
	if dot {
		site += "."
	}
	return site
}
