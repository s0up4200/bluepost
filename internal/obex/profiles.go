package obex

import (
	"context"
	"errors"

	"github.com/s0up4200/bluepost/internal/model"
)

var ErrConnectionLost = errors.New("OBEX connection was lost")

type Profiles struct {
	sessions *Sessions
	mapAPI   *MAP
	pbapAPI  *PBAP
	worker   *Worker
}

func NewProfiles(sessions *Sessions, mapAPI *MAP, pbapAPI *PBAP, worker *Worker) *Profiles {
	return &Profiles{sessions: sessions, mapAPI: mapAPI, pbapAPI: pbapAPI, worker: worker}
}

func (profiles *Profiles) Open(ctx context.Context, phone string) error {
	return profiles.worker.Submit(ctx, func(callCtx context.Context) error {
		return profiles.sessions.Open(callCtx, phone)
	})
}

func (profiles *Profiles) Watch(
	ctx context.Context,
	onMessage func(model.Message) error,
) error {
	watchCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	errorsChannel := make(chan error, 2)
	lost := make(chan struct{}, 1)
	go func() {
		errorsChannel <- profiles.mapAPI.Watch(watchCtx, onMessage)
	}()
	go func() {
		errorsChannel <- profiles.sessions.Monitor(watchCtx, func(string) {
			select {
			case lost <- struct{}{}:
			default:
			}
		})
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-lost:
		_ = profiles.sessions.close(context.Background(), false)
		return ErrConnectionLost
	case err := <-errorsChannel:
		if errors.Is(err, context.Canceled) && ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
}

func (profiles *Profiles) ListRecent(
	ctx context.Context,
	folder string,
	limit uint32,
) ([]model.Message, error) {
	var messages []model.Message
	err := profiles.worker.Submit(ctx, func(callCtx context.Context) error {
		var callErr error
		messages, callErr = profiles.mapAPI.ListRecent(callCtx, folder, limit)
		return callErr
	})
	return messages, err
}

func (profiles *Profiles) RefreshMAP(ctx context.Context) error {
	return profiles.worker.Submit(ctx, func(callCtx context.Context) error {
		return profiles.sessions.RefreshMAP(callCtx)
	})
}

func (profiles *Profiles) SyncContacts(ctx context.Context) ([]model.Contact, error) {
	var contacts []model.Contact
	err := profiles.worker.Submit(ctx, func(callCtx context.Context) error {
		var callErr error
		contacts, callErr = profiles.pbapAPI.Sync(callCtx)
		return callErr
	})
	return contacts, err
}

func (profiles *Profiles) Health() (bool, bool) {
	_, mapReady := profiles.sessions.MapPath()
	_, pbapReady := profiles.sessions.PBAPPath()
	return mapReady, pbapReady
}

func (profiles *Profiles) Close(ctx context.Context, removeRemote bool) error {
	return profiles.worker.Submit(ctx, func(callCtx context.Context) error {
		return profiles.sessions.close(callCtx, removeRemote)
	})
}
