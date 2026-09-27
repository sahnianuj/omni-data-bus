package couchbase

import (
	"sort"
)

// Role is one RBAC role assignment as reported by system:my_user_info.
type Role struct {
	Role       string `json:"role"`
	Bucket     string `json:"bucket_name,omitempty"`
	Scope      string `json:"scope_name,omitempty"`
	Collection string `json:"collection_name,omitempty"`
}

// Capabilities describe what the credential may do on the target (OC-012 AC4).
type Capabilities struct {
	CanListBuckets  bool     `json:"canListBuckets"`
	CanCreateBucket bool     `json:"canCreateBucket"`
	CanManageScopes bool     `json:"canManageScopes"`
	CanWriteDocs    bool     `json:"canWriteDocs"`
	RangeScan       bool     `json:"rangeScan"`
	QueryService    bool     `json:"queryService"`
	Deployment      string   `json:"deployment"`
	BucketsAllowed  []string `json:"bucketsAllowed,omitempty"` // empty with AllBuckets=true means any bucket
	AllBuckets      bool     `json:"allBuckets"`
	Reasons         []string `json:"reasons,omitempty"`
}

// ReasonCapellaBuckets explains why bucket creation is unavailable on Capella.
const ReasonCapellaBuckets = "Create buckets in the Capella UI, then pick them here."

// CapabilitiesFromRoles derives capabilities from RBAC roles.
func CapabilitiesFromRoles(roles []Role, deployment string) Capabilities {
	c := Capabilities{Deployment: deployment}
	buckets := map[string]bool{}
	for _, r := range roles {
		switch r.Role {
		case "admin", "cluster_admin":
			c.CanListBuckets, c.CanCreateBucket, c.CanManageScopes, c.CanWriteDocs, c.AllBuckets = true, true, true, true, true
		case "bucket_admin":
			c.CanManageScopes = true
			buckets[r.Bucket] = true
		case "scope_admin":
			c.CanManageScopes = true
			buckets[r.Bucket] = true
		case "bucket_full_access", "data_writer":
			c.CanWriteDocs = true
			buckets[r.Bucket] = true
		case "ro_admin", "data_reader", "query_select", "data_dcp_reader":
			if r.Bucket != "" {
				buckets[r.Bucket] = true
			}
		}
	}
	if buckets["*"] {
		c.AllBuckets = true
		delete(buckets, "*")
	}
	if !c.AllBuckets {
		for b := range buckets {
			if b != "" {
				c.BucketsAllowed = append(c.BucketsAllowed, b)
			}
		}
		sort.Strings(c.BucketsAllowed)
	}
	if deployment == Capella {
		c.CanCreateBucket = false
		c.Reasons = append(c.Reasons, ReasonCapellaBuckets)
	}
	return c
}
