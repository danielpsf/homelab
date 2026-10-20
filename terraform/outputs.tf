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

output "tolon_admin_aud" {
  description = "Access AUD tag of the Tolon poll results app — set as TOLON_POLL_ACCESS_AUD in .env"
  value       = cloudflare_zero_trust_access_application.tolon_admin.aud
}

output "tolon_turnstile_sitekey" {
  description = "Turnstile sitekey — set as TURNSTILE_SITEKEY in .env"
  value       = one(cloudflare_turnstile_widget.tolon[*].sitekey)
}

output "tolon_turnstile_secret" {
  description = "Turnstile secret — set as TURNSTILE_SECRET in .env"
  value       = one(cloudflare_turnstile_widget.tolon[*].secret)
  sensitive   = true
}
