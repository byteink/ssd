package remote

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseComposeStatus_NDJSON(t *testing.T) {
	out := `{"Service":"backend","State":"running","Status":"Up 18 minutes","Health":"","Image":"ssd-arcline-backend:7","Publishers":[{"URL":"0.0.0.0","TargetPort":8090,"PublishedPort":8092,"Protocol":"tcp"},{"URL":"::","TargetPort":8090,"PublishedPort":8092,"Protocol":"tcp"}]}
{"Service":"kb","State":"running","Status":"Up 2 minutes (healthy)","Health":"healthy","Image":"ssd-arcline-kb:3","Publishers":[{"URL":"","TargetPort":9001,"PublishedPort":0,"Protocol":"tcp"}]}`

	rows, err := ParseComposeStatus(out)
	require.NoError(t, err)
	require.Len(t, rows, 2)

	assert.Equal(t, ServiceStatus{
		Service: "backend",
		State:   "running",
		Uptime:  "18m",
		Version: "7",
		Ports:   "8092→8090",
	}, rows[0])

	assert.Equal(t, ServiceStatus{
		Service: "kb",
		State:   "running",
		Health:  "healthy",
		Uptime:  "2m",
		Version: "3",
	}, rows[1])
}

func TestParseComposeStatus_JSONArray(t *testing.T) {
	out := `[{"Service":"web","State":"exited","Status":"Exited (1) 3 minutes ago","Image":"ssd-arcline-web:14"}]`

	rows, err := ParseComposeStatus(out)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, ServiceStatus{
		Service: "web",
		State:   "exited",
		Uptime:  "-",
		Version: "14",
	}, rows[0])
}

func TestParseComposeStatus_SortedByService(t *testing.T) {
	out := `{"Service":"web","State":"running","Status":"Up 1 second","Image":"a:1"}
{"Service":"api","State":"running","Status":"Up 1 second","Image":"b:2"}`

	rows, err := ParseComposeStatus(out)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, "api", rows[0].Service)
	assert.Equal(t, "web", rows[1].Service)
}

func TestParseComposeStatus_Empty(t *testing.T) {
	rows, err := ParseComposeStatus("  \n ")
	require.NoError(t, err)
	assert.Empty(t, rows)
}

func TestParseComposeStatus_Malformed(t *testing.T) {
	_, err := ParseComposeStatus("not json")
	assert.Error(t, err)
}

func TestParseComposeStatus_ImageWithoutTag(t *testing.T) {
	out := `{"Service":"web","State":"running","Status":"Up 5 minutes","Image":"nginx"}`

	rows, err := ParseComposeStatus(out)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "-", rows[0].Version)
}

func TestParseComposeStatus_ImageWithRegistryPort(t *testing.T) {
	out := `{"Service":"web","State":"running","Status":"Up 5 minutes","Image":"registry.local:5000/team/web"}`

	rows, err := ParseComposeStatus(out)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "-", rows[0].Version, "registry port must not be read as a tag")
}

func TestParseComposeStatus_PortsCapped(t *testing.T) {
	out := `{"Service":"web","State":"running","Status":"Up 5 minutes","Image":"a:1","Publishers":[` +
		`{"TargetPort":80,"PublishedPort":80},` +
		`{"TargetPort":443,"PublishedPort":443},` +
		`{"TargetPort":8080,"PublishedPort":8080},` +
		`{"TargetPort":9090,"PublishedPort":9090}]}`

	rows, err := ParseComposeStatus(out)
	require.NoError(t, err)
	assert.Equal(t, "80→80, 443→443 +2", rows[0].Ports)
}

func TestUptimeFromDockerStatus(t *testing.T) {
	cases := map[string]string{
		"Up 18 minutes":              "18m",
		"Up 2 minutes (healthy)":     "2m",
		"Up About a minute":          "1m",
		"Up About an hour":           "1h",
		"Up Less than a second":      "0s",
		"Up 15 seconds":              "15s",
		"Up 3 hours":                 "3h",
		"Up 2 days":                  "2d",
		"Up 5 weeks":                 "5w",
		"Up 1 second":                "1s",
		"Exited (1) 3 minutes ago":   "-",
		"Restarting (1) 4 seconds a": "-",
		"":                           "-",
	}

	for status, want := range cases {
		assert.Equal(t, want, uptimeFromDockerStatus(status), status)
	}
}
