// Package cleanup selects and removes old image tags, build cache, and
// dangling images for ssd deployments. Selection is pure; removal is
// delegated to an ImageCleaner interface implemented per runtime.
package cleanup

import "sort"

// NewCleaner returns an ImageCleaner for the given runtime.
// "compose" → docker, "k3s" → nerdctl/buildctl.
func NewCleaner(runtime string, ssh SSHRunner) ImageCleaner {
	if runtime == "k3s" {
		return NewK3sCleaner(ssh)
	}
	return NewComposeCleaner(ssh)
}

// Tag describes a single image tag on the server.
// Numeric is the parsed integer version when the tag matches ssd's scheme
// (e.g. "ssd-foo-web:57" → Numeric=57). Raw holds the original tag string
// for non-numeric or untagged entries ("<none>", "latest").
type Tag struct {
	Numeric int
	Raw     string
}

// SelectOldTags returns the tags that are safe to remove.
//
// Rules (non-negotiable):
//   - Never remove the running tag.
//   - Keep the top N numeric tags (N = max(keep, 1)).
//   - Non-numeric tags are ignored entirely (not produced by ssd; user data).
//   - Duplicates collapse — a tag numeric is kept or removed once.
//
// Returns an empty slice when nothing should be removed.
func SelectOldTags(tags []Tag, keep, running int) []Tag {
	if keep < 1 {
		keep = 1
	}

	// Deduplicate by numeric, drop non-numeric entries.
	seen := make(map[int]struct{}, len(tags))
	numerics := make([]int, 0, len(tags))
	for _, t := range tags {
		if t.Numeric <= 0 {
			continue
		}
		if _, dup := seen[t.Numeric]; dup {
			continue
		}
		seen[t.Numeric] = struct{}{}
		numerics = append(numerics, t.Numeric)
	}

	// Sort descending so top N are at the front.
	sort.Sort(sort.Reverse(sort.IntSlice(numerics)))

	// Build keep-set: top N + running (if numeric and present in input).
	keepSet := make(map[int]struct{}, keep+1)
	for i := 0; i < keep && i < len(numerics); i++ {
		keepSet[numerics[i]] = struct{}{}
	}
	if running > 0 {
		keepSet[running] = struct{}{}
	}

	// Everything else is old. Preserve descending order.
	old := make([]Tag, 0, len(numerics))
	for _, n := range numerics {
		if _, keep := keepSet[n]; keep {
			continue
		}
		old = append(old, Tag{Numeric: n})
	}
	return old
}
