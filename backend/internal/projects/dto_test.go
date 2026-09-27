package projects

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/store"
)

func TestToDTO_ReposAndLatestJob(t *testing.T) {
	d := ToDTO(Summary{
		Project:   store.Project{ID: "p1", Name: "Shop"},
		Source:    &store.Connection{ID: "p1::source", Type: "postgres", Name: "Source"},
		Target:    &store.Connection{ID: "p1::target", Type: "couchbase", Name: "Target", LastTestStatus: "passed"},
		Status:    StatusReady,
		LatestJob: &store.Job{ID: "j1", Status: store.JobRunning},
	})
	require.Equal(t, "untested", d.Source.LastTestStatus, "a repository never tested reads as untested")
	require.Equal(t, "passed", d.Target.LastTestStatus)
	require.Equal(t, &JobRef{ID: "j1", Status: store.JobRunning}, d.LatestJob)

	empty := ToDTO(Summary{Project: store.Project{ID: "p2"}})
	require.Nil(t, empty.Source)
	require.Nil(t, empty.Target)
	require.Nil(t, empty.LatestJob)
}

func TestNewService_DefaultsToNoJobs(t *testing.T) {
	s := NewService(nil, nil, nil)
	active, err := s.jobs.HasActiveJob(context.Background(), "ws", "p1")
	require.NoError(t, err)
	require.False(t, active)
}

func TestIsNotFound(t *testing.T) {
	require.True(t, IsNotFound(fmt.Errorf("get project: %w", store.ErrNotFound)))
	require.False(t, IsNotFound(fmt.Errorf("boom")))
}
