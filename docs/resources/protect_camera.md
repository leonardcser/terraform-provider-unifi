---
page_title: UniFi Protect Camera (Resource)
description: |-
  Adopts a camera into UniFi Protect and manages its name, recording mode and status LED.
---

# unifi_protect_camera (Resource)

Adoption, `recording_mode` changes and unadoption use the private Protect API and
need a local OS administrator login: the provider's `username`/`password` or
`UNIFI_USERNAME`/`UNIFI_PASSWORD`. Without a login, the provider reads and
updates the name and status LED of an adopted camera through the Protect
integration API with `api_key`; `recording_mode` and `host` then keep their last
known values, because that API does not expose them. Protect must be installed
and ready before provisioning.

```terraform
resource "unifi_protect_camera" "entrance" {
  mac            = "00:11:22:33:44:55"
  name           = "Entrance"
  bootstrap_host = "192.168.1.20"
  recording_mode = "always"

  depends_on = [unifi_os_application.protect]

  lifecycle {
    prevent_destroy = true
  }
}
```

## Schema

- `mac` (required string): colon-separated MAC address. Changing it replaces the
  resource.
- `name` (required string): camera display name.
- `recording_mode` (required string): `always`, `detections`, or `never`.
- `status_led` (optional boolean): enable or disable the camera status LED. When
  omitted, preserves the current setting. Does not change infrared illumination
  or other LED settings.
- `bootstrap_host` (optional string): initial camera IP. If Protect has not found
  the camera, the provider logs into its setup API, verifies the MAC, and points
  it at the console. Uses public factory credentials unless both
  `UNIFI_CAMERA_USERNAME` and `UNIFI_CAMERA_PASSWORD` are set. Camera credentials
  are never stored in resource state. Reserve the IP before provisioning.
- `unadopt_on_destroy` (optional boolean): defaults to `false`; enable explicitly
  to unadopt the camera on destruction.
- `id` (computed string): Protect camera ID.
- `host` (computed string): camera address reported by Protect.

Adoption and configuration wait up to 10 minutes. Already-adopted cameras on
this console can be managed without re-adoption. The provider never takes over
cameras owned by another console; direct bootstrap refuses already-adopted
cameras. Removal from Terraform retains camera adoption by default.

Recording requires available console storage. If Protect has recording disabled,
provisioning with `always` or `detections` fails with an actionable error; use
`never` for live view until storage is available. Bootstrap and storage setup
are distinct.

Import using the MAC:

```sh
terraform import unifi_protect_camera.entrance 00:11:22:33:44:55
```
