package pagination_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"webhooks-go/internal/pagination"
)

func TestSeqCursorRoundTrips(t *testing.T) {
	encoded := pagination.EncodeSeqCursor(pagination.SeqCursor{Seq: 42})
	decoded, ok := pagination.DecodeSeqCursor(encoded)
	require.True(t, ok)
	require.Equal(t, int64(42), decoded.Seq)
}

func TestDecodeSeqCursorRejectsMalformedInput(t *testing.T) {
	_, ok := pagination.DecodeSeqCursor("not-valid-base64!!!")
	require.False(t, ok)
}

// deliveries.seq is a BIGSERIAL starting at 1 — a non-positive value can
// never be a real cursor, only malformed/garbage input smuggled in as
// otherwise-valid JSON.
func TestDecodeSeqCursorRejectsNonPositiveSeq(t *testing.T) {
	zero := pagination.EncodeSeqCursor(pagination.SeqCursor{Seq: 0})
	_, ok := pagination.DecodeSeqCursor(zero)
	require.False(t, ok)

	negative := pagination.EncodeSeqCursor(pagination.SeqCursor{Seq: -5})
	_, ok = pagination.DecodeSeqCursor(negative)
	require.False(t, ok)
}
