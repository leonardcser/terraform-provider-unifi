---
page_title: Setting Ether Lighting (Resource)
description: |-
  Manages the site-level Etherlighting color palette.
---

# unifi_setting_ether_lighting (Resource)

Manages network and link-speed colors for switches with Etherlighting ports.

```terraform
resource "unifi_setting_ether_lighting" "palette" {
  network_overrides = [
    { network_id = unifi_network.home.id, color_hex = "059FFF" },
  ]
  speed_overrides = []
}
```

Import using the site name, for example `default`.
