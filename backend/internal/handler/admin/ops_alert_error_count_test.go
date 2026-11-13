package admin

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpsAlertErrorCountPayload(t *testing.T) {
	for _, test := range []struct {
		name, extra string
		wantError   bool
	}{
		{"valid count", `"window_minutes":10,"sustained_minutes":0,"filters":{"status_codes":[502,503],"excluded_models":["model-a"]}`, false},
		{"fractional count", `"threshold":20.5`, true},
		{"empty codes", `"filters":{"status_codes":[]}`, true},
		{"invalid code", `"filters":{"status_codes":[200]}`, true},
		{"wrong model type", `"filters":{"excluded_models":"model-a"}`, true},
		{"window too long", `"window_minutes":1441`, true},
		{"negative sustained", `"sustained_minutes":-1`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var raw map[string]json.RawMessage
			require.NoError(t, json.Unmarshal([]byte(`{"name":"test","metric_type":"upstream_error_count","operator":">","threshold":20,`+test.extra+`}`), &raw))
			_, err := validateOpsAlertRulePayload(raw)
			if test.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
