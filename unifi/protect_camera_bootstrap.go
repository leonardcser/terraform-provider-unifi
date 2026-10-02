package unifi

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
)

// bootstrapCamera points an unadopted camera at this console. Camera credentials
// are environment-only so provisioning does not retain them in Terraform state.
func (c *consoleClient) bootstrapCamera(ctx context.Context, host, mac string) error {
	if net.ParseIP(host) == nil {
		return fmt.Errorf("camera bootstrap_host must be an IP address")
	}
	consoleURL, err := url.Parse(c.baseURL)
	if err != nil || consoleURL.Hostname() == "" {
		return fmt.Errorf("invalid UniFi OS URL for camera bootstrap")
	}
	username, password := os.Getenv("UNIFI_CAMERA_USERNAME"), os.Getenv("UNIFI_CAMERA_PASSWORD")
	if (username == "") != (password == "") {
		return fmt.Errorf("set both UNIFI_CAMERA_USERNAME and UNIFI_CAMERA_PASSWORD, or neither for factory credentials")
	}
	if username == "" {
		username, password = "ubnt", "ubnt"
	}
	camera := newConsoleClient("https://"+net.JoinHostPort(host, "443"), "", "", true)
	return camera.setCameraController(ctx, consoleURL.Hostname(), mac, username, password)
}

func (camera *consoleClient) setCameraController(ctx context.Context, controllerHost, mac, username, password string) error {
	status, err := camera.send(ctx, http.MethodPost, "/api/1.1/login", map[string]string{"username": username, "password": password}, nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("camera bootstrap login returned HTTP %d; set UNIFI_CAMERA_USERNAME and UNIFI_CAMERA_PASSWORD for non-factory cameras", status)
	}
	var device struct {
		Board struct {
			MAC string `json:"mac"`
		} `json:"board"`
		Controller struct {
			Adopted bool   `json:"adopted"`
			Host    string `json:"host"`
		} `json:"controller"`
	}
	status, err = camera.send(ctx, http.MethodGet, "/api/1.1/status", nil, &device)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("camera status returned HTTP %d", status)
	}
	if normalizeProtectMAC(device.Board.MAC) != normalizeProtectMAC(mac) {
		return fmt.Errorf("camera bootstrap host does not match the configured MAC; refusing to provision")
	}
	if device.Controller.Adopted {
		return fmt.Errorf("camera is already adopted but absent from this console; refusing to change its Protect server")
	}
	if device.Controller.Host == controllerHost {
		return nil
	}
	status, err = camera.send(ctx, http.MethodPut, "/api/1.1/settings", map[string]any{"controller": map[string]string{"addr": controllerHost}}, nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("camera Protect server configuration returned HTTP %d", status)
	}
	return nil
}
