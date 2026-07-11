package k3s

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"al.essio.dev/pkg/shellescape"
	"github.com/byteink/ssd/remote"
)

// podStatusFields is the jsonpath row we ask kubectl for: one tab-separated
// line per pod. --allow-missing-template-keys (on by default) leaves fields of
// a not-yet-scheduled pod empty rather than failing the whole query.
const podStatusFields = `{range .items[*]}` +
	`{.metadata.labels.app}{"\t"}` +
	`{.status.phase}{"\t"}` +
	`{.status.startTime}{"\t"}` +
	`{.spec.containers[0].image}{"\t"}` +
	`{.status.containerStatuses[0].ready}{"\n"}{end}`

// GetStatus returns one row per pod in the stack's namespace. An empty
// serviceName covers the whole stack; a named service narrows it via the
// per-service `app` label.
func (c *Client) GetStatus(ctx context.Context, serviceName string) ([]remote.ServiceStatus, error) {
	selector := ""
	if serviceName != "" {
		selector = fmt.Sprintf(" -l app=%s", shellescape.Quote(serviceName))
	}

	cmd := fmt.Sprintf("k3s kubectl get pods -n %s%s -o jsonpath=%s",
		shellescape.Quote(c.namespace), selector, shellescape.Quote(podStatusFields))

	output, err := c.SSH(ctx, cmd)
	if err != nil {
		return nil, err
	}
	return ParsePodStatus(output, time.Now()), nil
}

// ParsePodStatus turns the tab-separated jsonpath rows into status rows sorted
// by service name. Lines missing the mandatory fields are skipped rather than
// failing the command — a partially-scheduled pod is not a reason to show
// nothing.
func ParsePodStatus(output string, now time.Time) []remote.ServiceStatus {
	var rows []remote.ServiceStatus

	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) < 4 || strings.TrimSpace(fields[0]) == "" {
			continue
		}

		row := remote.ServiceStatus{
			Service: fields[0],
			State:   strings.ToLower(fields[1]),
			Uptime:  uptimeSince(fields[2], now),
			Version: remote.ImageTag(fields[3]),
		}
		if len(fields) > 4 && fields[4] == "false" {
			row.Health = "not ready"
		}
		rows = append(rows, row)
	}

	sort.Slice(rows, func(i, j int) bool { return rows[i].Service < rows[j].Service })
	return rows
}

// uptimeSince compacts a pod's RFC3339 start time into an uptime. A pod that
// has not started yet carries no start time and reports "-".
func uptimeSince(startTime string, now time.Time) string {
	started, err := time.Parse(time.RFC3339, startTime)
	if err != nil {
		return "-"
	}
	return shortDuration(now.Sub(started))
}

// shortDuration renders a duration at single-unit precision: 30s, 18m, 3h, 2d.
func shortDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", max(int(d.Seconds()), 0))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours())/24)
	}
}
