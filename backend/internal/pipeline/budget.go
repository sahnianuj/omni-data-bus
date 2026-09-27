package pipeline

// Channel capacities (in batches) between the stages (OC-024).
const (
	RowsChanCap = 4
	DocsChanCap = 8
	MinBatch    = 100
)

// BatchSizeFor lowers batchSize until the batches that can be buffered at once fit the memory
// budget (OC-024 AC5): the rows and docs channels, plus one batch in flight per reader and a
// few in the transformer and writer. A document is assumed to be about as large as its row.
func BatchSizeFor(batchSize int, avgRowBytes int64, readers int, budgetBytes int64) int {
	if avgRowBytes <= 0 || budgetBytes <= 0 {
		return batchSize
	}
	buffered := int64(RowsChanCap + DocsChanCap + max(readers, 1) + 4)
	limit := budgetBytes / (buffered * avgRowBytes)
	if int64(batchSize) <= limit {
		return batchSize
	}
	return max(int(limit), MinBatch)
}
