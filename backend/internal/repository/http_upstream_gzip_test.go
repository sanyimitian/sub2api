package repository

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestHTTPUpstreamGzipPayloadTimingSizes(t *testing.T) {
	body := `{"input":"` + strings.Repeat("unchanged context ", 20000) + `"}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reader io.Reader = r.Body
		if r.Header.Get("Content-Encoding") == "gzip" {
			decoded, err := gzip.NewReader(r.Body)
			if err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			defer decoded.Close()
			reader = decoded
		}
		got, err := io.ReadAll(reader)
		if err != nil || string(got) != body {
			t.Errorf("body changed: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	recorder := service.NewRequestTimingRecorder()
	ctx := service.WithRequestTimingRecorder(t.Context(), recorder)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	require.True(t, pkghttputil.GzipJSONRequest(req))
	resp, err := doUpstreamRequest(server.Client(), req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	timings := recorder.Snapshot()
	require.Equal(t, int64(len(body)), *timings.UpstreamRequestBodyBytes)
	require.Equal(t, req.ContentLength, *timings.UpstreamRequestWireBytes)

	// Reusing the recorder for an unknown-length body must clear earlier sizes.
	req, err = http.NewRequestWithContext(ctx, http.MethodPost, server.URL, io.NopCloser(strings.NewReader(body)))
	require.NoError(t, err)
	resp, err = doUpstreamRequest(server.Client(), req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Nil(t, recorder.Snapshot().UpstreamRequestBodyBytes)
	require.Nil(t, recorder.Snapshot().UpstreamRequestWireBytes)
}
