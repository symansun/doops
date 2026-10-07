# DoWeb DNS and Routing

DoWeb configures Nginx. DNS decides which gateway receives a browser request.
Both must be correct.

## Domain Registry vs Public DNS

`domain-set` records DoWeb metadata:

```bash
node /app/skills/doweb/scripts/doweb.mjs domain-set \
  --domain aihub.org.cn \
  --host-ip 114.55.61.32 \
  --provider aliyun
```

It does not create, update or delete DNS provider records. Provider-side records
must be managed through DNS tooling such as AliDNS, BIND or DoDNS.

## Exact Records and Wildcards

If `*.aihub.org.cn` exists, any new hostname without an exact record can inherit
the wildcard answer.

Example:

```text
*.aihub.org.cn A 114.55.61.32
*.aihub.org.cn A 192.168.150.228
```

Then `proxy-106.aihub.org.cn` may resolve to both addresses unless an exact
record overrides it:

```text
proxy-106.aihub.org.cn A 114.55.61.32
```

Every returned A record must serve the same hostname. If one IP has the DoWeb
site and another does not, users will see intermittent failures depending on
which address their resolver or proxy selects.

## Fake IP Ranges

Local proxy or TUN software may return fake IPs such as `198.18.2.84`. Treat
`198.18.0.0/15` as a local network-layer mapping, not as authoritative DNS
truth.

When local `dig` looks suspicious, verify with DNS-over-HTTPS or a clean remote
node:

```bash
curl -sS 'https://dns.google/resolve?name=proxy-106.aihub.org.cn&type=A'
curl -sS 'https://cloudflare-dns.com/dns-query?name=proxy-106.aihub.org.cn&type=A' \
  -H 'accept: application/dns-json'
```

## Forced Gateway Verification

Use `curl --resolve` to separate DNS from Nginx behavior:

```bash
curl --resolve proxy-106.aihub.org.cn:80:114.55.61.32 \
  http://proxy-106.aihub.org.cn/
```

If forced access succeeds but normal access fails, inspect DNS, local proxy,
browser cache, HSTS, and multi-A routing.

## HTTP and HTTPS Are Separate

The current CLI listens on:

- `80` when no TLS cert/key is supplied;
- `443 ssl http2` when both `--tls-cert` and `--tls-key` are supplied.

If a host already has an unrelated `443` stream or default TLS server, HTTPS
requests may hit that existing service instead of DoWeb. A common symptom is a
Kubernetes Ingress fake certificate or a 404 on HTTPS while HTTP works.

Before claiming HTTPS support, verify:

```bash
curl -vk --resolve <domain>:443:<gateway-ip> https://<domain>/
```

## DNS Cannot Route Ports or Paths

DNS can point a name to an IP. It cannot route:

- `domain -> IP:port`;
- `domain -> /path`;
- HTTP redirects;
- TLS certificate selection;
- reverse-proxy behavior.

Those belong in Nginx or another web gateway. DoWeb should pair DNS records with
Nginx server blocks and health checks.

## Recommended DNS Shape for DoWeb

For public DoWeb sites:

```text
<site>.aihub.org.cn A 114.55.61.32
```

Avoid returning private addresses from public DNS unless the private address is
reachable by the intended clients and serves the same hostname.

For split-horizon access:

- public DNS should return the public DoWeb gateway;
- internal DNS should return the internal DoWeb gateway;
- both gateways must have equivalent DoWeb/Nginx config for the hostname.
