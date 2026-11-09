package e2ee

import (
	"context"
	"errors"
	"sync"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// stateStore answers the crypto machine's questions about rooms by asking the
// homeserver, remembering the answers that cannot change (a room that is
// encrypted stays encrypted).
type stateStore struct {
	client *mautrix.Client

	mu        sync.Mutex
	encrypted map[id.RoomID]*event.EncryptionEventContent
}

func newStateStore(c *mautrix.Client) *stateStore {
	return &stateStore{client: c, encrypted: map[id.RoomID]*event.EncryptionEventContent{}}
}

func (s *stateStore) forget(room id.RoomID) {
	s.mu.Lock()
	delete(s.encrypted, room)
	s.mu.Unlock()
}

func (s *stateStore) GetEncryptionEvent(ctx context.Context, room id.RoomID) (*event.EncryptionEventContent, error) {
	s.mu.Lock()
	cached, ok := s.encrypted[room]
	s.mu.Unlock()
	if ok {
		return cached, nil
	}
	var content event.EncryptionEventContent
	if err := s.client.StateEvent(ctx, room, event.StateEncryption, "", &content); err != nil {
		if errors.Is(err, mautrix.MNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if content.Algorithm == "" {
		return nil, nil
	}
	s.mu.Lock()
	s.encrypted[room] = &content
	s.mu.Unlock()
	return &content, nil
}

func (s *stateStore) IsEncrypted(ctx context.Context, room id.RoomID) (bool, error) {
	content, err := s.GetEncryptionEvent(ctx, room)
	return content != nil, err
}

func (s *stateStore) GetHistoryVisibility(ctx context.Context, room id.RoomID) (*event.HistoryVisibilityEventContent, error) {
	var content event.HistoryVisibilityEventContent
	if err := s.client.StateEvent(ctx, room, event.StateHistoryVisibility, "", &content); err != nil {
		if errors.Is(err, mautrix.MNotFound) {
			return &event.HistoryVisibilityEventContent{HistoryVisibility: event.HistoryVisibilityShared}, nil
		}
		return nil, err
	}
	return &content, nil
}

// FindSharedRooms is only used to rotate keys when a user's devices change;
// the bridge rotates on membership events instead.
func (s *stateStore) FindSharedRooms(context.Context, id.UserID) ([]id.RoomID, error) {
	return nil, nil
}
