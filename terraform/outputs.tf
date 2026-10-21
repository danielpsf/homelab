output "tunnel_token" {
  description = "Cloudflare Tunnel token — set as CLOUDFLARE_TUNNEL_TOKEN in .env"
  value       = base64encode(jsonencode({
    a = var.cloudflare_account_id
    t = cloudflare_zero_trust_tunnel_cloudflared.homelab.id
    s = random_id.tunnel_secret.b64_std
  }))
  sensitive = true
}

output "tunnel_id" {
  description = "Cloudflare Tunnel ID"
  value       = cloudflare_zero_trust_tunnel_cloudflared.homelab.id
}
