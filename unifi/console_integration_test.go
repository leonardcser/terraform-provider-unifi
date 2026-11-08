package unifi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// integrationServer serves the Protect and Network integration APIs and fails
// the test on any request that is not authenticated with the API key.
func integrationServer(t *testing.T, protectStatus int, camera *protectIntegrationCamera) (*httptest.Server, *[]map[string]any) {
	t.Helper()
	var patches []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "key" {
			t.Errorf("%s %s without API key", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/proxy/protect/integration/v1/meta/info":
			w.WriteHeader(protectStatus)
			fmt.Fprint(w, `{"applicationVersion":"7.3.74"}`)
		case r.URL.Path == "/proxy/network/integration/v1/info":
			fmt.Fprint(w, `{"applicationVersion":"10.6.109"}`)
		case r.URL.Path == protectIntegrationCameras && r.Method == http.MethodGet:
			cameras := []protectIntegrationCamera{}
			if camera != nil {
				cameras = append(cameras, *camera)
			}
			json.NewEncoder(w).Encode(cameras)
		case camera != nil && r.URL.Path == protectIntegrationCameras+"/"+camera.ID && r.Method == http.MethodPatch:
			var patch map[string]any
			json.NewDecoder(r.Body).Decode(&patch)
			patches = append(patches, patch)
			if name, ok := patch["name"].(string); ok {
				camera.Name = name
			}
			if led, ok := patch["ledSettings"].(map[string]any); ok {
				camera.LEDSettings.IsEnabled = led["isEnabled"].(bool)
			}
			json.NewEncoder(w).Encode(camera)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server, &patches
}

func keyOnlyConsole(url string) *consoleClient {
	c := newConsoleClient(url, "", "", false)
	c.apiKey = "key"
	return c
}

func TestRunningApplicationWithAPIKey(t *testing.T) {
	ctx := context.Background()
	server, _ := integrationServer(t, http.StatusOK, nil)
	c := keyOnlyConsole(server.URL)

	app, err := c.runningApplication(ctx, "protect")
	if err != nil || app == nil || !app.Running || app.Version != "7.3.74" {
		t.Fatalf("protect: %+v %v", app, err)
	}
	app, err = c.runningApplication(ctx, "network")
	if err != nil || app == nil || app.Version != "10.6.109" {
		t.Fatalf("network: %+v %v", app, err)
	}
	if _, err := c.runningApplication(ctx, "access"); err == nil {
		t.Fatal("read an application without an integration endpoint")
	}

	stopped, _ := integrationServer(t, http.StatusBadGateway, nil)
	app, err = keyOnlyConsole(stopped.URL).runningApplication(ctx, "protect")
	if err != nil || app != nil {
		t.Fatalf("stopped protect: %+v %v", app, err)
	}

	rejected, _ := integrationServer(t, http.StatusUnauthorized, nil)
	if _, err := keyOnlyConsole(rejected.URL).runningApplication(ctx, "protect"); err == nil {
		t.Fatal("accepted a rejected API key")
	}
}

func TestUpdateIntegrationCamera(t *testing.T) {
	ctx := context.Background()
	camera := &protectIntegrationCamera{ID: "cam", MAC: "58D61FD1C8D4", Name: "Old"}
	camera.LEDSettings.IsEnabled = true
	server, patches := integrationServer(t, http.StatusOK, camera)
	c := keyOnlyConsole(server.URL)
	off := false

	found, err := c.integrationCamera(ctx, "58:d6:1f:d1:c8:d4")
	if err != nil || found == nil || found.ID != "cam" {
		t.Fatalf("lookup: %+v %v", found, err)
	}
	updated, err := c.updateIntegrationCamera(ctx, "58:d6:1f:d1:c8:d4", "New", &off)
	if err != nil || updated.Name != "New" || updated.LEDSettings.IsEnabled {
		t.Fatalf("update: %+v %v", updated, err)
	}
	if _, err := c.updateIntegrationCamera(ctx, "58:d6:1f:d1:c8:d4", "New", &off); err != nil {
		t.Fatal(err)
	}
	if len(*patches) != 1 {
		t.Fatalf("converged camera was patched again: %v", *patches)
	}
	if _, err := c.updateIntegrationCamera(ctx, "00:00:00:00:00:01", "New", nil); err == nil {
		t.Fatal("updated a camera that is not adopted")
	}
}

func TestKeyRequestWithoutAPIKey(t *testing.T) {
	c := newConsoleClient("https://127.0.0.1:1", "", "", false)
	if _, err := c.runningApplication(context.Background(), "protect"); err == nil {
		t.Fatal("sent a request without credentials")
	}
}
