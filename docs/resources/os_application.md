---
page_title: UniFi OS Application (Resource)
description: |-
  Installs and controls a UniFi OS application.
---

# unifi_os_application (Resource)

Installs a console application and manages whether it runs. Installing,
starting and stopping use the private UniFi OS API and need a local admin login:
the provider's `username`/`password` or `UNIFI_USERNAME`/`UNIFI_PASSWORD`.
Without a login, `network` and `protect` are read through their integration APIs
with `api_key`, so plans and applies that change nothing need no admin login. An
application that does not answer reads as not running. Environment credentials
are not stored in state.

```terraform
resource "unifi_os_application" "protect" {
  name    = "protect"
  running = true

  lifecycle {
    prevent_destroy = true
  }
}
```

## Schema

- `name` (required string): `protect`, `network`, `access`, `talk`, `connect`,
  or `innerspace`. Must be supported by the console. Changing it replaces the
  resource.
- `running` (required boolean): desired running state; `false` stops the
  application without uninstalling it.
- `uninstall_on_destroy` (optional boolean): defaults to `false`. When `true`,
  destruction uninstalls the application and may delete its data.
- `id` (computed string): application name.
- `version` (computed string): installed version; upgrades are not managed.

Installation and lifecycle operations wait up to 15 minutes. Interrupted
installation can be resumed without reinstalling an application already being
installed. Removal from Terraform retains the application by default.

For Protect recordings, supported recording storage must be installed and
configured separately. This resource does not format disks.

Import using the application name:

```sh
terraform import unifi_os_application.protect protect
```
