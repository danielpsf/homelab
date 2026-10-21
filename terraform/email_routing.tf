# Contact address tolon@<domain> forwards to admin_email (kept after the poll closed, for data deletion requests).
# Needs the API token permissions "Zone > Email Routing Rules > Edit" and
# "Account > Email Routing Addresses > Edit"; enable with enable_tolon_contact = true.
# Enabling Email Routing adds Cloudflare's MX and SPF records at the zone apex.
resource "cloudflare_email_routing_settings" "zone" {
  count   = var.enable_tolon_contact ? 1 : 0
  zone_id = var.cloudflare_zone_id
}

resource "cloudflare_email_routing_dns" "zone" {
  count      = var.enable_tolon_contact ? 1 : 0
  zone_id    = var.cloudflare_zone_id
  depends_on = [cloudflare_email_routing_settings.zone]
}

resource "cloudflare_email_routing_address" "admin" {
  count      = var.enable_tolon_contact ? 1 : 0
  account_id = var.cloudflare_account_id
  email      = var.admin_email
}

resource "cloudflare_email_routing_rule" "tolon_contact" {
  count   = var.enable_tolon_contact ? 1 : 0
  zone_id = var.cloudflare_zone_id
  name    = "Tolon poll contact"
  enabled = true

  matchers = [
    { type = "literal", field = "to", value = "tolon@${var.domain}" }
  ]
  actions = [
    { type = "forward", value = [cloudflare_email_routing_address.admin[0].email] }
  ]
}
