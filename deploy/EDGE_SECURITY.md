# Edge and HTTP Ingress Security

Codebot supports long-lived SSE and WebSocket requests. Protect the request
ingress without imposing a response `WriteTimeout`: a write deadline would
terminate healthy long generations and streams.

## Application defaults

- `server.max_header_bytes: 65536` limits HTTP/1 request headers to 64 KiB;
  Go maps it to the corresponding HTTP/2 header-list limit.
- `server.read_header_timeout: 10` bounds slow-header attacks. It does not
  limit request processing or response streaming.
- `server.max_request_body_size: 268435456` is the absolute 256 MiB safety net.
- `gateway.max_body_size: 268435456` remains available to multimodal, Gemini,
  image, video, and batch-image endpoints.
- `gateway.text_max_body_size: 33554432` limits the known pure-text
  `/embeddings` and `/alpha/search` endpoints to 32 MiB.
- H2C defaults to 50 concurrent streams per connection, a 2 MiB connection
  upload window, and a 512 KiB stream upload window.
- Invalid credential abuse is limited in process by trusted client IP (IPv6
  `/64`): 120 failures per 60 seconds followed by a 60-second block. This is a
  per-instance safety net; multi-instance enforcement still belongs at the
  load balancer, CDN, or WAF.

Do not add a single application-wide request semaphore: an SSE request may
legitimately occupy it for many minutes. Apply connection and unauthenticated
request controls at the edge; authenticated user/API-key concurrency remains
the application's responsibility.

## 新用户注册密码

用户自行设置的新注册密码至少需要 6 个字符，且同时包含英文大写字母、小写字母和数字；允许特殊字符。普通邮箱注册、第三方登录后的本地账号创建与已验证邮箱补全注册共用服务端校验，前端同步提示并拦截提交。弱密码会在验证码消费、账号创建和待注册会话变更前被拒绝。现有账号登录及第三方登录自动生成的内部密码不受此注册规则影响。

## Trusted client IPs

所有来源地址解析均受 `server.trusted_proxies`（可信代理列表）约束。未配置或显式配置为空时，只使用直接连接地址；启用旧版兼容开关也不能跳过此校验。反向代理部署升级前必须配置实际连接应用的代理地址，否则多个客户端会按同一个代理地址统计与限流。

标准 `X-Forwarded-For`（代理链）和 `X-Real-IP`（实际来源地址）使用 Gin 的可信代理链，从最近一跳向前解析到首个不可信地址。`CF-Connecting-IP`（Cloudflare 客户端地址）不再自动优先于反向代理写入的标准头。

`security.trust_forwarded_ip_for_api_key_acl`（自定义来源头开关）保留现有设置入口：开启后，仅当连接来自可信代理时，才按 `security.forwarded_client_ip_headers`（自定义来源头列表）依次尝试额外请求头。名单必须只包含代理会覆盖的头；例如使用 Cloudflare 时，可显式配置 `CF-Connecting-IP`，同时限制源站只能由 Cloudflare 访问。自定义头中的地址链也从最近一跳向前解析，不会跳过不可信的内网地址。关闭开关后，仅使用标准可信代理链。

自定义头列表最多 16 项，头名不区分大小写。列表和开关可从管理设置动态更新，同一个请求使用一致快照；可信代理列表修改需要重启应用。设置加载失败时使用标准可信代理模式。

同机反代、应用直接监听宿主机的配置示例：

```yaml
server:
  trusted_proxies:
    - 127.0.0.1/32
    - ::1/128
```

宿主机 Nginx 连接 Docker 映射端口时，应用看到的直接来源可能是 Docker 网桥网关，应配置实际网关的单个地址，不能将整个内网或所有地址加入可信名单。本次排查服务器的直接网关为 `172.19.0.1`，上线前应重新核对：

```yaml
server:
  trusted_proxies:
    - 172.19.0.1/32
security:
  trust_forwarded_ip_for_api_key_acl: false
  forwarded_client_ip_headers: []
```

面向宿主机反代的 Docker 端口应绑定回环地址，例如 `127.0.0.1:3000:8080`，关闭公网直连后端。Nginx 应覆盖标准来源头并清除未经校验的 CDN 头；自定义头也必须覆盖或清除，不能原样传入应用。

普通密码登录限制每个真实来源每分钟 20 次；普通登录与第三方绑定密码登录共用每个邮箱每分钟 10 次的计数，邮箱按忽略大小写和首尾空白的形式归一化，使用摘要作为 Redis（共享计数缓存）键。每次有效格式的密码登录都计数，不查询账号是否存在；超过次数返回 429（请求过多），携带 `Retry-After`（等待秒数）。窗口结束后可再次登录，缓存故障时拒绝登录，防止绕过。分布式节点共享邮箱计数；邮箱限制也可能被恶意消耗，因此它只是与来源限制配合的一层防护，不能代替独立密码和第二步验证。

升级不会使已经泄露的密码或令牌失效。受影响账号必须通过正式改密流程撤销旧登录会话，再轮换接口令牌。

## Nginx baseline

Define shared zones in the `http` block. Tune rates to measured legitimate
traffic; the values below are conservative starting points, not universal
capacity targets.

```nginx
limit_conn_zone $binary_remote_addr zone=sub2api_conn:20m;
limit_req_zone  $binary_remote_addr zone=sub2api_auth:20m rate=5r/s;
limit_req_zone  $binary_remote_addr zone=sub2api_api:40m rate=30r/s;
map $http_upgrade $connection_upgrade {
    default upgrade;
    ''      close;
}

server {
    listen 443 ssl http2;
    server_name api.example.com;

    client_header_timeout 10s;
    client_max_body_size 256m;
    large_client_header_buffers 4 16k;
    limit_conn sub2api_conn 40;

    location ^~ /api/v1/auth/ {
        limit_req zone=sub2api_auth burst=10 nodelay;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header CF-Connecting-IP "";
        proxy_set_header True-Client-IP "";
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_pass http://127.0.0.1:8080;
    }

    location ~ ^/(v1/)?(embeddings|alpha/search)$ {
        client_max_body_size 32m;
        limit_req zone=sub2api_api burst=60 nodelay;
        proxy_pass http://127.0.0.1:8080;
    }

    location / {
        limit_req zone=sub2api_api burst=60 nodelay;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header CF-Connecting-IP "";
        proxy_set_header True-Client-IP "";
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;
        proxy_buffering off;
        proxy_request_buffering off;
        proxy_read_timeout 1800s;
        proxy_send_timeout 1800s;
        proxy_pass http://127.0.0.1:8080;
    }
}
```

If Nginx gzip is enabled in the `http` block, keep `text/event-stream` out of
`gzip_types` and do not use `gzip_types *` for Codebot. The
`proxy_buffering off` setting above prevents proxy buffering, but it does not
disable the gzip response filter. Use an explicit list for ordinary responses:

```nginx
gzip on;
gzip_types text/plain text/css application/json application/javascript application/xml image/svg+xml;
```

If a shared global configuration cannot exclude SSE by content type, set
`gzip off;` in the locations serving streaming API routes. This leaves gzip
available for the web UI and static assets.

Do not use an incoming `$http_x_forwarded_for` value unless Nginx real-IP
processing is restricted to explicit trusted proxy CIDRs.

## Caddy and CDN

The bundled `deploy/Caddyfile` sets a 64 KiB header limit, a 10-second header
timeout, a 256 MiB absolute body limit, and overwrites forwarded addresses from
the TCP peer. It is therefore a direct-to-Caddy baseline. Do not use its
`{remote_host}` forwarding lines unchanged behind a CDN: all clients would be
attributed to a CDN egress address, collapsing rejection aggregation and the
invalid-auth limiter onto unrelated users.

The bundled Caddy configuration leaves `flush_interval` unset so Caddy can
automatically flush `text/event-stream` responses while still propagating
client cancellation upstream. Do not set it globally: positive values can add
streaming latency, while Caddy 2.6.2's special `-1` mode also causes
reverse-proxied requests to continue after clients disconnect. The
configuration uses an explicit response content-type list for compression. Do
not replace that list with `text/*` or the shorthand `encode gzip zstd`: both
match `text/event-stream` and can buffer SSE until the response ends. Keep
streaming responses uncompressed while retaining compression for the web UI,
JSON, and static assets.

For a CDN deployment, first firewall the origin so only current CDN egress
CIDRs can connect. Then configure those exact ranges as Caddy trusted proxies
and derive upstream headers from Caddy's parsed `{client_ip}`. For example:

```caddyfile
{
	servers {
		trusted_proxies static 192.0.2.0/24 2001:db8:1234::/48
		trusted_proxies_strict
		client_ip_headers CF-Connecting-IP X-Forwarded-For
	}
}

api.example.com {
	reverse_proxy 127.0.0.1:8080 {
		header_up X-Real-IP {client_ip}
		header_up X-Forwarded-For {client_ip}
	}
}
```

Replace the documentation ranges with the CDN's published, automatically
maintained egress ranges. `CF-Connecting-IP` is safe here only because direct
origin access is blocked and Caddy trusts only those TCP peers. Configure
Codebot `server.trusted_proxies` with the Caddy address/private subnet so the
application accepts only Caddy's rewritten headers.

Caddy core does not provide a general request-rate limiter; use a trusted
CDN/WAF, a supported rate-limit module, or host firewall controls.

At a CDN/WAF, configure connection limits, header/body limits, bot challenges,
and per-IP/ASN rates before traffic reaches the origin. Allow origin ingress
only from CDN egress CIDRs or a private load balancer. Keep the application port
off the public Internet.

## DDoS boundary

Application checks reduce amplification after a connection reaches Go. They
cannot absorb volumetric attacks, TLS floods, bandwidth saturation, or a large
distributed source set. Those require upstream network capacity, CDN/WAF
filtering, provider firewall rules, and origin isolation. Avoid high-cardinality
metrics or per-request database security logs during rejection storms.
