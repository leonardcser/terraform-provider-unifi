---
page_title: Setting Locale (Resource)
description: |-
  Manages the UniFi site timezone.
---

# unifi_setting_locale (Resource)

```terraform
resource "unifi_setting_locale" "site" {
  timezone = "Europe/Zurich"
}
```

Import using the site name, for example `default`.
