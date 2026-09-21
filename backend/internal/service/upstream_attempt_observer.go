package service

import (
	"context"
	"errors"
	"net/http"
	"time"
)

var errUpstreamAttemptFirstOutputCanceled = errors.New("upstream attempt canceled before first output")

// upstreamAttemptObserver is the protocol-facing boundary for request-scoped
// observation. Protocol implementations only report their first semantic
// output; feature-specific policy remains behind this interface.
type upstreamAttemptObserver interface {
	allowFirstOutput(elapsed time.Duration) bool
	cancellationSignal() <-chan struct{}
	cancellationError() error
	observationDone() <-chan struct{}
}

type upstreamAttemptObserverContextKey struct{}

type upstreamAttemptObservation struct {
	observer  upstreamAttemptObserver
	startedAt time.Time
}

func withUpstreamAttemptObserver(ctx context.Context, observer upstreamAttemptObserver, startedAt time.Time) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if observer == nil {
		return ctx
	}
	return context.WithValue(ctx, upstreamAttemptObserverContextKey{}, &upstreamAttemptObservation{
		observer:  observer,
		startedAt: startedAt,
	})
}

func upstreamAttemptObservationFromContext(ctx context.Context) *upstreamAttemptObservation {
	if ctx == nil {
		return nil
	}
	observation, _ := ctx.Value(upstreamAttemptObserverContextKey{}).(*upstreamAttemptObservation)
	return observation
}

func allowUpstreamFirstOutput(ctx context.Context) bool {
	observation := upstreamAttemptObservationFromContext(ctx)
	if observation == nil || observation.observer == nil {
		return true
	}
	return observation.observer.allowFirstOutput(time.Since(observation.startedAt))
}

func allowUpstreamFirstOutputResponse(resp *http.Response) bool {
	if resp != nil && resp.Request != nil {
		return allowUpstreamFirstOutput(resp.Request.Context())
	}
	return true
}

func upstreamAttemptCancellationError(ctx context.Context) error {
	if observation := upstreamAttemptObservationFromContext(ctx); observation != nil && observation.observer != nil {
		if err := observation.observer.cancellationError(); err != nil {
			return err
		}
	}
	if err := context.Cause(ctx); err != nil {
		return err
	}
	return errUpstreamAttemptFirstOutputCanceled
}

func upstreamAttemptResponseCancellationError(resp *http.Response) error {
	if resp != nil && resp.Request != nil {
		return upstreamAttemptCancellationError(resp.Request.Context())
	}
	return errUpstreamAttemptFirstOutputCanceled
}

// detachContextWithUpstreamAttemptCancellation preserves feature-requested
// attempt cancellation while intentionally detaching client cancellation for
// upstream stream draining.
func detachContextWithUpstreamAttemptCancellation(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	detached := context.WithoutCancel(ctx)
	observation := upstreamAttemptObservationFromContext(ctx)
	if observation == nil || observation.observer == nil {
		return detached
	}

	guarded, cancel := context.WithCancelCause(detached)
	go func(observer upstreamAttemptObserver) {
		select {
		case <-observer.cancellationSignal():
			if err := observer.cancellationError(); err != nil {
				cancel(err)
			}
		case <-observer.observationDone():
		}
	}(observation.observer)
	return guarded
}
