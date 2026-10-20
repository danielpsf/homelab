# Edge rate limit for the poll host (free plan: 1 rule, 10 s period, 10 s block).
# Needs the API token permission "Zone > Zone WAF > Edit"; enable with
# enable_tolon_rate_limit = true once the token has it.
resource "cloudflare_ruleset" "tolon_rate_limit" {
  count = var.enable_tolon_rate_limit ? 1 : 0

  zone_id     = var.cloudflare_zone_id
  name        = "Tolon poll rate limit"
  description = "Per-IP request limit for the Tolon poll"
  kind        = "zone"
  phase       = "http_ratelimit"

  rules = [
    {
      ref         = "tolon_poll_per_ip"
      description = "20 requests per 10 s per IP on the poll host"
      expression  = "(http.host eq \"tolon.${var.domain}\")"
      action      = "block"
      ratelimit = {
        characteristics     = ["cf.colo.id", "ip.src"]
        period              = 10
        requests_per_period = 20
        mitigation_timeout  = 10
      }
    }
  ]
}
