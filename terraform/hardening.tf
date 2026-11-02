# Zone hardening for the public sites (danielpsf.net, www, tolon). Free plan.

resource "cloudflare_zone_dnssec" "zone" {
  zone_id = var.cloudflare_zone_id
  status  = "active"
}

resource "cloudflare_zone_setting" "min_tls_version" {
  zone_id    = var.cloudflare_zone_id
  setting_id = "min_tls_version"
  value      = "1.2"
}

resource "cloudflare_zone_setting" "always_use_https" {
  zone_id    = var.cloudflare_zone_id
  setting_id = "always_use_https"
  value      = "on"
}

resource "cloudflare_zone_setting" "always_online" {
  zone_id    = var.cloudflare_zone_id
  setting_id = "always_online"
  value      = "on"
}

# No mail is sent from this domain (Email Routing only receives), so spoofed mail is rejected.
resource "cloudflare_dns_record" "dmarc" {
  zone_id = var.cloudflare_zone_id
  name    = "_dmarc.${var.domain}"
  type    = "TXT"
  content = "\"v=DMARC1; p=reject; sp=reject; adkim=s; aspf=s\""
  ttl     = 1
}

# Only the CAs Cloudflare Universal SSL uses may issue certificates.
locals {
  caa = {
    le      = "letsencrypt.org"
    google  = "pki.goog; cansignhttpexchanges=yes"
    sslcom  = "ssl.com"
    sectigo = "sectigo.com"
  }
}

resource "cloudflare_dns_record" "caa" {
  for_each = local.caa
  zone_id  = var.cloudflare_zone_id
  name     = var.domain
  type     = "CAA"
  ttl      = 1
  data = {
    flags = 0
    tag   = "issue"
    value = each.value
  }
}

resource "cloudflare_dns_record" "caa_iodef" {
  zone_id = var.cloudflare_zone_id
  name    = var.domain
  type    = "CAA"
  ttl     = 1
  data = {
    flags = 0
    tag   = "iodef"
    value = "mailto:${var.admin_email}"
  }
}

# Cloudflare Free Managed Ruleset (not deployed by default).
resource "cloudflare_ruleset" "waf_managed" {
  zone_id = var.cloudflare_zone_id
  name    = "Managed WAF"
  kind    = "zone"
  phase   = "http_request_firewall_managed"
  rules = [
    {
      ref         = "free_managed"
      description = "Cloudflare Free Managed Ruleset"
      expression  = "true"
      action      = "execute"
      action_parameters = {
        id = "77454fe2d30c4220b5701f6fdfb893ba"
      }
    }
  ]
}

locals {
  static_hosts = "{\"${var.domain}\" \"www.${var.domain}\" \"tolon.${var.domain}\"}"
}

# The static sites only serve GET/HEAD and have no PHP, WordPress or dotfiles.
resource "cloudflare_ruleset" "waf_custom" {
  zone_id = var.cloudflare_zone_id
  name    = "Static site rules"
  kind    = "zone"
  phase   = "http_request_firewall_custom"
  rules = [
    {
      ref         = "static_methods"
      description = "Static sites: block methods other than GET, HEAD, OPTIONS"
      expression  = "(http.host in ${local.static_hosts} and not http.request.method in {\"GET\" \"HEAD\" \"OPTIONS\"})"
      action      = "block"
    },
    {
      ref         = "static_probes"
      description = "Static sites: block common exploit probes"
      expression  = "(http.host in ${local.static_hosts} and (http.request.uri.path contains \"/wp-\" or http.request.uri.path contains \"/xmlrpc\" or http.request.uri.path contains \"/.env\" or http.request.uri.path contains \"/.git\" or http.request.uri.path contains \"/cgi-bin\" or ends_with(http.request.uri.path, \".php\") or ends_with(http.request.uri.path, \".asp\") or ends_with(http.request.uri.path, \".aspx\")))"
      action      = "block"
    }
  ]
}
