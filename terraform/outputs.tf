output "tunnel_token" {
  description = "Cloudflare Tunnel token — set as CLOUDFLARE_TUNNEL_TOKEN in .env"
  value       = cloudflare_zero_trust_tunnel_cloudflared.homelab.tunnel_token
  sensitive   = true
}

output "tunnel_id" {
  description = "Cloudflare Tunnel ID"
  value       = cloudflare_zero_trust_tunnel_cloudflared.homelab.id
}
