---
page_title: "unifi_setting_wifi_speed Resource - terraform-provider-unifi"
subcategory: "Settings"
description: |-
  Manages the site-level Default WiFi Speeds preset and channel widths.
---

# unifi_setting_wifi_speed (Resource)

Manages the site-level Default WiFi Speeds preset, its per-band channel widths,
and Extended 5 GHz Spectrum setting. Access-point radio resources can use the
same widths to implement the console's Apply to All APs action declaratively.

## Example Usage

```terraform
resource "unifi_setting_wifi_speed" "maximum" {
  preset                  = "maximum_speed"
  width_2_4_ghz           = 20
  width_5_ghz             = 80
  width_6_ghz             = 320
  extended_5_ghz_spectrum = true
}
```

## Schema

### Required

- `preset` (String) Default WiFi Speeds preset: `maximum_speed`, `conservative`, or `custom`.
- `width_2_4_ghz` (Number) 2.4 GHz channel width: `20` or `40` MHz.
- `width_5_ghz` (Number) 5 GHz channel width: `20`, `40`, `80`, or `160` MHz.
- `width_6_ghz` (Number) 6 GHz channel width: `20`, `40`, `80`, `160`, or `320` MHz.
- `extended_5_ghz_spectrum` (Boolean) Enables DFS channels in the Extended 5 GHz Spectrum.

### Optional

- `site` (String) UniFi site name. Defaults to the provider site.

### Read-Only

- `id` (String) Controller setting identifier.

## Import

Import using the site name:

```shell
terraform import unifi_setting_wifi_speed.maximum default
```
