package obex

import (
	"context"
	"errors"
	"sync"

	"github.com/s0up4200/bluepost/internal/protocol"
)

var (
	ErrQueueFull     = errors.New("OBEX operation queue is full")
	ErrWorkerStopped = errors.New("OBEX worker is stopped")
)

type operation struct {
	ctx  context.Context
	work func(context.Context) error
	done chan error
}

type Worker struct {
	queue      chan operation
	slots      chan struct{}
	stop       chan struct{}
	done       chan struct{}
	stopCtx    context.Context
	cancelStop context.CancelFunc
	once       sync.Once
}

func NewWorker() *Worker {
	return newWorker(protocol.MaxOBEXOperations)
}

func newWorker(capacity int) *Worker {
	stopCtx, cancelStop := context.WithCancel(context.Background())
	worker := &Worker{
		queue:      make(chan operation, capacity),
		slots:      make(chan struct{}, capacity),
		stop:       make(chan struct{}),
		done:       make(chan struct{}),
		stopCtx:    stopCtx,
		cancelStop: cancelStop,
	}
	go worker.run()
	return worker
}

func (worker *Worker) Submit(ctx context.Context, work func(context.Context) error) error {
	if ctx == nil || work == nil {
		return errors.New("OBEX operation requires a context and function")
	}
	select {
	case worker.slots <- struct{}{}:
	default:
		return ErrQueueFull
	}
	request := operation{ctx: ctx, work: work, done: make(chan error, 1)}
	select {
	case worker.queue <- request:
	case <-ctx.Done():
		<-worker.slots
		return ctx.Err()
	case <-worker.stop:
		<-worker.slots
		return ErrWorkerStopped
	}
	select {
	case err := <-request.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-worker.stop:
		return ErrWorkerStopped
	}
}

func (worker *Worker) Stop() {
	worker.once.Do(func() {
		worker.cancelStop()
		close(worker.stop)
	})
	<-worker.done
}

func (worker *Worker) run() {
	defer close(worker.done)
	for {
		select {
		case <-worker.stop:
			worker.rejectQueued()
			return
		case request := <-worker.queue:
			callCtx, cancel := context.WithCancel(request.ctx)
			stopCancel := context.AfterFunc(worker.stopCtx, cancel)
			err := callCtx.Err()
			if err == nil {
				err = request.work(callCtx)
			}
			stopCancel()
			cancel()
			request.done <- err
			<-worker.slots
		}
	}
}

func (worker *Worker) rejectQueued() {
	for {
		select {
		case request := <-worker.queue:
			request.done <- ErrWorkerStopped
			<-worker.slots
		default:
			return
		}
	}
}
