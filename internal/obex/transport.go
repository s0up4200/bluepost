package obex

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

type Transport interface {
	Call(context.Context, string, dbus.ObjectPath, string, ...any) ([]any, error)
	Subscribe(context.Context, ...dbus.MatchOption) (<-chan *dbus.Signal, func(), error)
}

type DBusTransport struct {
	Conn *dbus.Conn
}

func (transport DBusTransport) Call(
	ctx context.Context,
	destination string,
	path dbus.ObjectPath,
	method string,
	args ...any,
) ([]any, error) {
	if transport.Conn == nil {
		return nil, errors.New("D-Bus connection is not available")
	}
	call := transport.Conn.Object(destination, path).CallWithContext(ctx, method, 0, args...)
	if call.Err != nil {
		return nil, call.Err
	}
	return call.Body, nil
}

func (transport DBusTransport) Subscribe(
	ctx context.Context,
	options ...dbus.MatchOption,
) (<-chan *dbus.Signal, func(), error) {
	if transport.Conn == nil {
		return nil, nil, errors.New("D-Bus connection is not available")
	}
	channel := make(chan *dbus.Signal, 64)
	transport.Conn.Signal(channel)
	if err := transport.Conn.AddMatchSignalContext(ctx, options...); err != nil {
		transport.Conn.RemoveSignal(channel)
		return nil, nil, err
	}
	var once sync.Once
	cancel := func() {
		once.Do(func() {
			removeCtx, stop := context.WithTimeout(context.Background(), 2*time.Second)
			_ = transport.Conn.RemoveMatchSignalContext(removeCtx, options...)
			stop()
			transport.Conn.RemoveSignal(channel)
		})
	}
	go func() {
		<-ctx.Done()
		cancel()
	}()
	return channel, cancel, nil
}
