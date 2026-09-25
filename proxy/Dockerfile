# eredo egress proxy sidecar.
# Squid allows CONNECT to port 443 on allowlisted public domains and nothing
# else. socat optionally forwards one port to the host (EREDO_FORWARD).
FROM alpine:3.22
RUN apk add --no-cache squid socat ca-certificates
COPY squid.conf /etc/squid/squid.conf
COPY allowlist.txt /etc/eredo/allowlist.txt
COPY entrypoint.sh /usr/local/bin/entrypoint
RUN chmod 0755 /usr/local/bin/entrypoint
EXPOSE 3128
ENTRYPOINT ["/usr/local/bin/entrypoint"]
