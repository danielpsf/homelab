# danielpsf.net

Personal site at the zone apex. Plain static files: HTML, CSS and one small script. No build step.

- `web/`: the site. Edit the HTML and CSS directly. `web/pt/` is the pt-BR copy of `web/index.html`; keep the two in sync.
- `nginx.conf`: headers, CSP, caching and rate limit.
- `web/js/menu.js` closes the mobile menu after a link tap. The menu also works without it.

After editing:

1. If you change the `application/ld+json` block in an HTML file, put its new sha256 in the CSP `script-src` in `nginx.conf` (`openssl dgst -sha256 -binary <block> | base64`).
2. If you change `css/site.css` or `js/menu.js`, bump the `?v=` value on their links. Cloudflare caches them for up to 1 h and 7 d.
3. `docker compose restart site` (the config is a bind mount, so restart rather than reload).
