package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/GLINCKER/levelrail/internal/store"
)

func newAppRegistryCredentialRouter(t *testing.T) (*Router, *store.DB, *http.Cookie) {
	t.Helper()
	rt, db := newTestRouterWithRegistryCredentialSecrets(t, &fakeRegistryCredentialSecretsSetter{resolveValue: "token"})
	for _, c := range []store.RegistryCredential{
		{ID: "regcred_gitlab", Name: "gitlab", RegistryHost: "registry.gitlab.com", Username: "bot"},
		{ID: "regcred_ghcr", Name: "ghcr", RegistryHost: "ghcr.io", Username: "octo"},
	} {
		c.CreatedAt = time.Now().UTC().Format(time.RFC3339)
		if err := db.SaveRegistryCredential(context.Background(), c); err != nil {
			t.Fatalf("SaveRegistryCredential(%s) error = %v", c.Name, err)
		}
	}
	return rt, db, loginTestSession(t, rt, db)
}

func storedCredentialID(t *testing.T, db *store.DB, app string) string {
	t.Helper()
	svc, err := db.GetDesiredService(context.Background(), app)
	if err != nil {
		t.Fatalf("GetDesiredService(%s) error = %v", app, err)
	}
	return svc.RegistryCredentialID
}

func TestHandleCreateApp_RegistryCredential(t *testing.T) {
	rt, db, cookie := newAppRegistryCredentialRouter(t)

	rec := httptest.NewRecorder()
	body := `{"name":"web","image":"registry.gitlab.com/org/web:1.0","port":80,"registry_credential":"gitlab"}`
	rt.Handler().ServeHTTP(rec, authedRequest(t, cookie, http.MethodPost, "/api/v1/apps", body))
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got appResource
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.RegistryCredentialID != "regcred_gitlab" {
		t.Errorf("response registry_credential_id = %q, want regcred_gitlab", got.RegistryCredentialID)
	}
	if id := storedCredentialID(t, db, "web"); id != "regcred_gitlab" {
		t.Errorf("stored RegistryCredentialID = %q, want regcred_gitlab", id)
	}
}

func TestHandleCreateApp_UnknownRegistryCredential(t *testing.T) {
	rt, db, cookie := newAppRegistryCredentialRouter(t)

	rec := httptest.NewRecorder()
	body := `{"name":"web","image":"registry.gitlab.com/org/web:1.0","port":80,"registry_credential":"nope"}`
	rt.Handler().ServeHTTP(rec, authedRequest(t, cookie, http.MethodPost, "/api/v1/apps", body))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if _, err := db.GetDesiredService(context.Background(), "web"); err == nil {
		t.Error("app was created despite an unknown registry credential")
	}
}

func TestHandleUpdateApp_RegistryCredential(t *testing.T) {
	rt, db, cookie := newAppRegistryCredentialRouter(t)
	put := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		rt.Handler().ServeHTTP(rec, authedRequest(t, cookie, http.MethodPut, "/api/v1/apps/web", body))
		return rec
	}

	rec := httptest.NewRecorder()
	create := `{"name":"web","image":"registry.gitlab.com/org/web:1.0","port":80,"registry_credential":"gitlab"}`
	rt.Handler().ServeHTTP(rec, authedRequest(t, cookie, http.MethodPost, "/api/v1/apps", create))
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("create status = %d, body = %s", rec.Code, rec.Body.String())
	}

	steps := []struct {
		name, body string
		wantCode   int
		wantID     string
	}{
		{"omitted keeps", `{"replicas":2}`, http.StatusOK, "regcred_gitlab"},
		{"other host credential rejected", `{"registry_credential":"ghcr"}`, http.StatusBadRequest, "regcred_gitlab"},
		{"image moved off the credential's host rejected", `{"image":"ghcr.io/org/web:1.0"}`, http.StatusBadRequest, "regcred_gitlab"},
		{"image and credential switch together", `{"image":"ghcr.io/org/web:1.0","registry_credential":"ghcr"}`, http.StatusOK, "regcred_ghcr"},
		{"unknown rejected", `{"registry_credential":"nope"}`, http.StatusBadRequest, "regcred_ghcr"},
		{"empty clears", `{"registry_credential":""}`, http.StatusOK, ""},
		{"no credential, any host", `{"image":"docker.io/library/nginx:1.27"}`, http.StatusOK, ""},
	}
	for _, s := range steps {
		if rec := put(s.body); rec.Code != s.wantCode {
			t.Fatalf("%s: status = %d, want %d, body = %s", s.name, rec.Code, s.wantCode, rec.Body.String())
		}
		if id := storedCredentialID(t, db, "web"); id != s.wantID {
			t.Errorf("%s: stored RegistryCredentialID = %q, want %q", s.name, id, s.wantID)
		}
	}
}

func TestHandleCreateApp_RegistryCredentialHostMismatch(t *testing.T) {
	rt, db, cookie := newAppRegistryCredentialRouter(t)

	rec := httptest.NewRecorder()
	body := `{"name":"web","image":"attacker.example.com/web:1.0","port":80,"registry_credential":"gitlab"}`
	rt.Handler().ServeHTTP(rec, authedRequest(t, cookie, http.MethodPost, "/api/v1/apps", body))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if _, err := db.GetDesiredService(context.Background(), "web"); err == nil {
		t.Error("app was created with a credential for another registry")
	}
}

func TestHandleTriggerDeploy_RegistryCredentialHostMismatch(t *testing.T) {
	rt, _, cookie := newAppRegistryCredentialRouter(t)

	rec := httptest.NewRecorder()
	create := `{"name":"web","image":"registry.gitlab.com/org/web:1.0","port":80,"registry_credential":"gitlab"}`
	rt.Handler().ServeHTTP(rec, authedRequest(t, cookie, http.MethodPost, "/api/v1/apps", create))
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("create status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	rt.Handler().ServeHTTP(rec, authedRequest(t, cookie, http.MethodPost, "/api/v1/apps/web/deploys", `{"image":"attacker.example.com/web:2.0"}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("deploy status = %d, want %d, body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestCredentialHostMismatch(t *testing.T) {
	tests := []struct {
		credHost, image string
		wantMatch       bool
	}{
		{"registry.gitlab.com", "registry.gitlab.com/org/web:1.0", true},
		{"https://Registry.GitLab.com/", "registry.gitlab.com/org/web@sha256:" + strings.Repeat("a", 64), true},
		{"docker.io", "nginx:1.27", true},
		{"index.docker.io", "docker.io/library/nginx", true},
		{"registry.gitlab.com", "ghcr.io/org/web:1.0", false},
		{"registry.gitlab.com", "registry.gitlab.com.attacker.example/web:1.0", false},
		{"registry.gitlab.com", "not a reference", false},
	}
	for _, tt := range tests {
		err := credentialHostMismatch(store.RegistryCredential{Name: "c", RegistryHost: tt.credHost}, tt.image)
		if (err == nil) != tt.wantMatch {
			t.Errorf("credentialHostMismatch(%q, %q) error = %v, want match %v", tt.credHost, tt.image, err, tt.wantMatch)
		}
	}
}
