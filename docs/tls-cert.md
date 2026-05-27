# TLS 证书说明

Docker 客户端使用 `https://192.168.44.100` 连接代理服务时，服务端证书必须满足：

1. 客户机器信任签发该证书的 CA。
2. 证书 SAN 包含 `IP Address:192.168.44.100`。

测试环境可以用下面的 OpenSSL 配置生成自签证书：

```ini
[req]
default_bits = 2048
prompt = no
default_md = sha256
x509_extensions = v3_req
distinguished_name = dn

[dn]
CN = 192.168.44.100

[v3_req]
subjectAltName = @alt_names

[alt_names]
IP.1 = 192.168.44.100
```

生成命令：

```bash
openssl req -x509 -nodes -days 365 \
  -newkey rsa:2048 \
  -keyout tls.key \
  -out tls.crt \
  -config openssl-ip-san.cnf
```

生产环境建议使用企业内部 CA 签发证书，并把 CA 证书分发到客户机器的系统信任链中。
