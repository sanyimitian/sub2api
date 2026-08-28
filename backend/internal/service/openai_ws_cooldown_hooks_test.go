package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestRunOpenAIWSBeforeUpstreamSendWrapsSafeLaterTurnPayload(t *testing.T) {
	guardErr := errors.New("shared cooldown guard blocked account")
	hooks := &OpenAIWSIngressHooks{
		BeforeUpstreamSend: func(turn int, upstreamModel string) error {
			require.Equal(t, 2, turn)
			require.Equal(t, "mapped-model", upstreamModel)
			return guardErr
		},
	}
	payload := []byte(`{"type":"response.create","model":"mapped-model","previous_response_id":"resp_old"}`)
	fullInput := []json.RawMessage{
		json.RawMessage(`{"role":"user","content":"first"}`),
		json.RawMessage(`{"role":"assistant","content":"answer"}`),
		json.RawMessage(`{"role":"user","content":"second"}`),
	}

	err := runOpenAIWSBeforeUpstreamSend(hooks, 2, "mapped-model", payload, fullInput, true, "client-model")
	require.ErrorIs(t, err, guardErr)
	retryPayload, retryCurrentTurn := OpenAIWSCurrentTurnRetryPayload(err)
	require.True(t, retryCurrentTurn)
	require.NotEmpty(t, retryPayload)
	require.Equal(t, "client-model", gjson.GetBytes(retryPayload, "model").String())
	require.False(t, gjson.GetBytes(retryPayload, "previous_response_id").Exists())
	require.Equal(t, int64(3), gjson.GetBytes(retryPayload, "input.#").Int())
}

type countingPassthroughDialer struct {
	inner openAIWSClientDialer
	calls atomic.Int32
}

func (d *countingPassthroughDialer) Dial(ctx context.Context, rawURL string, headers http.Header, proxyURL string) (openAIWSClientConn, int, http.Header, error) {
	d.calls.Add(1)
	return d.inner.Dial(ctx, rawURL, headers, proxyURL)
}

func TestPassthroughIngressRunsUpstreamSendGuardBeforeFirstDial(t *testing.T) {
	controlCtx, cancelControl := context.WithCancelCause(context.Background())
	defer cancelControl(context.Canceled)

	upstream := newStagedPassthroughConn()
	svc := newPassthroughLifecycleService(passthroughLifecycleConfig(), upstream)
	dialer := &countingPassthroughDialer{inner: svc.openaiWSPassthroughDialer}
	svc.openaiWSPassthroughDialer = dialer
	guardErr := errors.New("shared cooldown guard blocked account")
	guardCalls := atomic.Int32{}
	hooks := &OpenAIWSIngressHooks{
		BeforeUpstreamSend: func(turn int, upstreamModel string) error {
			guardCalls.Add(1)
			require.Equal(t, 1, turn)
			require.Equal(t, "gpt-5.1", upstreamModel)
			return guardErr
		},
	}

	server, serverErr := startPassthroughHookRecordingServer(t, controlCtx, svc, passthroughLifecycleAccount(), hooks)
	defer server.Close()
	clientConn := dialPassthroughLifecycleClient(t, server)
	defer func() { _ = clientConn.CloseNow() }()

	select {
	case err := <-serverErr:
		require.ErrorIs(t, err, guardErr)
	case <-time.After(3 * time.Second):
		t.Fatal("passthrough send guard did not terminate the attempt")
	}
	require.EqualValues(t, 1, guardCalls.Load())
	require.Zero(t, dialer.calls.Load(), "guard rejection must happen before the authenticated upstream dial")
	select {
	case payload := <-upstream.writes:
		t.Fatalf("guarded payload unexpectedly reached upstream: %s", payload)
	default:
	}
}

func TestPassthroughIngressReportsPerTurnRequestAndResponseLifecycle(t *testing.T) {
	controlCtx, cancelControl := context.WithCancelCause(context.Background())
	defer cancelControl(context.Canceled)

	upstream := newStagedPassthroughConn()
	upstream.Send(`{"type":"response.completed","response":{"id":"resp_cooldown_1","model":"gpt-5.1","usage":{"input_tokens":1,"output_tokens":1}}}`)

	guarded := make(chan int, 2)
	sent := make(chan int, 2)
	started := make(chan int, 2)
	var eventMu sync.Mutex
	var events []string
	hooks := &OpenAIWSIngressHooks{
		InitialTurnStartedAt: time.Now(),
		BeforeUpstreamSend: func(turn int, upstreamModel string) error {
			require.Equal(t, "gpt-5.1", upstreamModel)
			guarded <- turn
			return nil
		},
		UpstreamRequestSent: func(turn int) { sent <- turn },
		DownstreamResponseStarted: func(turn int) {
			eventMu.Lock()
			events = append(events, fmt.Sprintf("started:%d", turn))
			eventMu.Unlock()
			started <- turn
		},
		AfterTurn: func(turn int, _ *OpenAIForwardResult, _ error) {
			eventMu.Lock()
			events = append(events, fmt.Sprintf("finished:%d", turn))
			eventMu.Unlock()
		},
	}

	server, serverErr := startPassthroughHookRecordingServer(
		t,
		controlCtx,
		newPassthroughLifecycleService(passthroughLifecycleConfig(), upstream),
		passthroughLifecycleAccount(),
		hooks,
	)
	defer server.Close()
	clientConn := dialPassthroughLifecycleClient(t, server)
	defer func() { _ = clientConn.CloseNow() }()

	require.Equal(t, 1, <-guarded)
	require.Equal(t, 1, <-sent)
	require.Equal(t, "response.create", gjson.GetBytes(requirePassthroughUpstreamWrite(t, upstream, 3*time.Second), "type").String())
	completed, err := readPassthroughLifecycleFrame(t, clientConn, 3*time.Second)
	require.NoError(t, err)
	require.Equal(t, "response.completed", gjson.GetBytes(completed, "type").String())
	require.Equal(t, 1, <-started)
	eventMu.Lock()
	require.Equal(t, []string{"started:1", "finished:1"}, append([]string(nil), events...))
	eventMu.Unlock()

	writeCtx, cancelWrite := context.WithTimeout(context.Background(), 3*time.Second)
	require.NoError(t, clientConn.Write(writeCtx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5.1","previous_response_id":"resp_cooldown_1"}`)))
	cancelWrite()
	require.Equal(t, 2, <-guarded)
	require.Equal(t, 2, <-sent)
	require.Equal(t, "response.create", gjson.GetBytes(requirePassthroughUpstreamWrite(t, upstream, 3*time.Second), "type").String())
	upstream.Send(`{"type":"response.completed","response":{"id":"resp_cooldown_2","model":"gpt-5.1","usage":{"input_tokens":1,"output_tokens":1}}}`)
	completed, err = readPassthroughLifecycleFrame(t, clientConn, 3*time.Second)
	require.NoError(t, err)
	require.Equal(t, "response.completed", gjson.GetBytes(completed, "type").String())
	require.Equal(t, 2, <-started)
	eventMu.Lock()
	require.Equal(t, []string{"started:1", "finished:1", "started:2", "finished:2"}, append([]string(nil), events...))
	eventMu.Unlock()

	require.NoError(t, clientConn.Close(coderws.StatusNormalClosure, "done"))
	select {
	case <-serverErr:
	case <-time.After(3 * time.Second):
		t.Fatal("passthrough ingress did not exit")
	}
}
