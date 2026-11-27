package service

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPrepareOpenAIRequestGzipRequiresExplicitAPIKeyCapability(t *testing.T) {
	cases := []struct {
		name    string
		account *Account
		want    bool
	}{
		{"nil", nil, false},
		{"default off", &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, false},
		{"enabled", &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: map[string]any{"openai_request_gzip": true}}, true},
		{"string is not opt in", &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: map[string]any{"openai_request_gzip": "true"}}, false},
		{"OAuth unaffected", &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{"openai_request_gzip": true}}, false},
		{"other platform", &Account{Platform: "anthropic", Type: AccountTypeAPIKey, Extra: map[string]any{"openai_request_gzip": true}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(`{"input":"` + strings.Repeat("context ", 20000) + `"}`)
			req, _ := http.NewRequest(http.MethodPost, "https://example.invalid/v1/responses", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			prepareOpenAIRequestGzip(req, tc.account)
			if got := req.Header.Get("Content-Encoding") == "gzip"; got != tc.want {
				t.Fatalf("gzip = %v, want %v", got, tc.want)
			}
		})
	}
}

type gzipTransportTestUpstream struct {
	HTTPUpstream
	client *http.Client
}

func (u *gzipTransportTestUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return u.client.Do(req)
}

func TestOpenAIRequestGzipHTTP2SSEAndAccountIsolation(t *testing.T) {
	original := []byte(`{"model":"test-model","stream":true,"prompt_cache_key":"stable-cache-key","input":"` + strings.Repeat("unchanged context ", 12000) + `"}`)
	const firstEvent = "data: {\"type\":\"response.output_text.delta\",\"delta\":\"OK\"}\n\n"
	const finalEvent = "data: {\"type\":\"response.completed\"}\n\n"
	type observation struct {
		encoding string
		body     []byte
		readErr  error
		length   int64
		protocol int
	}
	observed := make(chan observation, 1)
	finishStream := make(chan struct{}, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reader io.Reader = r.Body
		if r.Header.Get("Content-Encoding") == "gzip" {
			decoded, err := gzip.NewReader(r.Body)
			if err != nil {
				http.Error(w, "invalid gzip", http.StatusBadRequest)
				return
			}
			defer decoded.Close()
			reader = decoded
		}
		body, err := io.ReadAll(reader)
		observed <- observation{r.Header.Get("Content-Encoding"), body, err, r.ContentLength, r.ProtoMajor}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, firstEvent)
		w.(http.Flusher).Flush()
		select {
		case <-finishStream:
			_, _ = io.WriteString(w, finalEvent)
		case <-r.Context().Done():
		}
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	upstream := &gzipTransportTestUpstream{client: server.Client()}
	svc := &OpenAIGatewayService{httpUpstream: upstream, pluginManager: &PluginManager{}}
	for _, tc := range []struct {
		name    string
		account *Account
		gzip    bool
	}{
		{"enabled account", &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: map[string]any{"openai_request_gzip": true}}, true},
		{"different account disabled", &Account{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, false},
		{"same account switched off", &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: map[string]any{"openai_request_gzip": false}}, false},
		{"OAuth ignored", &Account{ID: 3, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{"openai_request_gzip": true}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL, bytes.NewReader(original))
			require.NoError(t, err)
			req.Header.Set("Content-Type", "application/json")
			resp, err := svc.doOpenAIUpstream(req, "", tc.account)
			require.NoError(t, err)
			defer resp.Body.Close()
			require.Equal(t, http.StatusOK, resp.StatusCode)
			var got observation
			select {
			case got = <-observed:
			case <-ctx.Done():
				t.Fatal("upstream request was not observed")
			}
			require.NoError(t, got.readErr)
			require.Equal(t, 2, got.protocol)
			require.Equal(t, original, got.body, "JSON and prompt_cache_key must survive unchanged")
			require.Equal(t, tc.gzip, got.encoding == "gzip")
			if tc.gzip {
				require.Less(t, got.length, int64(len(original)))
			} else {
				require.Equal(t, int64(len(original)), got.length)
			}
			// The server cannot complete until the first event is received here.
			first := make([]byte, len(firstEvent))
			_, err = io.ReadFull(resp.Body, first)
			require.NoError(t, err)
			require.Equal(t, firstEvent, string(first))
			finishStream <- struct{}{}
			rest, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.Equal(t, finalEvent, string(rest))
		})
	}
}
