package schema

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExclusiveArc_OC171(t *testing.T) {
	require.Equal(t, []string{"svc_id", "scan_and_pay_qr_code_id"}, ExclusiveArc(`CHECK ((num_nonnulls(svc_id, scan_and_pay_qr_code_id) = 1))`))
	pg := `CHECK ((((((
CASE
    WHEN (cash_tender_id IS NOT NULL) THEN 1
    ELSE 0
END +
CASE
    WHEN (credit_card_id IS NOT NULL) THEN 1
    ELSE 0
END) +
CASE
    WHEN (scan_and_pay_qr_code_id IS NOT NULL) THEN 1
    ELSE 0
END) +
CASE
    WHEN (svc_id IS NOT NULL) THEN 1
    ELSE 0
END) +
CASE
    WHEN (scc_id IS NOT NULL) THEN 1
    ELSE 0
END) = 1))`
	require.Equal(t, []string{"cash_tender_id", "credit_card_id", "scan_and_pay_qr_code_id", "svc_id", "scc_id"}, ExclusiveArc(pg))
	require.Nil(t, ExclusiveArc(`CHECK ((char_length((last_four_digits)::text) = 4))`))
	require.Nil(t, ExclusiveArc(`CHECK ((num_nonnulls(a, b) = 2))`), "two of them, not one")
	require.Nil(t, ExclusiveArc(`CHECK (((CASE WHEN (a IS NOT NULL) THEN 1 ELSE 0 END + CASE WHEN (b IS NOT NULL) THEN 1 ELSE 0 END) <= 1))`), "at most one is not exactly one")
}
