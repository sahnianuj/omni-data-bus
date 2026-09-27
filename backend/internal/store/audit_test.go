package store

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAuditFilter_Matches(t *testing.T) {
	e := AuditEvent{Action: "job.create", EntityType: "job", UserID: "u1"}
	for f, want := range map[AuditFilter]bool{
		{}:                             true,
		{Action: "job.create"}:         true,
		{Action: "job."}:               true,
		{Action: "job"}:                false, // a prefix must end in "."
		{Action: "project."}:           false,
		{Action: "job.cancel"}:         false,
		{EntityType: "job"}:            true,
		{EntityType: "project"}:        false,
		{UserID: "u1"}:                 true,
		{UserID: "u2"}:                 false,
		{Action: "job.", UserID: "u1"}: true,
	} {
		require.Equal(t, want, f.Matches(e), "%+v", f)
	}
}
