package unifi

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"
)

// consoleClient sends raw requests to a local console. Application management
// and the private Protect API need a local OS session. The Network API and the
// official Protect integration API use the API key when one is configured.
// Response bodies are never included in errors because they may contain credentials.
type consoleClient struct {
	baseURL       string
	username      string
	password      string
	apiKey        string
	http          *http.Client
	mu            sync.Mutex
	csrf          string
	authenticated bool
}

func newConsoleClient(baseURL, username, password string, insecure bool) *consoleClient {
	jar, _ := cookiejar.New(nil)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: insecure}
	return &consoleClient{baseURL: strings.TrimRight(baseURL, "/"), username: username, password: password,
		http: &http.Client{Transport: transport, Jar: jar, Timeout: 15 * time.Minute,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}
}

// hasSession reports whether local OS admin credentials are configured.
func (c *consoleClient) hasSession() bool {
	return c.username != "" && c.password != ""
}

func (c *consoleClient) request(ctx context.Context, method, endpoint string, payload, result any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.hasSession() {
		return fmt.Errorf("UniFi OS resources require a local admin username and password via provider configuration or UNIFI_USERNAME and UNIFI_PASSWORD")
	}
	for attempt := 0; attempt < 2; attempt++ {
		if !c.authenticated {
			status, err := c.send(ctx, http.MethodPost, "/api/auth/login", map[string]any{"username": c.username, "password": c.password, "rememberMe": false}, nil)
			if err != nil {
				return err
			}
			if status < 200 || status >= 300 {
				return fmt.Errorf("UniFi OS login returned HTTP %d", status)
			}
			c.authenticated = true
		}
		status, err := c.send(ctx, method, endpoint, payload, result)
		if err != nil {
			return err
		}
		if status == http.StatusUnauthorized && attempt == 0 {
			c.authenticated = false
			continue
		}
		if status < 200 || status >= 300 {
			return fmt.Errorf("UniFi OS %s %s returned HTTP %d", method, endpoint, status)
		}
		return nil
	}
	return fmt.Errorf("UniFi OS session authentication failed")
}

// network sends a Network API request for site and decodes the response data
// into result. It uses the API key when one is configured and the local OS
// session otherwise.
func (c *consoleClient) network(ctx context.Context, method, site, endpoint string, payload, result any) error {
	endpoint = "/proxy/network/api/s/" + url.PathEscape(site) + endpoint
	var body struct {
		Meta struct {
			RC  string `json:"rc"`
			Msg string `json:"msg"`
		} `json:"meta"`
		Data json.RawMessage `json:"data"`
	}
	if c.apiKey == "" {
		if err := c.request(ctx, method, endpoint, payload, &body); err != nil {
			return err
		}
	} else {
		status, err := c.keyRequest(ctx, method, endpoint, payload, &body)
		if err != nil {
			return err
		}
		if status < 200 || status >= 300 {
			return fmt.Errorf("UniFi Network %s %s returned HTTP %d", method, endpoint, status)
		}
	}
	if body.Meta.RC != "ok" {
		return fmt.Errorf("UniFi Network %s %s failed: %s", method, endpoint, body.Meta.Msg)
	}
	if result == nil {
		return nil
	}
	if err := json.Unmarshal(body.Data, result); err != nil {
		return fmt.Errorf("invalid data from UniFi Network %s", endpoint)
	}
	return nil
}

// keyRequest sends a request authenticated with the API key and returns the
// HTTP status. result is decoded only for successful responses.
func (c *consoleClient) keyRequest(ctx context.Context, method, endpoint string, payload, result any) (int, error) {
	if c.apiKey == "" {
		return 0, fmt.Errorf("this operation requires the provider api_key or local admin UNIFI_USERNAME and UNIFI_PASSWORD")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sendWith(ctx, method, endpoint, payload, result, c.apiKey)
}

func (c *consoleClient) send(ctx context.Context, method, endpoint string, payload, result any) (int, error) {
	return c.sendWith(ctx, method, endpoint, payload, result, "")
}

func (c *consoleClient) sendWith(ctx context.Context, method, endpoint string, payload, result any, apiKey string) (int, error) {
	// Installation actions may block until package installation completes.
	if !strings.HasPrefix(endpoint, "/api/controllers/") {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Minute)
		defer cancel()
	}
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return 0, fmt.Errorf("encode UniFi OS request: %w", err)
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+endpoint, body)
	if err != nil {
		return 0, fmt.Errorf("invalid UniFi OS endpoint")
	}
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.csrf != "" {
		req.Header.Set("X-CSRF-Token", c.csrf)
	}
	if apiKey != "" {
		req.Header.Set("X-Api-Key", apiKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("UniFi OS request failed: %w", err)
	}
	defer resp.Body.Close()
	if token := resp.Header.Get("X-CSRF-Token"); token != "" {
		c.csrf = token
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 && result != nil {
		if !strings.Contains(resp.Header.Get("Content-Type"), "application/json") {
			return resp.StatusCode, fmt.Errorf("UniFi OS %s is not serving JSON; verify the application is installed and ready", endpoint)
		}
		if err := json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(result); err != nil {
			return resp.StatusCode, fmt.Errorf("invalid JSON from UniFi OS %s", endpoint)
		}
	} else {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 32<<20))
	}
	return resp.StatusCode, nil
}

type consoleApplication struct {
	Name             string `json:"name"`
	Installed        bool   `json:"isInstalled"`
	Running          bool   `json:"isRunning"`
	Version          string `json:"version"`
	InstallState     string `json:"installState"`
	ControllerStatus string `json:"controllerStatus"`
}

func (c *consoleClient) application(ctx context.Context, name string) (*consoleApplication, error) {
	var apps struct {
		Controllers []consoleApplication `json:"controllers"`
	}
	if err := c.request(ctx, http.MethodGet, "/api/apps", nil, &apps); err != nil {
		return nil, err
	}
	for _, app := range apps.Controllers {
		if app.Name == name {
			return &app, nil
		}
	}
	return nil, fmt.Errorf("application %q is not supported by this UniFi OS console", name)
}

// applicationInfoEndpoints are official integration endpoints that answer with
// the application version only while the application is installed and running.
var applicationInfoEndpoints = map[string]string{
	"network": "/proxy/network/integration/v1/info",
	"protect": "/proxy/protect/integration/v1/meta/info",
}

// runningApplication reads an application through its integration API with the
// API key. It returns nil when the application does not answer, because it is
// stopped, not installed, or not ready yet.
func (c *consoleClient) runningApplication(ctx context.Context, name string) (*consoleApplication, error) {
	endpoint, ok := applicationInfoEndpoints[name]
	if !ok {
		return nil, fmt.Errorf("reading application %q requires local admin UNIFI_USERNAME and UNIFI_PASSWORD", name)
	}
	var info struct {
		Version string `json:"applicationVersion"`
	}
	status, err := c.keyRequest(ctx, http.MethodGet, endpoint, nil, &info)
	if err != nil {
		return nil, err
	}
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return nil, fmt.Errorf("UniFi OS rejected the API key for %s with HTTP %d", endpoint, status)
	case status >= 200 && status < 300 && info.Version != "":
		return &consoleApplication{Name: name, Installed: true, Running: true, Version: info.Version, ControllerStatus: "READY"}, nil
	default:
		return nil, nil
	}
}

func (c *consoleClient) applicationAction(ctx context.Context, name, action string) error {
	return c.request(ctx, http.MethodPost, "/api/controllers/"+url.PathEscape(name)+"/"+action, nil, nil)
}

func waitConsole(ctx context.Context, interval time.Duration, check func() (bool, error)) error {
	for {
		done, err := check()
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (c *consoleClient) ensureApplication(ctx context.Context, name string, running bool) (*consoleApplication, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	app, err := c.application(ctx, name)
	if err != nil {
		return nil, err
	}
	if !app.Installed {
		if app.InstallState != "installing" && app.InstallState != "install_pending" {
			if err := c.applicationAction(ctx, name, "install"); err != nil {
				return nil, err
			}
		}
		err = waitConsole(ctx, 5*time.Second, func() (bool, error) {
			var err error
			app, err = c.application(ctx, name)
			if err != nil {
				return false, err
			}
			if app.InstallState == "install_failed" {
				return false, fmt.Errorf("UniFi OS failed to install %s; check console storage and application logs", name)
			}
			return app.Installed, nil
		})
		if err != nil {
			return nil, err
		}
	}
	if app.Running != running {
		action := "disable"
		if running {
			action = "enable"
		}
		if err := c.applicationAction(ctx, name, action); err != nil {
			return nil, err
		}
	}
	err = waitConsole(ctx, 5*time.Second, func() (bool, error) {
		var err error
		app, err = c.application(ctx, name)
		if err != nil {
			return false, err
		}
		if app.InstallState == "start_failed" || app.InstallState == "stop_failed" {
			return false, fmt.Errorf("UniFi OS failed to change %s running state", name)
		}
		return app.Running == running && (!running || app.ControllerStatus == "READY"), nil
	})
	return app, err
}
