package unifi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProtectCameraAdoption(t *testing.T) {
	camera := protectCamera{ID: "camera-1", MAC: "58D61FD1C8D4", Host: "10.10.1.250", CanAdopt: true}
	adopts, patches := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/auth/login":
			fmt.Fprint(w, `{}`)
		case "/proxy/protect/api/bootstrap":
			json.NewEncoder(w).Encode(map[string]any{"cameras": []protectCamera{camera}})
		case "/proxy/protect/api/devices/adopt":
			if r.Method != "POST" {
				t.Error("wrong adoption method")
			}
			var body struct {
				Cameras map[string]any `json:"cameras"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if _, ok := body.Cameras[camera.ID]; !ok {
				t.Error("missing camera ID")
			}
			adopts++
			camera.Adopted = true
			fmt.Fprint(w, `{"cameras":{"camera-1":{"adopted":true}}}`)
		case "/proxy/protect/api/cameras/camera-1":
			if r.Method != "PATCH" {
				t.Error("wrong update method")
			}
			var update protectCamera
			if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
				t.Error(err)
			}
			patches++
			camera.Name = update.Name
			camera.RecordingSettings.Mode = update.RecordingSettings.Mode
			fmt.Fprint(w, `{}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	c := newConsoleClient(server.URL, "admin", "secret", false)
	got, err := c.ensureCamera(context.Background(), "58:d6:1f:d1:c8:d4", "G5 Turret Ultra", "always", "")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Adopted || got.Name != "G5 Turret Ultra" || got.RecordingSettings.Mode != "always" {
		t.Fatal("camera did not converge")
	}
	if _, err := c.ensureCamera(context.Background(), "58:d6:1f:d1:c8:d4", "G5 Turret Ultra", "always", ""); err != nil {
		t.Fatal(err)
	}
	if adopts != 1 || patches != 1 {
		t.Fatalf("not idempotent: adopts=%d patches=%d", adopts, patches)
	}
	if _, err := c.ensureCamera(context.Background(), "58:d6:1f:d1:c8:d4", "Entrance", "detections", ""); err != nil {
		t.Fatal(err)
	}
	if adopts != 1 || patches != 2 {
		t.Fatal("update readopted camera")
	}
}

func TestProtectCameraRejectsForeignOwnership(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/auth/login" {
			fmt.Fprint(w, `{}`)
			return
		}
		if r.Method != "GET" {
			t.Error("mutated foreign-owned camera")
		}
		fmt.Fprint(w, `{"cameras":[{"id":"camera-1","mac":"58D61FD1C8D4","isAdopted":true,"isAdoptedByOther":true}]}`)
	}))
	defer server.Close()
	c := newConsoleClient(server.URL, "admin", "secret", false)
	if _, err := c.ensureCamera(context.Background(), "58:d6:1f:d1:c8:d4", "G5", "always", ""); err == nil {
		t.Fatal("accepted foreign ownership")
	}
}

func TestProtectCameraRecordingWithoutStorage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/auth/login" {
			fmt.Fprint(w, `{}`)
			return
		}
		if r.Method != "GET" {
			t.Error("mutated camera while recording is disabled")
		}
		fmt.Fprint(w, `{"nvr":{"isRecordingDisabled":true},"cameras":[{"id":"camera-1","mac":"58D61FD1C8D4","name":"G5","isAdopted":true,"recordingSettings":{"mode":"never"}}]}`)
	}))
	defer server.Close()
	c := newConsoleClient(server.URL, "admin", "secret", false)
	if _, err := c.ensureCamera(context.Background(), "58:d6:1f:d1:c8:d4", "G5", "always", ""); err == nil {
		t.Fatal("accepted recording without storage")
	}
	if _, err := c.ensureCamera(context.Background(), "58:d6:1f:d1:c8:d4", "G5", "never", ""); err != nil {
		t.Fatal(err)
	}
}

func TestProtectCameraBootstrap(t *testing.T) {
	for _, tc := range []struct {
		name, mac          string
		adopted, wantError bool
	}{
		{name: "factory", mac: "58D61FD1C8D4"},
		{name: "wrong device", mac: "112233445566", wantError: true},
		{name: "already adopted", mac: "58D61FD1C8D4", adopted: true, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writes := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/1.1/login":
					fmt.Fprint(w, `{}`)
				case "/api/1.1/status":
					json.NewEncoder(w).Encode(map[string]any{"board": map[string]string{"mac": tc.mac}, "controller": map[string]any{"adopted": tc.adopted, "host": ""}})
				case "/api/1.1/settings":
					if r.Method != "PUT" {
						t.Error("wrong setup method")
					}
					var body struct {
						Controller struct {
							Addr string `json:"addr"`
						} `json:"controller"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if body.Controller.Addr != "10.10.90.1" {
						t.Error("wrong controller address")
					}
					writes++
					fmt.Fprint(w, `{}`)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			c := newConsoleClient(server.URL, "", "", false)
			err := c.setCameraController(context.Background(), "10.10.90.1", "58:d6:1f:d1:c8:d4", "ubnt", "ubnt")
			if (err != nil) != tc.wantError {
				t.Fatalf("bootstrap error: %v", err)
			}
			if tc.wantError && writes != 0 {
				t.Fatal("mutated mismatched or adopted camera")
			}
			if !tc.wantError && writes != 1 {
				t.Fatal("did not set controller")
			}
		})
	}
}
