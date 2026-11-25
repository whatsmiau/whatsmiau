package controllers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/puzpuzpuz/xsync/v4"
	"github.com/verbeux-ai/whatsmiau/env"
	"github.com/verbeux-ai/whatsmiau/interfaces"
	"github.com/verbeux-ai/whatsmiau/lib/whatsmiau"
	"github.com/verbeux-ai/whatsmiau/models"
	"github.com/verbeux-ai/whatsmiau/server/dto"
	"github.com/verbeux-ai/whatsmiau/server/middleware"
	goWhatsmeow "go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store"
)

// cachedInstance returns a core with just the instance cache wired, so handlers
// that invalidate the cache can run without standing up Redis or WhatsApp.
func cachedInstance(t *testing.T) *whatsmiau.Whatsmiau {
	t.Helper()
	core := &whatsmiau.Whatsmiau{}
	setUnexportedField(t, core, "instanceCache", reflect.ValueOf(xsync.NewMap[string, models.Instance]()))
	return core
}

func serveInstanceCreate(t *testing.T, repo interfaces.InstanceRepository, body string) *httptest.ResponseRecorder {
	t.Helper()
	// The create handler only touches the repository unless a migration is
	// present, so a nil core is enough to exercise webhook validation.
	controller := NewInstances(repo, nil)
	app := echo.New()
	app.POST("/v1/instance/create", controller.Create)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/instance/create", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	app.ServeHTTP(rec, req)
	return rec
}

func serveInstanceUpdate(t *testing.T, repo interfaces.InstanceRepository, body string) *httptest.ResponseRecorder {
	t.Helper()
	controller := NewInstances(repo, cachedInstance(t))
	app := echo.New()
	app.PUT("/v1/instance/update/:id", controller.Update)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/v1/instance/update/inst", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	app.ServeHTTP(rec, req)
	return rec
}

func serveInstanceList(t *testing.T, repo interfaces.InstanceRepository) *httptest.ResponseRecorder {
	t.Helper()
	controller := NewInstances(repo, nil)
	app := echo.New()
	app.GET("/v1/instance/fetchInstances", controller.List)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/instance/fetchInstances?instanceName=inst", nil)
	app.ServeHTTP(rec, req)
	return rec
}

func TestBatchConnectionStatesOmitsMissingIDsWithOneBulkLookup(t *testing.T) {
	repo := newInMemoryInstanceRepo(models.Instance{ID: "closed"})
	core := &whatsmiau.Whatsmiau{}
	clients := xsync.NewMap[string, *goWhatsmeow.Client]()
	clients.Store("removed", goWhatsmeow.NewClient(&store.Device{}, nil))
	setUnexportedField(t, core, "clients", reflect.ValueOf(clients))
	setUnexportedField(t, core, "qrCache", reflect.ValueOf(xsync.NewMap[string, string]()))
	controller := NewInstances(repo, core)
	app := echo.New()
	previousAPIKey := env.Env.ApiKey
	env.Env.ApiKey = "batch-status-test-key"
	t.Cleanup(func() { env.Env.ApiKey = previousAPIKey })
	v1 := app.Group("/v1", middleware.Simplify(middleware.Auth))
	v1.GET("/instance/connectionState/:id", controller.Status)
	v1.POST("/instance/connectionStates", controller.BatchConnectionStates)

	for _, id := range []string{"absent", "removed"} {
		individualRec := httptest.NewRecorder()
		individualReq := httptest.NewRequest(http.MethodGet, "/v1/instance/connectionState/"+id, nil)
		individualReq.Header.Set("apikey", env.Env.ApiKey)
		app.ServeHTTP(individualRec, individualReq)
		if individualRec.Code != http.StatusNotFound {
			t.Fatalf("individual status for %q = %d, want %d (body=%s)", id, individualRec.Code, http.StatusNotFound, individualRec.Body.String())
		}
	}

	closedRec := httptest.NewRecorder()
	closedReq := httptest.NewRequest(http.MethodGet, "/v1/instance/connectionState/closed", nil)
	closedReq.Header.Set("apikey", env.Env.ApiKey)
	app.ServeHTTP(closedRec, closedReq)
	if closedRec.Code != http.StatusOK {
		t.Fatalf("individual status for existing closed instance = %d, want %d (body=%s)", closedRec.Code, http.StatusOK, closedRec.Body.String())
	}
	var individualResponse dto.StatusInstanceResponse
	if err := json.Unmarshal(closedRec.Body.Bytes(), &individualResponse); err != nil {
		t.Fatalf("decode individual response: %v", err)
	}
	if individualResponse.Status != "closed" {
		t.Fatalf("individual state = %q, want closed", individualResponse.Status)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/instance/connectionStates", strings.NewReader(`{"instanceNames":["closed","absent","removed","closed"]}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("apikey", env.Env.ApiKey)
	listCallsBeforeBatch := repo.listCalls
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body=%s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	var response dto.BatchConnectionStatesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := response.ConnectionStates["closed"]; got != dto.BatchConnectionStateClosed {
		t.Fatalf("closed state = %q, want %q", got, dto.BatchConnectionStateClosed)
	}
	if _, exists := response.ConnectionStates["absent"]; exists {
		t.Fatal("missing instance was reported as a valid closed session")
	}
	if len(response.ConnectionStates) != 1 {
		t.Fatalf("returned %d connection states, want only the existing instance", len(response.ConnectionStates))
	}
	if repo.existingIDsCalls != 1 {
		t.Fatalf("bulk existence lookups = %d, want 1", repo.existingIDsCalls)
	}
	if got := repo.listCalls - listCallsBeforeBatch; got != 0 {
		t.Fatalf("batch request made %d List lookups, want 0", got)
	}
}

func TestBatchConnectionStatesReturnsStorageFailure(t *testing.T) {
	core := &whatsmiau.Whatsmiau{}
	setUnexportedField(t, core, "clients", reflect.ValueOf(xsync.NewMap[string, *goWhatsmeow.Client]()))
	controller := NewInstances(failingInstanceRepo{err: errors.New("redis unavailable")}, core)
	app := echo.New()
	app.POST("/v1/instance/connectionStates", controller.BatchConnectionStates)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/instance/connectionStates", strings.NewReader(`{"instanceNames":["known"]}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d (body=%s)", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
}

type instanceRepositoryWithoutExistence struct {
	interfaces.InstanceRepository
}

func TestBatchConnectionStatesRequiresBulkExistenceCapability(t *testing.T) {
	repo := instanceRepositoryWithoutExistence{InstanceRepository: newInMemoryInstanceRepo(models.Instance{ID: "known"})}
	controller := NewInstances(repo, nil)
	app := echo.New()
	app.POST("/v1/instance/connectionStates", controller.BatchConnectionStates)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/instance/connectionStates", strings.NewReader(`{"instanceNames":["known"]}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want %d (body=%s)", rec.Code, http.StatusNotImplemented, rec.Body.String())
	}
}

func TestCreateInstanceRejectsHeadersOverPlainHTTP(t *testing.T) {
	repo := newInMemoryInstanceRepo()
	rec := serveInstanceCreate(t, repo,
		`{"instanceName":"inst","webhook":{"url":"http://internal-host/hook","headers":{"Authorization":"Bearer secret"}}}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (body=%s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestCreateInstanceRejectsInvalidHeaderName(t *testing.T) {
	repo := newInMemoryInstanceRepo()
	rec := serveInstanceCreate(t, repo,
		`{"instanceName":"inst","webhook":{"url":"https://example.com/hook","headers":{"Bad Name":"x"}}}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (body=%s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestCreateInstanceAcceptsValidHTTPSWebhook(t *testing.T) {
	repo := newInMemoryInstanceRepo()
	rec := serveInstanceCreate(t, repo,
		`{"instanceName":"inst","webhook":{"url":"https://example.com/hook","headers":{"Authorization":"Bearer secret"}}}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d (body=%s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	if got := repo.byID["inst"].Webhook.Headers["Authorization"]; got != "Bearer secret" {
		t.Fatalf("headers not persisted: %q", got)
	}
}

func TestCreateInstanceWithoutWebhookStillWorks(t *testing.T) {
	repo := newInMemoryInstanceRepo()
	rec := serveInstanceCreate(t, repo, `{"instanceName":"inst"}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d (body=%s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
}

func TestCreateInstanceWithPlainHTTPWebhookWithoutHeadersStillWorks(t *testing.T) {
	repo := newInMemoryInstanceRepo()
	rec := serveInstanceCreate(t, repo, `{"instanceName":"inst","webhook":{"url":"http://example.com/hook"}}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d (body=%s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
}

func TestUpdateInstanceRejectsURLDowngradeWithStoredHeaders(t *testing.T) {
	repo := newInMemoryInstanceRepo(models.Instance{
		ID:      "inst",
		Webhook: models.InstanceWebhook{Url: "https://example.com/hook", Headers: map[string]string{"Authorization": "Bearer secret"}},
	})
	// The request does not carry headers; they come from storage, so the
	// resulting pair must still be rejected.
	rec := serveInstanceUpdate(t, repo, `{"webhook":{"url":"http://example.com/hook"}}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (body=%s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestUpdateInstanceAllowsHTTPSWithStoredHeaders(t *testing.T) {
	repo := newInMemoryInstanceRepo(models.Instance{
		ID:      "inst",
		Webhook: models.InstanceWebhook{Url: "https://example.com/hook", Headers: map[string]string{"Authorization": "Bearer secret"}},
	})
	rec := serveInstanceUpdate(t, repo, `{"webhook":{"url":"https://other.example/hook"}}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d (body=%s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
}

func TestUpdateInstanceAllowsPlainHTTPWithoutStoredHeaders(t *testing.T) {
	repo := newInMemoryInstanceRepo(models.Instance{ID: "inst"})
	rec := serveInstanceUpdate(t, repo, `{"webhook":{"url":"http://example.com/hook"}}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d (body=%s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
}

func TestUpdateInstanceWithoutURLKeepsLegacyInvalidRowUpdatable(t *testing.T) {
	// A row stored before the transport rule (http + headers) must not become
	// impossible to update: changing an unrelated field leaves the pair as it
	// already was and makes nothing worse.
	repo := newInMemoryInstanceRepo(models.Instance{
		ID:      "inst",
		Webhook: models.InstanceWebhook{Url: "http://example.com/hook", Headers: map[string]string{"Authorization": "Bearer secret"}},
	})
	rec := serveInstanceUpdate(t, repo, `{"groupsIgnore":true}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d (body=%s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
}

func TestListInstancesHidesWebhookHeaders(t *testing.T) {
	repo := newInMemoryInstanceRepo(models.Instance{
		ID:      "inst",
		Webhook: models.InstanceWebhook{Url: "https://example.com/hook", Headers: map[string]string{"Authorization": "Bearer secret"}},
	})
	rec := serveInstanceList(t, repo)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body=%s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "Bearer secret") {
		t.Fatalf("listing must not leak webhook headers: %s", rec.Body.String())
	}
}

func boolPtr(value bool) *bool {
	return &value
}

func TestUpdateInstancePreservesStoredBase64(t *testing.T) {
	repo := newInMemoryInstanceRepo(models.Instance{
		ID:      "inst",
		Webhook: models.InstanceWebhook{Url: "https://example.com/hook", Base64: boolPtr(true)},
	})
	rec := serveInstanceUpdate(t, repo, `{"groupsIgnore":true}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d (body=%s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	if got := repo.byID["inst"].Webhook.Base64; got == nil || !*got {
		t.Fatalf("an unrelated update must not reset webhook.base64: %v", got)
	}
}

func TestUpdateInstanceCanDisableBase64(t *testing.T) {
	repo := newInMemoryInstanceRepo(models.Instance{
		ID:      "inst",
		Webhook: models.InstanceWebhook{Url: "https://example.com/hook", Base64: boolPtr(true)},
	})
	rec := serveInstanceUpdate(t, repo, `{"webhook":{"base64":false}}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d (body=%s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	if got := repo.byID["inst"].Webhook.Base64; got == nil || *got {
		t.Fatalf("explicit base64=false must persist: %v", got)
	}
}
