package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/foxc888/foxos/internal/domain"
	storepkg "github.com/foxc888/foxos/internal/store/sqlite"
)

type memoryGroups struct {
	items     map[string]domain.Group
	updateErr error
	deleteErr error
}

func (m *memoryGroups) SaveGroup(_ context.Context, group domain.Group) error {
	m.items[group.ID] = group
	return nil
}
func (m *memoryGroups) CreateGroup(_ context.Context, group domain.Group) error {
	if _, exists := m.items[group.ID]; exists {
		return errors.New("group already exists")
	}
	m.items[group.ID] = group
	return nil
}
func (m *memoryGroups) UpdateGroup(_ context.Context, group domain.Group) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	if _, exists := m.items[group.ID]; !exists {
		return storepkg.ErrNotFound
	}
	m.items[group.ID] = group
	return nil
}
func (m *memoryGroups) Group(_ context.Context, id string) (domain.Group, error) {
	group, found := m.items[id]
	if !found {
		return domain.Group{}, storepkg.ErrNotFound
	}
	return group, nil
}
func (m *memoryGroups) Groups(context.Context) ([]domain.Group, error) { return nil, nil }
func (m *memoryGroups) DeleteGroup(_ context.Context, id string) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	delete(m.items, id)
	return nil
}

func TestProxyGroupReferenceConflictsUseHTTP409(t *testing.T) {
	t.Parallel()
	const token = "01234567890123456789012345678901"
	referenceErr := &storepkg.ReferenceError{Resource: "proxy group", References: []string{"device-policy:phone"}}
	tests := []struct {
		name       string
		method     string
		body       string
		configure  func(*memoryGroups)
		wantStored bool
	}{
		{name: "update", method: http.MethodPut, body: `{"name":"Changed","type":"select","nodeIds":["node-a"]}`, configure: func(store *memoryGroups) { store.updateErr = referenceErr }, wantStored: true},
		{name: "delete", method: http.MethodDelete, configure: func(store *memoryGroups) { store.deleteErr = referenceErr }, wantStored: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			store := &memoryGroups{items: map[string]domain.Group{"group-1": {ID: "group-1", Name: "Group", Type: "select", NodeIDs: []string{"node-a"}}}}
			test.configure(store)
			app, err := New(&memoryNodes{nodes: map[string]domain.Node{}}, token)
			if err != nil {
				t.Fatal(err)
			}
			mux := http.NewServeMux()
			app.RegisterGroups(mux, store)
			request := httptest.NewRequest(test.method, "/api/v1/proxy-groups/group-1", strings.NewReader(test.body))
			request.Header.Set("Authorization", "Bearer "+token)
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, request)
			if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "device-policy:phone") || !strings.Contains(response.Body.String(), "group_in_use") {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			_, stored := store.items["group-1"]
			if stored != test.wantStored {
				t.Fatalf("stored=%t, want %t", stored, test.wantStored)
			}
		})
	}
}

func TestProxyGroupIDsAreServerGeneratedAndPutCannotCreate(t *testing.T) {
	t.Parallel()
	const token = "01234567890123456789012345678901"
	store := &memoryGroups{items: map[string]domain.Group{}}
	app, err := New(&memoryNodes{nodes: map[string]domain.Node{}}, token)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	app.RegisterGroups(mux, store)

	create := httptest.NewRequest(http.MethodPost, "/api/v1/proxy-groups", strings.NewReader(`{"id":"client-id","name":"Group","type":"select","nodeIds":["node-a"]}`))
	create.Header.Set("Authorization", "Bearer "+token)
	createResponse := httptest.NewRecorder()
	mux.ServeHTTP(createResponse, create)
	if createResponse.Code != http.StatusUnprocessableEntity || len(store.items) != 0 {
		t.Fatalf("create status=%d items=%+v", createResponse.Code, store.items)
	}

	update := httptest.NewRequest(http.MethodPut, "/api/v1/proxy-groups/missing", strings.NewReader(`{"name":"Missing","type":"select","nodeIds":["node-a"]}`))
	update.Header.Set("Authorization", "Bearer "+token)
	updateResponse := httptest.NewRecorder()
	mux.ServeHTTP(updateResponse, update)
	if updateResponse.Code != http.StatusNotFound || len(store.items) != 0 {
		t.Fatalf("update status=%d items=%+v", updateResponse.Code, store.items)
	}
}
