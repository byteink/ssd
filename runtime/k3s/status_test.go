package k3s

import (
	"testing"
	"time"

	"github.com/byteink/ssd/remote"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParsePodStatus(t *testing.T) {
	now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	out := "web\tRunning\t2026-07-12T11:42:00Z\tssd-arcline-web:14\ttrue\n" +
		"api\tRunning\t2026-07-12T11:59:30Z\tssd-arcline-api:3\tfalse\n" +
		"db\tPending\t\tpostgres:17\t\n"

	rows := ParsePodStatus(out, now)
	require.Len(t, rows, 3)

	assert.Equal(t, remote.ServiceStatus{
		Service: "api",
		State:   "running",
		Health:  "not ready",
		Uptime:  "30s",
		Version: "3",
	}, rows[0])

	assert.Equal(t, remote.ServiceStatus{
		Service: "db",
		State:   "pending",
		Uptime:  "-",
		Version: "17",
	}, rows[1])

	assert.Equal(t, remote.ServiceStatus{
		Service: "web",
		State:   "running",
		Uptime:  "18m",
		Version: "14",
	}, rows[2])
}

func TestParsePodStatus_Empty(t *testing.T) {
	assert.Empty(t, ParsePodStatus("  \n", time.Now()))
}

func TestParsePodStatus_SkipsShortLines(t *testing.T) {
	assert.Empty(t, ParsePodStatus("web\tRunning\n", time.Now()))
}

func TestShortDuration(t *testing.T) {
	cases := map[time.Duration]string{
		500 * time.Millisecond: "0s",
		30 * time.Second:       "30s",
		90 * time.Second:       "1m",
		18 * time.Minute:       "18m",
		3 * time.Hour:          "3h",
		49 * time.Hour:         "2d",
		-1 * time.Second:       "0s",
	}

	for d, want := range cases {
		assert.Equal(t, want, shortDuration(d), d.String())
	}
}
