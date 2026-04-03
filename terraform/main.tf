resource "random_id" "tunnel_secret" {
  byte_length = 32
}

resource "cloudflare_zero_trust_tunnel_cloudflared" "homelab" {
  account_id    = var.cloudflare_account_id
  name          = "homelab"
  tunnel_secret = random_id.tunnel_secret.b64_std
  config_src    = "cloudflare"
}

resource "cloudflare_zero_trust_tunnel_cloudflared_config" "homelab" {
  account_id = var.cloudflare_account_id
  tunnel_id  = cloudflare_zero_trust_tunnel_cloudflared.homelab.id

  config = {
    ingress = [
      {
        hostname = var.domain
        service  = "http://homepage:3000"
      },
      {
        hostname = "grafana.${var.domain}"
        service  = "http://grafana:3000"
      },
      {
        hostname = "pdf.${var.domain}"
        service  = "http://stirling-pdf:8080"
      },
      {
        hostname = "n8n.${var.domain}"
        service  = "http://n8n:5678"
      },
      {
        hostname = "portainer.${var.domain}"
        service  = "http://portainer:9000"
      },
      {
        hostname = "wazuh.${var.domain}"
        service  = "https://wazuh-dashboard:443"
        origin_request = {
          no_tls_verify = true
        }
      },
      {
        # catch-all rule (required by Cloudflare)
        service = "http_status:404"
      }
    ]
  }
}
