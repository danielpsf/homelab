#!/bin/bash
# Wazuh Dashboard entrypoint wrapper
# Generates opensearch_dashboards.yml from env vars at runtime,
# bypassing the keystore which requires /etc/wazuh-dashboard to be writable.

INSTALL_DIR=/usr/share/wazuh-dashboard
CONFIG_FILE="$INSTALL_DIR/config/opensearch_dashboards.yml"
DASHBOARD_USERNAME="${DASHBOARD_USERNAME:-kibanaserver}"
DASHBOARD_PASSWORD="${DASHBOARD_PASSWORD:-kibanaserver}"
OPENSEARCH_HOSTS="${OPENSEARCH_HOSTS:-https://localhost:9200}"

cat > "$CONFIG_FILE" <<EOF
server.host: 0.0.0.0
server.port: 443
opensearch.hosts: ${OPENSEARCH_HOSTS}
opensearch.username: ${DASHBOARD_USERNAME}
opensearch.password: ${DASHBOARD_PASSWORD}
opensearch.ssl.verificationMode: certificate
opensearch.requestHeadersAllowlist: ["securitytenant","Authorization"]
opensearch_security.multitenancy.enabled: false
opensearch_security.readonly_mode.roles: ["kibana_read_only"]
server.ssl.enabled: true
server.ssl.key: "/etc/wazuh-dashboard/certs/dashboard-key.pem"
server.ssl.certificate: "/etc/wazuh-dashboard/certs/dashboard.pem"
opensearch.ssl.certificateAuthorities: ["/etc/wazuh-dashboard/certs/root-ca.pem"]
uiSettings.overrides.defaultRoute: /app/wz-home
EOF

# Configure Wazuh app (API connection)
/wazuh_app_config.sh $WAZUH_UI_REVISION

# Start dashboard directly (skip keystore)
exec $INSTALL_DIR/bin/opensearch-dashboards -c "$CONFIG_FILE"
