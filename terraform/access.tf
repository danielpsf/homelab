locals {
  access_apps = {
    homepage  = { subdomain = "home.", service_name = "Homepage" }
    grafana   = { subdomain = "grafana.", service_name = "Grafana" }
    pdf       = { subdomain = "pdf.", service_name = "Stirling PDF" }
    n8n       = { subdomain = "n8n.", service_name = "n8n" }
    portainer = { subdomain = "portainer.", service_name = "Portainer" }
    wazuh     = { subdomain = "wazuh.", service_name = "Wazuh" }
  }
}

resource "cloudflare_zero_trust_access_application" "apps" {
  for_each = local.access_apps

  account_id       = var.cloudflare_account_id
  name             = each.value.service_name
  domain           = "${each.value.subdomain}${var.domain}"
  type             = "self_hosted"
  session_duration = "24h"

  # Grafana also admits the MCP client by service token (non_identity), ahead of the email policy.
  policies = concat(
    each.key == "grafana" && var.grafana_mcp_service_token_id != "" ? [
      {
        name     = "allow-mcp"
        decision = "non_identity"
        include  = [{ service_token = { token_id = var.grafana_mcp_service_token_id } }]
      }
    ] : [],
    [
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
  )
}

