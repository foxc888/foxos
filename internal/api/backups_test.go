package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/foxc888/foxos/internal/backup"
	"github.com/foxc888/foxos/internal/confirmation"
	"github.com/foxc888/foxos/internal/domain"
)

type backupAPIService struct {
	preview  backup.Preview
	restores int
}

func (s *backupAPIService) Create(context.Context, string) (backup.Manifest, error) {
	return s.preview.Manifest, nil
}
func (s *backupAPIService) List() ([]backup.Manifest, error) {
	return []backup.Manifest{s.preview.Manifest}, nil
}
func (s *backupAPIService) Inspect(string) (backup.Preview, error) { return s.preview, nil }
func (s *backupAPIService) Restore(context.Context, string, string) error {
	s.restores++
	return nil
}

func TestBackupRestorePlanQueuesDigestBoundJob(t *testing.T) {
	const apiToken = "01234567890123456789012345678901"
	service := &backupAPIService{preview: backup.Preview{Manifest: backup.Manifest{ID: "backup-a", Database: "database.sqlite", FileCount: 1, Checksums: map[string]string{"database.sqlite": strings.Repeat("a", 64)}}, Digest: strings.Repeat("b", 64)}}
	app, _ := New(&memoryNodes{nodes: map[string]domain.Node{}}, apiToken)
	signer, _ := confirmation.New([]byte("abcdefghijklmnopqrstuvwxyz012345"))
	jobs := &fakeEgressJobs{}
	audit := &fakeAudit{}
	mux := http.NewServeMux()
	app.RegisterBackups(mux, service, signer, acceptingReplayStore{}, jobs, audit)

	planRequest := httptest.NewRequest(http.MethodPost, "/api/v1/backups/backup-a/restore/plan", nil)
	planRequest.Header.Set("Authorization", "Bearer "+apiToken)
	planResponse := httptest.NewRecorder()
	mux.ServeHTTP(planResponse, planRequest)
	var plan struct {
		ConfirmationToken string `json:"confirmationToken"`
	}
	if planResponse.Code != http.StatusOK || json.Unmarshal(planResponse.Body.Bytes(), &plan) != nil {
		t.Fatalf("status=%d body=%s", planResponse.Code, planResponse.Body.String())
	}
	body, _ := json.Marshal(map[string]string{"confirmationToken": plan.ConfirmationToken})
	restoreRequest := httptest.NewRequest(http.MethodPost, "/api/v1/backups/backup-a/restore", strings.NewReader(string(body)))
	restoreRequest.Header.Set("Authorization", "Bearer "+apiToken)
	restoreResponse := httptest.NewRecorder()
	mux.ServeHTTP(restoreResponse, restoreRequest)
	if restoreResponse.Code != http.StatusAccepted || jobs.calls != 1 || service.restores != 0 || jobs.request["digest"] != service.preview.Digest {
		t.Fatalf("status=%d body=%s jobs=%+v restores=%d", restoreResponse.Code, restoreResponse.Body.String(), jobs, service.restores)
	}
}

func TestBackupRestoreRejectsManifestChangedAfterPlan(t *testing.T) {
	const apiToken = "01234567890123456789012345678901"
	service := &backupAPIService{preview: backup.Preview{Manifest: backup.Manifest{ID: "backup-a", Database: "database.sqlite", FileCount: 1}, Digest: strings.Repeat("b", 64)}}
	app, _ := New(&memoryNodes{nodes: map[string]domain.Node{}}, apiToken)
	signer, _ := confirmation.New([]byte("abcdefghijklmnopqrstuvwxyz012345"))
	jobs := &fakeEgressJobs{}
	mux := http.NewServeMux()
	app.RegisterBackups(mux, service, signer, acceptingReplayStore{}, jobs, &fakeAudit{})
	planRequest := httptest.NewRequest(http.MethodPost, "/api/v1/backups/backup-a/restore/plan", nil)
	planRequest.Header.Set("Authorization", "Bearer "+apiToken)
	planResponse := httptest.NewRecorder()
	mux.ServeHTTP(planResponse, planRequest)
	var plan struct {
		ConfirmationToken string `json:"confirmationToken"`
	}
	_ = json.Unmarshal(planResponse.Body.Bytes(), &plan)
	service.preview.Digest = strings.Repeat("c", 64)
	body, _ := json.Marshal(map[string]string{"confirmationToken": plan.ConfirmationToken})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/backups/backup-a/restore", strings.NewReader(string(body)))
	request.Header.Set("Authorization", "Bearer "+apiToken)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusConflict || jobs.calls != 0 {
		t.Fatalf("status=%d body=%s jobs=%d", response.Code, response.Body.String(), jobs.calls)
	}
}
