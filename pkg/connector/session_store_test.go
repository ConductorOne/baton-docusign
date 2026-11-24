package connector

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/conductorone/baton-sdk/pkg/types/sessions"
)

var _ sessions.SessionStore = (*fakeSessionStore)(nil)

// fakeSessionStore is an in-memory sessions.SessionStore that honors WithPrefix; the SDK
// has no standalone one. Errors are injected per operation and calls are counted.
type fakeSessionStore struct {
	values       map[string][]byte
	getManyCalls int
	setManyCalls int
	getManyErr   error
	setManyErr   error
	getErr       error
	setErr       error
}

func newFakeSessionStore() *fakeSessionStore {
	return &fakeSessionStore{values: map[string][]byte{}}
}

func (store *fakeSessionStore) namespacedKey(ctx context.Context, key string, opts []sessions.SessionStoreOption) (string, error) {
	bag := &sessions.SessionStoreBag{}
	for _, opt := range opts {
		if err := opt(ctx, bag); err != nil {
			return "", err
		}
	}
	return bag.Prefix + "/" + key, nil
}

// keysWithPrefix returns the sorted keys stored under prefix, without the prefix.
func (store *fakeSessionStore) keysWithPrefix(prefix string) []string {
	var keys []string
	for namespacedKey := range store.values {
		if key, found := strings.CutPrefix(namespacedKey, prefix+"/"); found {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	return keys
}

func (store *fakeSessionStore) Get(ctx context.Context, key string, opts ...sessions.SessionStoreOption) ([]byte, bool, error) {
	if store.getErr != nil {
		return nil, false, store.getErr
	}
	namespacedKey, err := store.namespacedKey(ctx, key, opts)
	if err != nil {
		return nil, false, err
	}
	value, found := store.values[namespacedKey]
	return value, found, nil
}

func (store *fakeSessionStore) GetMany(ctx context.Context, keys []string, opts ...sessions.SessionStoreOption) (map[string][]byte, []string, error) {
	store.getManyCalls++
	if store.getManyErr != nil {
		return nil, nil, store.getManyErr
	}
	found := make(map[string][]byte)
	for _, key := range keys {
		value, ok, err := store.Get(ctx, key, opts...)
		if err != nil {
			return nil, nil, err
		}
		if ok {
			found[key] = value
		}
	}
	return found, nil, nil
}

func (store *fakeSessionStore) Set(ctx context.Context, key string, value []byte, opts ...sessions.SessionStoreOption) error {
	if store.setErr != nil {
		return store.setErr
	}
	namespacedKey, err := store.namespacedKey(ctx, key, opts)
	if err != nil {
		return err
	}
	store.values[namespacedKey] = slices.Clone(value)
	return nil
}

func (store *fakeSessionStore) SetMany(ctx context.Context, values map[string][]byte, opts ...sessions.SessionStoreOption) error {
	store.setManyCalls++
	if store.setManyErr != nil {
		return store.setManyErr
	}
	for key, value := range values {
		if err := store.Set(ctx, key, value, opts...); err != nil {
			return err
		}
	}
	return nil
}

func (store *fakeSessionStore) Delete(ctx context.Context, key string, opts ...sessions.SessionStoreOption) error {
	namespacedKey, err := store.namespacedKey(ctx, key, opts)
	if err != nil {
		return err
	}
	delete(store.values, namespacedKey)
	return nil
}

func (store *fakeSessionStore) Clear(_ context.Context, _ ...sessions.SessionStoreOption) error {
	store.values = map[string][]byte{}
	return nil
}

func (store *fakeSessionStore) GetAll(_ context.Context, _ string, _ ...sessions.SessionStoreOption) (map[string][]byte, string, error) {
	return nil, "", errors.New("fakeSessionStore: GetAll is not supported")
}
