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
