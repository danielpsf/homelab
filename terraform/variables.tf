variable "cloudflare_api_token" {
  description = "Cloudflare API token"
  type        = string
  sensitive   = true
}

variable "cloudflare_account_id" {
  description = "Cloudflare account ID"
  type        = string
}

variable "cloudflare_zone_id" {
  description = "Cloudflare zone ID"
  type        = string
}

variable "domain" {
  description = "Root domain name"
  type        = string
}

variable "admin_email" {
  description = "Email address for Zero Trust OTP authentication"
  type        = string
}

variable "enable_tolon_rate_limit" {
  description = "Create the Tolon poll edge rate limiting rule (token needs Zone WAF Edit)"
  type        = bool
  default     = false
}

variable "enable_tolon_turnstile" {
  description = "Create the Tolon poll Turnstile widget (token needs Turnstile Edit)"
  type        = bool
  default     = false
}

variable "resend_dns_records" {
  description = "Resend sending-domain DNS records (full names), keyed by a short label"
  type = map(object({
    name     = string
    type     = string
    content  = string
    priority = optional(number)
  }))
  default = {}
}

variable "enable_tolon_contact" {
  description = "Enable Email Routing and forward the Tolon poll contact address to admin_email (token needs Email Routing Edit)"
  type        = bool
  default     = false
}
