package remote

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
)

// maxPortsShown caps the PORTS cell so a service publishing a whole range
// doesn't blow the table width; the remainder is summarised as "+N".
const maxPortsShown = 2

// ServiceStatus is one running instance of a service, as observed on the
// server. Both runtimes fill the same shape so `ssd status` renders one table
// regardless of runtime.
type ServiceStatus struct {
	Service string
	State   string // running, exited, restarting, pending, ...
	Health  string // healthy, unhealthy, starting, not ready — empty when unknown
	Uptime  string // compact: 15s, 18m, 3h, 2d — "-" when not running
	Version string // image tag — "-" when the image is untagged
	Ports   string // published host→container ports — empty when none
}

// composePS is the subset of `docker compose ps --format json` we consume.
type composePS struct {
	Service    string `json:"Service"`
	State      string `json:"State"`
	Status     string `json:"Status"`
	Health     string `json:"Health"`
	Image      string `json:"Image"`
	Publishers []struct {
		TargetPort    int `json:"TargetPort"`
		PublishedPort int `json:"PublishedPort"`
	} `json:"Publishers"`
}

// ParseComposeStatus turns `docker compose ps --format json` output into rows
// sorted by service name. Compose emits a JSON array on some versions and
// newline-delimited objects on others, so both are accepted.
func ParseComposeStatus(output string) ([]ServiceStatus, error) {
	entries, err := decodeComposePS(strings.TrimSpace(output))
	if err != nil {
		return nil, err
	}

	rows := make([]ServiceStatus, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, ServiceStatus{
			Service: e.Service,
			State:   e.State,
			Health:  e.Health,
			Uptime:  uptimeFromDockerStatus(e.Status),
			Version: ImageTag(e.Image),
			Ports:   e.ports(),
		})
	}

	sort.Slice(rows, func(i, j int) bool { return rows[i].Service < rows[j].Service })
	return rows, nil
}

func decodeComposePS(output string) ([]composePS, error) {
	if output == "" {
		return nil, nil
	}

	if strings.HasPrefix(output, "[") {
		var entries []composePS
		if err := json.Unmarshal([]byte(output), &entries); err != nil {
			return nil, fmt.Errorf("parsing compose ps output: %w", err)
		}
		return entries, nil
	}

	var entries []composePS
	for _, line := range strings.Split(output, "\n") {
		if line = strings.TrimSpace(line); line == "" {
			continue
		}
		var e composePS
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			return nil, fmt.Errorf("parsing compose ps output: %w", err)
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// ports renders the published host→container mappings, deduplicated (compose
// lists IPv4 and IPv6 publishers separately for the same mapping).
func (e composePS) ports() string {
	var mapped []string
	seen := make(map[string]bool)
	for _, p := range e.Publishers {
		if p.PublishedPort == 0 {
			continue // container-internal port, not reachable from the host
		}
		m := fmt.Sprintf("%d→%d", p.PublishedPort, p.TargetPort)
		if seen[m] {
			continue
		}
		seen[m] = true
		mapped = append(mapped, m)
	}

	if len(mapped) <= maxPortsShown {
		return strings.Join(mapped, ", ")
	}
	return fmt.Sprintf("%s +%d", strings.Join(mapped[:maxPortsShown], ", "), len(mapped)-maxPortsShown)
}

// ImageTag extracts the tag from an image reference. A registry port
// (registry.local:5000/web) is not a tag, so only the final path element is
// inspected. Untagged images report "-".
func ImageTag(image string) string {
	_, tag, found := strings.Cut(path.Base(image), ":")
	if !found || tag == "" {
		return "-"
	}
	return tag
}

// dockerUptime matches the human duration docker prints in the Status column
// of a running container, e.g. "Up 18 minutes", "Up About an hour (healthy)".
var dockerUptime = regexp.MustCompile(`^Up (\d+|About an?|Less than a) (second|minute|hour|day|week|month|year)s?`)

var unitSuffix = map[string]string{
	"second": "s",
	"minute": "m",
	"hour":   "h",
	"day":    "d",
	"week":   "w",
	"month":  "mo",
	"year":   "y",
}

// uptimeFromDockerStatus compacts docker's Status column into a short uptime
// ("Up 18 minutes" → "18m"). Anything not currently up (exited, restarting)
// has no uptime and reports "-".
func uptimeFromDockerStatus(status string) string {
	m := dockerUptime.FindStringSubmatch(status)
	if m == nil {
		return "-"
	}

	count := m[1]
	switch {
	case strings.HasPrefix(count, "About"):
		count = "1"
	case strings.HasPrefix(count, "Less than"):
		count = "0"
	}
	return count + unitSuffix[m[2]]
}
