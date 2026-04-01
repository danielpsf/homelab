locals {
  access_apps = {
    homepage  = { subdomain = "",           service_name = "Homepage" }
    grafana   = { subdomain = "grafana.",   service_name = "Grafana" }
    pdf       = { subdomain = "pdf.",       service_name = "Stirling PDF" }
    n8n       = { subdomain = "n8n.",       service_name = "n8n" }
    portainer = { subdomain = "portainer.", service_name = "Portainer" }
  }
}

resource "cloudflare_zero_trust_access_application" "apps" {
  for_each = local.access_apps

  account_id       = var.cloudflare_account_id
  name             = each.value.service_name
  domain           = "${each.value.subdomain}${var.domain}"
  type             = "self_hosted"
  session_duration = "24h"

  policies = [
    {
      name     = "Allow admin email OTP"
      decision = "allow"
      include = [
        {
          email = {
            email = var.admin_email
          }
        }
      ]
    }
  ]
}
