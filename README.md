# Registry Mirror Proxy

一个用 Go 实现的 Docker Registry Mirror 反向代理。客户 Docker daemon 只需要配置代理服务器地址：

```json
{
  "registry-mirrors": ["https://192.168.44.100"]
}
```

代理服务会在服务端访问真实上游，并尽量避免把真实上游域名通过响应头、重定向和错误响应暴露给客户。

## 功能

- Registry V2 拉取链路代理：`/v2/`、manifest、blob、tags。
- 默认只允许 `GET`、`HEAD`、`OPTIONS`，拒绝推送相关方法。
- 服务端内部跟随 HTTPS 重定向，避免 `Location` 泄露上游域名。
- 上游错误响应清洗。
- 大镜像层流式转发。
- 多上游故障切换。
- 可选磁盘 blob cache。
- 可选 CIDR 客户端白名单。
- 可选并发控制。
- `/healthz`、`/readyz`、`/metrics`。
- systemd 部署样例。

## 上游认证

如果上游镜像服务需要登录，不要让客户登录真实上游域名。可以把上游账号密码配置在代理侧：

```yaml
upstream_username: "your-upstream-user"
upstream_password: "your-upstream-password-or-token"
```

生产环境更建议通过 systemd 环境变量注入：

```ini
[Service]
Environment="REGISTRY_MIRROR_UPSTREAM_USERNAME=your-upstream-user"
Environment="REGISTRY_MIRROR_UPSTREAM_PASSWORD=your-upstream-password-or-token"
```

代理会在内部请求上游 token，并用 Bearer token 重试 manifest/blob 请求。

也可以通过上游授权 API 自动获取临时访问凭据：

```yaml
upstream_auth_enabled: true
upstream_region: "go-sec-v1-..."
upstream_endpoint: "go-sec-v1-..."
upstream_registry: "go-sec-v1-..."
upstream_access_key: "go-sec-v1-..."
upstream_secret_key: "go-sec-v1-..."
```

加密使用 `go-sec` 工具：

```bash
export REGISTRY_MIRROR_CONFIG_KEY='一串足够长的本地密钥'
go build -o go-sec ./cmd/go-sec
./go-sec '明文内容'
```

输出会是：

```text
go-sec-v1-...
```

代理启动时也需要同一把解密密钥：

```ini
[Service]
Environment="REGISTRY_MIRROR_CONFIG_KEY=一串足够长的本地密钥"
```

代理会缓存 `GetAuthorizationToken` 返回的 `Username` 和 `Token`，并在过期前自动刷新。

## 快速开始

```bash
go build -o registry-mirror-proxy ./cmd/registry-mirror-proxy
./registry-mirror-proxy --config configs/config.example.yaml
```

生产环境请配置 TLS 证书，并确保客户端信任该证书。证书必须包含代理 IP 的 SAN，例如 `192.168.44.100`。

## 测试

```bash
go test ./...
```

详细部署说明见 [docs/deploy.md](/Users/chengyh/2-code/go/registry-mirror/docs/deploy.md)，需求与验收标准见 [docs/registry-mirror-proxy-prd.md](/Users/chengyh/2-code/go/registry-mirror/docs/registry-mirror-proxy-prd.md)。
