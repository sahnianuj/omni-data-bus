package progress

// ExactCountMaxBytes is the table size up to which exactCount "auto" runs count(*) (OC-027).
const ExactCountMaxBytes = 5 << 30

// CountExactly applies the exactCount option: always, never, or auto (tables up to 5 GB).
// The count runs inside the job's shared snapshot, so it matches exactly what's read.
func CountExactly(policy string, sizeBytes int64) bool {
	switch policy {
	case "always":
		return true
	case "never":
		return false
	}
	return sizeBytes <= ExactCountMaxBytes
}
