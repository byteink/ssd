package deploy

import (
	"testing"

	"github.com/byteink/ssd/config"
	"github.com/stretchr/testify/assert"
)

// svc is a tiny config builder for ordering tests.
func svc(name string, deps ...string) *config.Config {
	d := make(config.Dependencies, len(deps))
	for i, dep := range deps {
		d[i] = config.Dependency{Name: dep}
	}
	return &config.Config{Name: name, DependsOn: d}
}

// pos returns the index of name in order (or -1).
func pos(order []string, name string) int {
	for i, n := range order {
		if n == name {
			return i
		}
	}
	return -1
}

// TestOrderByDependsOn_StackShape is the Bug 4 regression: the exact db +
// backend(needs db) + web shape. Alphabetical order is backend, db, frontend —
// which starts backend before db and deadlocks. The order must place db before
// backend, and every service must appear exactly once.
func TestOrderByDependsOn_StackShape(t *testing.T) {
	services := map[string]*config.Config{
		"db":       svc("db"),
		"backend":  svc("backend", "db"),
		"frontend": svc("frontend", "backend"),
	}

	order := OrderByDependsOn(services)

	assert.Len(t, order, 3, "every service must appear exactly once")
	assert.Less(t, pos(order, "db"), pos(order, "backend"), "db must start before backend")
	assert.Less(t, pos(order, "backend"), pos(order, "frontend"), "backend must start before frontend")
}

func TestOrderByDependsOn_NoDeps_Alphabetical(t *testing.T) {
	services := map[string]*config.Config{
		"c": svc("c"), "a": svc("a"), "b": svc("b"),
	}
	assert.Equal(t, []string{"a", "b", "c"}, OrderByDependsOn(services))
}

func TestOrderByDependsOn_TieBreakAlphabetical(t *testing.T) {
	// Two services depend on db; among ready peers, order is alphabetical.
	services := map[string]*config.Config{
		"db":    svc("db"),
		"zebra": svc("zebra", "db"),
		"apple": svc("apple", "db"),
	}
	order := OrderByDependsOn(services)
	assert.Equal(t, "db", order[0])
	assert.Equal(t, []string{"apple", "zebra"}, order[1:])
}

// A depends_on naming a service not in the set is ignored, not fatal.
func TestOrderByDependsOn_DanglingDepIgnored(t *testing.T) {
	services := map[string]*config.Config{
		"web": svc("web", "ghost"),
	}
	assert.Equal(t, []string{"web"}, OrderByDependsOn(services))
}

// A cycle must not drop services: all still appear (order best-effort).
func TestOrderByDependsOn_CycleKeepsAllServices(t *testing.T) {
	services := map[string]*config.Config{
		"a": svc("a", "b"),
		"b": svc("b", "a"),
		"c": svc("c"),
	}
	order := OrderByDependsOn(services)
	assert.Len(t, order, 3)
	assert.ElementsMatch(t, []string{"a", "b", "c"}, order)
}

func TestOrderByDependsOn_Empty(t *testing.T) {
	assert.Empty(t, OrderByDependsOn(map[string]*config.Config{}))
}
