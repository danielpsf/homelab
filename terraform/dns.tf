locals {
  dns_records = {
    root      = var.domain
    grafana   = "grafana.${var.domain}"
    pdf       = "pdf.${var.domain}"
    n8n       = "n8n.${var.domain}"
    portainer = "portainer.${var.domain}"
    wazuh     = "wazuh.${var.domain}"
    gotify    = "gotify.${var.domain}"
  }
}

resource "cloudflare_dns_record" "tunnel" {
  for_each = local.dns_records

  zone_id = var.cloudflare_zone_id
  name    = each.value
  type    = "CNAME"
  content = "${cloudflare_zero_trust_tunnel_cloudflared.homelab.id}.cfargotunnel.com"
  proxied = true
  ttl     = 1 # auto
}
