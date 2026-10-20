# Resend sending domain for the poll's one-time code emails (tolon.<domain>).
# Copy the DKIM TXT, SPF TXT and bounce MX records from the Resend dashboard into
# resend_dns_records in terraform.tfvars, then apply. Empty map = no records.
resource "cloudflare_dns_record" "resend" {
  for_each = var.resend_dns_records

  zone_id  = var.cloudflare_zone_id
  name     = each.value.name
  type     = each.value.type
  content  = each.value.content
  priority = each.value.priority
  proxied  = false
  ttl      = 1 # auto
}
