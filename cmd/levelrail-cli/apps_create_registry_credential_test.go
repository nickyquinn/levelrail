package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestRun_AppsCreateFileSendsRegistryCredential(t *testing.T) {
	tests := []struct {
		name, build string
		want        *string
	}{
		{"named", "      registryCredential: gitlab\n", stringPtrOrNil("gitlab")},
		{"unset", "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "app.yaml")
			yaml := "version: 1\nservices:\n  web:\n    build:\n      type: image\n      image: registry.gitlab.com/org/web:1.0\n" + tt.build + "    port: 80\n"
			if err := os.WriteFile(file, []byte(yaml), 0o600); err != nil {
				t.Fatal(err)
			}
			var created *appResource
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost && r.URL.Path == "/api/v1/apps" {
					created = &appResource{}
					_ = json.NewDecoder(r.Body).Decode(created)
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(appResource{Name: "web", Image: "registry.gitlab.com/org/web:1.0", Port: 80})
			}))
			defer srv.Close()

			var stdout, stderr bytes.Buffer
			if got := run("levelrail-cli-test", []string{"apps", "create", "--file", file, "--api-url", srv.URL, "--json"}, &stdout, &stderr, envMap()); got != exitOK {
				t.Fatalf("exit = %d (stdout=%q stderr=%q)", got, stdout.String(), stderr.String())
			}
			if created == nil {
				t.Fatal("no POST /api/v1/apps was sent")
			}
			switch {
			case tt.want == nil && created.RegistryCredential != nil:
				t.Errorf("registry_credential = %q, want it omitted", *created.RegistryCredential)
			case tt.want != nil && (created.RegistryCredential == nil || *created.RegistryCredential != *tt.want):
				t.Errorf("registry_credential = %v, want %q", created.RegistryCredential, *tt.want)
			}
		})
	}
}
