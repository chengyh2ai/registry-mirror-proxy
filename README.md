# Registry Mirror Proxy

一个用 Go 实现的 Docker Registry Mirror 反向代理。客户 Docker daemon 只需要配置代理服务器地址：

```json
{
  "registry-mirrors": ["https://192.168.44.100"]
}
```

代理服务会在服务端访问真实上游：

```text
https://chengyh2go-cn-beijing.cr.volces.com
```

并尽量避免把真实上游域名通过响应头、重定向和错误响应暴露给客户。

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
