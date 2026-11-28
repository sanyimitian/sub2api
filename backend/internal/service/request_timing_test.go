package service

import (
	"testing"
	"time"
)

func TestRequestTimingRecorderAccumulatesQueueWaitAndCopiesSnapshots(t *testing.T) {
	recorder := NewRequestTimingRecorder()
	recorder.AddAccountQueueWait(20 * time.Millisecond)
	recorder.AddAccountQueueWait(35 * time.Millisecond)

	first := recorder.Snapshot()
	if first == nil || first.AccountQueueWaitMs == nil || *first.AccountQueueWaitMs != 55 {
		t.Fatalf("unexpected accumulated account queue wait: %#v", first)
	}
	*first.AccountQueueWaitMs = 1

	second := recorder.Snapshot()
	if second == nil || second.AccountQueueWaitMs == nil || *second.AccountQueueWaitMs != 55 {
		t.Fatalf("snapshot mutation changed recorder state: %#v", second)
	}
}

func TestRequestTimingRecorderStartsWithZeroAccountQueueWait(t *testing.T) {
	timings := NewRequestTimingRecorder().Snapshot()
	if timings == nil || timings.AccountQueueWaitMs == nil || *timings.AccountQueueWaitMs != 0 {
		t.Fatalf("expected a zero account queue wait, got %#v", timings)
	}
}

func TestRequestTimingPayloadSizesAreIsolatedBetweenAttempts(t *testing.T) {
	recorder := NewRequestTimingRecorder()
	recorder.SetUpstreamRequestBodyBytes(2000000, 300000)
	first := recorder.Snapshot()
	if *first.UpstreamRequestBodyBytes != 2000000 || *first.UpstreamRequestWireBytes != 300000 {
		t.Fatalf("unexpected sizes: %#v", first)
	}
	*first.UpstreamRequestWireBytes = 1
	if *recorder.Snapshot().UpstreamRequestWireBytes != 300000 {
		t.Fatal("snapshot mutation changed the recorder")
	}
	recorder.BeginUpstreamAttempt()
	recorder.SetUpstreamRequestBodyBytes(-1, -1)
	second := recorder.Snapshot()
	if second.UpstreamRequestBodyBytes != nil || second.UpstreamRequestWireBytes != nil {
		t.Fatal("previous attempt sizes leaked into a streamed attempt")
	}
}
