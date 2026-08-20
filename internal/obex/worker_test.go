package obex

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWorkerSerializesOperations(t *testing.T) {
	worker := newWorker(2)
	defer worker.Stop()

	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	secondStarted := make(chan struct{})
	firstDone := make(chan error, 1)
	secondDone := make(chan error, 1)

	go func() {
		firstDone <- worker.Submit(context.Background(), func(context.Context) error {
			close(firstStarted)
			<-releaseFirst
			return nil
		})
	}()
	<-firstStarted
	go func() {
		secondDone <- worker.Submit(context.Background(), func(context.Context) error {
			close(secondStarted)
			return nil
		})
	}()

	select {
	case <-secondStarted:
		t.Fatal("second operation started before the first operation completed")
	case <-time.After(10 * time.Millisecond):
	}
	close(releaseFirst)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
}

func TestWorkerRejectsOperationBeyondCapacity(t *testing.T) {
	worker := newWorker(1)
	defer worker.Stop()

	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- worker.Submit(context.Background(), func(context.Context) error {
			close(started)
			<-release
			return nil
		})
	}()
	<-started
	err := worker.Submit(context.Background(), func(context.Context) error { return nil })
	if !errors.Is(err, ErrQueueFull) {
		t.Fatalf("error %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestWorkerStopCancelsActiveOperation(t *testing.T) {
	worker := newWorker(1)
	callerCtx, cancelCaller := context.WithCancel(context.Background())
	defer cancelCaller()
	started := make(chan struct{})
	workCanceled := make(chan struct{})
	submitDone := make(chan error, 1)
	go func() {
		submitDone <- worker.Submit(callerCtx, func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			close(workCanceled)
			return ctx.Err()
		})
	}()
	<-started

	stopDone := make(chan struct{})
	go func() {
		worker.Stop()
		close(stopDone)
	}()
	select {
	case <-stopDone:
	case <-time.After(100 * time.Millisecond):
		cancelCaller()
		<-stopDone
		t.Fatal("worker stop did not cancel the active operation")
	}
	<-workCanceled
	<-submitDone
}
