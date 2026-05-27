# 部署说明

## 1. 编译

```bash
go build -o registry-mirror-proxy ./cmd/registry-mirror-proxy
```

## 2. 安装

```bash
sudo install -m 0755 registry-mirror-proxy /usr/local/bin/registry-mirror-proxy
sudo mkdir -p /etc/registry-mirror-proxy /var/cache/registry-mirror-proxy
sudo cp configs/config.example.yaml /etc/registry-mirror-proxy/config.yaml
```

## 3. TLS 证书

Docker 客户端配置的是 `https://192.168.44.100`，证书必须包含 IP SAN：

```text
IP Address:192.168.44.100
```

测试环境可以用内部 CA 或自签 CA，但客户机器必须信任该 CA。不建议长期使用 `insecure-registries`。

## 4. systemd

```bash
sudo useradd --system --no-create-home --shell /usr/sbin/nologin registry-mirror
sudo chown -R registry-mirror:registry-mirror /var/cache/registry-mirror-proxy
sudo cp deploy/systemd/registry-mirror-proxy.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now registry-mirror-proxy
```

如果上游需要认证，建议用 systemd drop-in 注入环境变量：

```bash
sudo systemctl edit registry-mirror-proxy
```

```ini
[Service]
Environment="REGISTRY_MIRROR_UPSTREAM_USERNAME=your-upstream-user"
Environment="REGISTRY_MIRROR_UPSTREAM_PASSWORD=your-upstream-password-or-token"
```

然后重启：

```bash
sudo systemctl daemon-reload
sudo systemctl restart registry-mirror-proxy
```

如果使用火山引擎 `GetAuthorizationToken` 自动获取临时访问密钥，使用下面的方式：

```ini
[Service]
Environment="REGISTRY_MIRROR_VOLC_AUTH_ENABLED=true"
Environment="REGISTRY_MIRROR_VOLC_ACCESS_KEY=your-ak"
Environment="REGISTRY_MIRROR_VOLC_SECRET_KEY=your-sk"
Environment="REGISTRY_MIRROR_VOLC_REGION=cn-beijing"
Environment="REGISTRY_MIRROR_VOLC_ENDPOINT=https://cr.cn-beijing.volcengineapi.com"
Environment="REGISTRY_MIRROR_VOLC_REGISTRY=your-registry-name"
```

其中 `REGISTRY_MIRROR_VOLC_REGISTRY` 是镜像仓库实例名称，不是域名。

## 5. 客户侧 Docker 配置

`/etc/docker/daemon.json`：

```json
{
  "registry-mirrors": ["https://192.168.44.100"]
}
```

重启 Docker：

```bash
sudo systemctl restart docker
```

## 6. 验证

```bash
curl -vk https://192.168.44.100/v2/
docker pull alpine:latest
docker pull nginx:latest
docker pull mysql:8
```

客户侧响应、日志和抓包中不应出现真实上游域名。
