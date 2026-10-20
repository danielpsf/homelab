# Turnstile widget for the poll's email code form. Needs the API token permission
# "Account > Turnstile > Edit"; enable with enable_tolon_turnstile = true once the token has it.
resource "cloudflare_turnstile_widget" "tolon" {
  count = var.enable_tolon_turnstile ? 1 : 0

  account_id = var.cloudflare_account_id
  name       = "Tolon Poll"
  domains    = ["tolon.${var.domain}"]
  mode       = "managed"
}
