package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestLiveBeats_DropsExpired(t *testing.T) {
	now := time.Date(2026, 11, 2, 14, 0, 0, 0, time.UTC)
	beats := map[string]NodeBeat{
		"b": {ID: "b", Role: NodeRoleWorker, Version: "2.0.0", SeenAt: now.Add(-10 * time.Second)},
		"a": {ID: "a", Role: NodeRoleAPI, Version: "2.0.0", SeenAt: now},
		"z": {ID: "z", Role: NodeRoleWorker, Version: "1.0.1", SeenAt: now.Add(-NodeTTL)},
	}
	live := LiveBeats(beats, now)
	assert.Equal(t, []string{"a", "b"}, []string{live[0].ID, live[1].ID})
}

func TestVersionsByRole(t *testing.T) {
	byRole, ok := VersionsByRole([]NodeBeat{{Role: NodeRoleAll, Version: "2.0.0"}})
	assert.True(t, ok)
	assert.Equal(t, map[string][]string{"api": {"2.0.0"}, "worker": {"2.0.0"}}, byRole)

	byRole, ok = VersionsByRole([]NodeBeat{
		{Role: NodeRoleAPI, Version: "2.0.0"}, {Role: NodeRoleWorker, Version: "1.0.1"}, {Role: NodeRoleWorker, Version: "2.0.0"},
	})
	assert.False(t, ok)
	assert.Equal(t, map[string][]string{"api": {"2.0.0"}, "worker": {"1.0.1", "2.0.0"}}, byRole)

	byRole, ok = VersionsByRole(nil)
	assert.True(t, ok)
	assert.Equal(t, map[string][]string{"api": {}, "worker": {}}, byRole)
}
