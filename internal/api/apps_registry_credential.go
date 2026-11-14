package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/GLINCKER/levelrail/internal/docker"
	"github.com/GLINCKER/levelrail/internal/store"
)

// appRegistryCredentialID resolves a body's credential name to its ID. set is false when
// no name was sent (keep the stored one) and true for "" (clear it). ok is false once a
// response has been written.
func (rt *Router) appRegistryCredentialID(w http.ResponseWriter, r *http.Request, app, image string, name *string) (id string, set, ok bool) {
	if name == nil {
		return "", false, true
	}
	if *name == "" {
		return "", true, true
	}
	cred, err := rt.registryCredentials.GetRegistryCredentialByName(r.Context(), *name)
	if errors.Is(err, store.ErrRegistryCredentialNotFound) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("registry_credential: no registry credential named %q", *name))
		return "", false, false
	}
	if err != nil {
		rt.internalError(w, "api: resolve app registry credential failed", err, slog.String("name", app), slog.String("registry_credential", *name))
		return "", false, false
	}
	if err := credentialHostMismatch(cred, image); err != nil {
		writeError(w, http.StatusBadRequest, "registry_credential: "+err.Error())
		return "", false, false
	}
	return cred.ID, true, true
}

// checkStoredCredentialHost refuses an image whose registry differs from the app's stored
// credential, so a changed image can never receive that registry's password. ok is false
// once a response has been written.
func (rt *Router) checkStoredCredentialHost(w http.ResponseWriter, ctx context.Context, app, credentialID, image string) bool {
	if credentialID == "" || image == "" {
		return true
	}
	cred, err := rt.registryCredentials.GetRegistryCredential(ctx, credentialID)
	if err != nil {
		rt.internalError(w, "api: load app registry credential failed", err, slog.String("name", app), slog.String("registry_credential_id", credentialID))
		return false
	}
	if err := credentialHostMismatch(cred, image); err != nil {
		writeError(w, http.StatusBadRequest, err.Error()+"; send registry_credential to change or clear it")
		return false
	}
	return true
}

func credentialHostMismatch(cred store.RegistryCredential, image string) error {
	imageHost := normalizeRegistryHost(docker.RegistryHost(image))
	if imageHost != "" && imageHost == normalizeRegistryHost(cred.RegistryHost) {
		return nil
	}
	return fmt.Errorf("image %q is not on registry credential %q's host %q", image, cred.Name, cred.RegistryHost)
}

func normalizeRegistryHost(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	h = strings.TrimPrefix(strings.TrimPrefix(h, "https://"), "http://")
	h = strings.TrimSuffix(h, "/")
	switch h {
	case "index.docker.io", "registry-1.docker.io", "registry.hub.docker.com":
		return "docker.io"
	}
	return h
}
