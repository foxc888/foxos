package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/foxc888/foxos/internal/confirmation"
	"github.com/foxc888/foxos/internal/domain"
	storepkg "github.com/foxc888/foxos/internal/store/sqlite"
	"github.com/foxc888/foxos/internal/subscription"
)

type subscriptionAPIFetcher struct {
	result subscription.Result
}

func (f *subscriptionAPIFetcher) Fetch(context.Context, string) (subscription.Result, error) {
	return f.result, nil
}

func TestSubscriptionPreviewSignsPlanAndUpdateQueuesJob(t *testing.T) {
	const apiToken = "01234567890123456789012345678901"
	store, err := storepkg.Open(filepath.Join(t.TempDir(), "foxos.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	item := domain.Subscription{ID: "source-a", Name: "Primary", URL: "https://example.com/source?access=fixture", Enabled: true, Interval: 3600}
	if err := store.SaveSubscription(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	link := (&url.URL{Scheme: "vless", User: url.User("fixture-uuid"), Host: "example.com:443", Fragment: "East"}).String()
	fetcher := &subscriptionAPIFetcher{result: subscription.Result{Digest: strings.Repeat("a", 64), Body: []byte(link)}}
	app, err := New(&memoryNodes{nodes: map[string]domain.Node{}}, apiToken)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := confirmation.New([]byte("abcdefghijklmnopqrstuvwxyz012345"))
	if err != nil {
		t.Fatal(err)
	}
	jobs := &fakeEgressJobs{}
	audit := &fakeAudit{}
	mux := http.NewServeMux()
	app.RegisterSubscriptions(mux, store, store, fetcher, signer, store, jobs, audit)

	previewRequest := httptest.NewRequest(http.MethodPost, "/api/v1/subscriptions/source-a/preview", nil)
	previewRequest.Header.Set("Authorization", "Bearer "+apiToken)
	previewResponse := httptest.NewRecorder()
	mux.ServeHTTP(previewResponse, previewRequest)
	if previewResponse.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", previewResponse.Code, previewResponse.Body.String())
	}
	var preview struct {
		Plan              subscription.UpdatePlan `json:"plan"`
		ConfirmationToken string                  `json:"confirmationToken"`
	}
	if err := json.Unmarshal(previewResponse.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if preview.Plan.NodeCount != 1 || preview.ConfirmationToken == "" {
		t.Fatalf("preview=%+v", preview)
	}
	body, _ := json.Marshal(map[string]any{"plan": preview.Plan, "confirmationToken": preview.ConfirmationToken})
	updateRequest := httptest.NewRequest(http.MethodPost, "/api/v1/subscriptions/source-a/update", strings.NewReader(string(body)))
	updateRequest.Header.Set("Authorization", "Bearer "+apiToken)
	updateResponse := httptest.NewRecorder()
	mux.ServeHTTP(updateResponse, updateRequest)
	if updateResponse.Code != http.StatusAccepted || jobs.calls != 1 || jobs.request["subscriptionId"] != "source-a" {
		t.Fatalf("status=%d body=%s jobs=%+v", updateResponse.Code, updateResponse.Body.String(), jobs)
	}
	listRequest := httptest.NewRequest(http.MethodGet, "/api/v1/subscriptions", nil)
	listRequest.Header.Set("Authorization", "Bearer "+apiToken)
	listResponse := httptest.NewRecorder()
	mux.ServeHTTP(listResponse, listRequest)
	if strings.Contains(listResponse.Body.String(), "access=fixture") || !strings.Contains(listResponse.Body.String(), "redacted") {
		t.Fatalf("subscription URL was not redacted: %s", listResponse.Body.String())
	}
}

func TestSubscriptionDeleteRequiresSignedCurrentImpact(t *testing.T) {
	const apiToken = "01234567890123456789012345678901"
	store, err := storepkg.Open(filepath.Join(t.TempDir(), "foxos.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	item := domain.Subscription{ID: "source-a", Name: "Primary", URL: "https://example.com/source", Enabled: false, Interval: 3600}
	if err := store.SaveSubscription(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	node := domain.Node{ID: "sub-fixture", Name: "Primary / Node", Type: "vless", Server: "example.com", Port: 443, UUID: "fixture-uuid", SubscriptionID: item.ID}
	if err := store.ReplaceSubscriptionNodes(context.Background(), item.ID, []domain.Node{node}); err != nil {
		t.Fatal(err)
	}
	app, _ := New(&memoryNodes{nodes: map[string]domain.Node{}}, apiToken)
	signer, _ := confirmation.New([]byte("abcdefghijklmnopqrstuvwxyz012345"))
	mux := http.NewServeMux()
	app.RegisterSubscriptions(mux, store, store, nil, signer, store, nil, &fakeAudit{})

	direct := httptest.NewRequest(http.MethodDelete, "/api/v1/subscriptions/source-a", nil)
	direct.Header.Set("Authorization", "Bearer "+apiToken)
	directResponse := httptest.NewRecorder()
	mux.ServeHTTP(directResponse, direct)
	if directResponse.Code != http.StatusConflict {
		t.Fatalf("direct delete status=%d", directResponse.Code)
	}
	planRequest := httptest.NewRequest(http.MethodPost, "/api/v1/subscriptions/source-a/delete/plan", nil)
	planRequest.Header.Set("Authorization", "Bearer "+apiToken)
	planResponse := httptest.NewRecorder()
	mux.ServeHTTP(planResponse, planRequest)
	var plan struct {
		ConfirmationToken string `json:"confirmationToken"`
	}
	if planResponse.Code != http.StatusOK || json.Unmarshal(planResponse.Body.Bytes(), &plan) != nil {
		t.Fatalf("status=%d body=%s", planResponse.Code, planResponse.Body.String())
	}
	deleteBody, _ := json.Marshal(map[string]string{"confirmationToken": plan.ConfirmationToken})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/subscriptions/source-a/delete", strings.NewReader(string(deleteBody)))
	request.Header.Set("Authorization", "Bearer "+apiToken)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if _, err := store.Subscription(context.Background(), item.ID); !errors.Is(err, storepkg.ErrNotFound) {
		t.Fatalf("subscription err=%v", err)
	}
}
