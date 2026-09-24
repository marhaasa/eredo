# Egress proxy sidecar for the Claude sandbox.
# Squid allows CONNECT to port 443 on allowlisted domains and nothing else.
# socat optionally forwards one port to the host for the reminders MCP server.
FROM alpine:3.22
RUN apk add --no-cache squid socat ca-certificates
COPY squid.conf /etc/squid/squid.conf
COPY allowlist.txt /etc/claude-proxy/allowlist.txt
COPY entrypoint.sh /usr/local/bin/entrypoint
RUN chmod 0755 /usr/local/bin/entrypoint
EXPOSE 3128
ENTRYPOINT ["/usr/local/bin/entrypoint"]
