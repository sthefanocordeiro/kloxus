package ingest

// robots.txt handling (D27/D30). A minimal RFC 9309 parser: enough to
// honor KloxusBot-specific and wildcard groups, longest-match Allow /
// Disallow, and Crawl-delay. 4xx on robots.txt means "fully allowed";
// 5xx means "assume disallowed" and retry tomorrow — the conservative
// reading Google documents.
import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// robotRules are the parsed directives that apply to our agent.
type robotRules struct {
	fetched    bool
	allowAll   bool
	crawlDelay time.Duration
	rules      []robotRule // ordered; longest path wins, allow wins ties
}

type robotRule struct {
	path  string
	allow bool
}

// robotsAllowed reports whether the rules permit path.
func (r robotRules) allowed(path string) bool {
	if !r.fetched {
		return true // fetch failed transiently; the caller decides policy
	}
	best := -1
	allow := true
	for _, rule := range r.rules {
		if strings.HasPrefix(path, rule.path) && len(rule.path) >= best {
			if len(rule.path) > best || rule.allow {
				best, allow = len(rule.path), rule.allow
			}
		}
	}
	return allow
}

// parseRobots extracts the rules that apply to agent ("KloxusBot"),
// falling back to the wildcard group.
func parseRobots(body string, agent string) robotRules {
	type group struct {
		agents []string
		rules  []robotRule
		delay  time.Duration
	}
	var groups []group
	cur := group{}
	inAgents := true // still reading User-agent lines of the current group
	flush := func() {
		if len(cur.agents) > 0 {
			groups = append(groups, cur)
		}
		cur = group{}
		inAgents = true
	}
	for _, line := range strings.Split(body, "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k, v = strings.ToLower(strings.TrimSpace(k)), strings.TrimSpace(v)
		switch k {
		case "user-agent":
			if !inAgents {
				flush()
			}
			cur.agents = append(cur.agents, strings.ToLower(v))
		case "allow":
			inAgents = false
			cur.rules = append(cur.rules, robotRule{path: v, allow: true})
		case "disallow":
			inAgents = false
			if v != "" {
				cur.rules = append(cur.rules, robotRule{path: v})
			}
		case "crawl-delay":
			inAgents = false
			if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
				cur.delay = time.Duration(f * float64(time.Second))
			}
		}
	}
	flush()

	agent = strings.ToLower(agent)
	var star *group
	for i := range groups {
		for _, a := range groups[i].agents {
			if a == agent {
				g := groups[i]
				return robotRules{fetched: true, rules: g.rules, crawlDelay: g.delay}
			}
			if a == "*" {
				star = &groups[i]
			}
		}
	}
	if star != nil {
		return robotRules{fetched: true, rules: star.rules, crawlDelay: star.delay}
	}
	return robotRules{fetched: true, allowAll: true}
}

// checkRobots fetches and parses the site's robots.txt for feedURL.
// errRobotsDenied is returned (with the rules attached via
// robotsResult) when our path is disallowed.
func (f *FeedFetcher) checkRobots(ctx context.Context, feedURL string) (robotRules, error) {
	u, err := url.Parse(feedURL)
	if err != nil {
		return robotRules{}, err
	}
	robotsURL := (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: "/robots.txt"}).String()
	req, err := http.NewRequestWithContext(ctx, "GET", robotsURL, nil)
	if err != nil {
		return robotRules{}, err
	}
	req.Header.Set("User-Agent", f.userAgent)
	res, err := f.http.Do(req)
	if err != nil {
		return robotRules{}, err
	}
	defer res.Body.Close()
	switch {
	case res.StatusCode >= 400 && res.StatusCode < 500:
		return robotRules{fetched: true, allowAll: true}, nil
	case res.StatusCode >= 500:
		return robotRules{fetched: true}, fmt.Errorf("robots.txt: HTTP %d", res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, 512*1024))
	if err != nil {
		return robotRules{}, err
	}
	rules := parseRobots(string(data), f.agentName)
	if rules.allowAll {
		return rules, nil
	}
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	if u.RawQuery != "" {
		path += "?" + u.RawQuery
	}
	if !rules.allowed(path) {
		return rules, fmt.Errorf("robots.txt disallows %s", u.Path)
	}
	return rules, nil
}
