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

// consoleClient uses a local OS session independently of Network API-key authentication.
// Response bodies are never included in errors because they may contain credentials.
type consoleClient struct {
	baseURL       string
	username      string
	password      string
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

func (c *consoleClient) request(ctx context.Context, method, endpoint string, payload, result any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.username == "" || c.password == "" {
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

func (c *consoleClient) send(ctx context.Context, method, endpoint string, payload, result any) (int, error) {
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
