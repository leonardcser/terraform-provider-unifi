---
page_title: "unifi_nat_rule Resource - terraform-provider-unifi"
subcategory: "Firewall"
description: |-
  Manages a custom UniFi NAT rule.
---

# unifi_nat_rule (Resource)

Manages a custom source NAT, destination NAT, or masquerade rule.

## Example Usage

```terraform
resource "unifi_nat_rule" "example" {
  description       = "VPN to upstream router"
  type              = "SNAT"
  protocol          = "tcp"
  ip_address        = "192.168.1.2"
  out_interface     = unifi_network.upstream.id
  setting_preference = "manual"

  source_filter = {
    filter_type    = "NETWORK_CONF"
    network_conf_id = unifi_vpn_server.admin.id
  }

  destination_filter = {
    filter_type = "ADDRESS_AND_PORT"
    address     = "192.168.1.1"
    port        = 443
  }
}
```

Import with the rule ID, or with `site:rule-id` when the rule is not in the provider's default site.
