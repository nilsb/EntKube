# Caddyfile.ha — Load-balance across HA nodes with cookie-based sticky sessions.
# DOMAIN is injected from the environment (set in .env / docker-compose.ha.yml).
# For production, point your DNS A record at this host and Caddy will obtain a
# Let's Encrypt certificate automatically.
#
# Sticky sessions reduce split-brain reads during the sync window and give
# consistent JWT validation even if JWT_SECRET diverges between nodes.

{$DOMAIN:localhost} {
    reverse_proxy node-1:8080 node-2:8080 {
        # Consistent hashing on session cookie keeps a given browser session
        # on the same node as long as it is healthy.
        lb_policy       cookie {
            name  entkube_node
            path  /
            # Secure and HttpOnly are auto-added when the site uses HTTPS.
        }

        health_uri      /api/health
        health_interval 10s
        health_timeout  3s
    }
}
