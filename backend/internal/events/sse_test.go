package events

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSSEFormat_AC6_Framing(t *testing.T) {
	require.Equal(t, "id: 184\nevent: progress\ndata: {\"seq\":184}\n\n",
		string(Format(Event{ID: 184, Type: TypeProgress, Data: json.RawMessage(`{"seq":184}`)})))
	// Multi-line data: each line gets its own data: prefix, so a newline can't end the event.
	require.Equal(t, "event: error-sample\ndata: {\ndata:   \"a\": 1\ndata: }\n\n",
		string(Format(Event{Type: TypeErrorSample, Data: json.RawMessage("{\r\n  \"a\": 1\n}")})))
}
