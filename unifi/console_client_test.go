package unifi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestConsoleApplicationInstall(t *testing.T) {
	installed, running := false, false
	actions := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/auth/login":
			w.Header().Set("X-CSRF-Token", "csrf")
			http.SetCookie(w, &http.Cookie{Name: "TOKEN", Value: "session", Path: "/"})
			fmt.Fprint(w, `{}`)
		case "/api/apps":
			if _, err := r.Cookie("TOKEN"); err != nil {
				t.Error("missing session cookie")
			}
			json.NewEncoder(w).Encode(map[string]any{"controllers": []consoleApplication{{Name: "protect", Installed: installed, Running: running, ControllerStatus: "READY", Version: "1.0"}}})
		case "/api/controllers/protect/install", "/api/controllers/protect/enable", "/api/controllers/protect/disable":
			if r.Method != "POST" || r.Header.Get("X-CSRF-Token") != "csrf" {
				t.Error("missing mutation authentication")
			}
			action := strings.TrimPrefix(r.URL.Path, "/api/controllers/protect/")
			actions = append(actions, action)
			switch action {
			case "install":
				installed = true
			case "enable":
				running = true
			case "disable":
				running = false
			}
			fmt.Fprint(w, `{}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	c := newConsoleClient(server.URL, "admin", "secret", false)
	app, err := c.ensureApplication(context.Background(), "protect", true)
	if err != nil || !app.Running {
		t.Fatalf("install: %v", err)
	}
	if strings.Join(actions, ",") != "install,enable" {
		t.Fatalf("actions: %v", actions)
	}
	if _, err := c.ensureApplication(context.Background(), "protect", true); err != nil {
		t.Fatal(err)
	}
	if len(actions) != 2 {
		t.Fatal("idempotent apply mutated the console")
	}
	if _, err := c.ensureApplication(context.Background(), "protect", false); err != nil {
		t.Fatal(err)
	}
	if actions[2] != "disable" {
		t.Fatalf("actions: %v", actions)
	}
}

func TestConsoleSessionRenewalAndRedaction(t *testing.T) {
	logins, reads := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/auth/login" {
			logins++
			fmt.Fprint(w, `{}`)
			return
		}
		reads++
		if reads == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"password":"do-not-log"}`)
			return
		}
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"password":"do-not-log"}`)
	}))
	defer server.Close()
	c := newConsoleClient(server.URL, "admin", "secret", false)
	err := c.request(context.Background(), "GET", "/api/apps", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "403") || strings.Contains(err.Error(), "do-not-log") {
		t.Fatalf("unsafe error: %v", err)
	}
	if logins != 2 {
		t.Fatalf("logins: %d", logins)
	}
}

func TestConsoleRejectsHTMLAndMissingCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/auth/login" {
			fmt.Fprint(w, `{}`)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html>console</html>")
	}))
	defer server.Close()
	c := newConsoleClient(server.URL, "admin", "secret", false)
	if _, err := c.application(context.Background(), "protect"); err == nil {
		t.Fatal("accepted HTML as application state")
	}
	c = newConsoleClient(server.URL, "", "", false)
	if _, err := c.application(context.Background(), "protect"); err == nil {
		t.Fatal("accepted missing OS credentials")
	}
}

func TestConsoleNetworkUsesAPIKey(t *testing.T) {
	var bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/auth/login" {
			t.Error("API-key request opened an OS session")
		}
		if r.URL.Path != "/proxy/network/api/s/default/rest/portconf/abc" || r.Header.Get("X-Api-Key") != "key" {
			t.Errorf("request %s %s without API key", r.Method, r.URL.Path)
		}
		if r.Method == http.MethodPut {
			b, _ := io.ReadAll(r.Body)
			bodies = append(bodies, string(b))
			if strings.Contains(string(b), "auto") {
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprint(w, `{"meta":{"rc":"error","msg":"api.err.InvalidPayload"},"data":[]}`)
				return
			}
			fmt.Fprint(w, `{"meta":{"rc":"ok"},"data":[]}`)
			return
		}
		fmt.Fprint(w, `{"meta":{"rc":"ok"},"data":[{"_id":"abc","stp_edge_state":"enabled"}]}`)
	}))
	defer server.Close()
	c := newConsoleClient(server.URL, "", "", false)
	c.apiKey = "key"
	r := &portProfileResource{client: &Client{console: c}}
	ctx := context.Background()

	edge, err := r.readSTPEdgeState(ctx, "default", "abc")
	if err != nil || edge.ValueString() != "enabled" {
		t.Fatalf("read: %v %v", edge, err)
	}
	if err := r.writeSTPEdgeState(ctx, "default", "abc", types.StringValue("disabled")); err != nil {
		t.Fatal(err)
	}
	if err := r.writeSTPEdgeState(ctx, "default", "abc", types.StringNull()); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 1 || bodies[0] != `{"stp_edge_state":"disabled"}` {
		t.Fatalf("bodies: %v", bodies)
	}
	if err := r.writeSTPEdgeState(ctx, "default", "abc", types.StringValue("auto")); err == nil {
		t.Fatal("accepted a rejected payload")
	}
}

func TestConsoleNetworkRejectsErrorMeta(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"meta":{"rc":"error","msg":"api.err.NoSiteContext"},"data":[]}`)
	}))
	defer server.Close()
	c := newConsoleClient(server.URL, "", "", false)
	c.apiKey = "key"
	err := c.network(context.Background(), http.MethodGet, "default", "/rest/portconf", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "api.err.NoSiteContext") {
		t.Fatalf("error: %v", err)
	}
}

func TestConsoleWaitCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitConsole(ctx, time.Hour, func() (bool, error) { return false, nil }); err != context.Canceled {
		t.Fatalf("cancellation: %v", err)
	}
}
