# new-api 发布简版

> 本文档只保留当前实际使用的发布方式：
>
> - 本地打包好 `new-api` 和 `.env`
> - 服务器依赖 PostgreSQL、Redis、systemd
> - 服务端只做上传、解压、启动、验收

## 一、发布前

### 1. 准备交付物

发布包建议包含：

```text
newapi-runtime.tar.gz
├── new-api
└── .env
```

说明：

- `new-api`：后端二进制，前端页面已内嵌，不需要单独部署前端
- `.env`：目标环境运行配置

如果不打压缩包，也至少准备这两个文件：

- `new-api`
- `.env`

### 2. 确认服务器前置条件

应用服务器建议满足：

- Linux x86_64
- 已安装 `systemd`
- 具备部署目录写权限
- 能连 PostgreSQL
- 能连 Redis

建议资源：

- 应用服务器：4 核 / 8 GB / 50 GB+
- PostgreSQL：14+，优先 15
- Redis：6.x 或 7.x

### 3. 确认 `.env` 关键项

发布前至少检查这些变量：

```env
PORT=3000
TZ=Asia/Shanghai

SESSION_SECRET=REPLACE_WITH_A_LONG_RANDOM_SECRET
CRYPTO_SECRET=REPLACE_WITH_ANOTHER_LONG_RANDOM_SECRET

SQL_DSN=postgresql://newapi:StrongPassword@10.0.0.10:5432/newapi?sslmode=disable
REDIS_CONN_STRING=redis://:StrongPassword@10.0.0.11:6379/0
```

重点说明：

- `SESSION_SECRET` 不能使用默认值 `random_string`
- `CRYPTO_SECRET` 必须配置
- `SQL_DSN` 必须指向目标 PostgreSQL
- `REDIS_CONN_STRING` 必须指向目标 Redis
- `PORT` 建议提前定好，默认 3000

如需生成密钥：

```bash
openssl rand -hex 32
```

### 4. 确认网络与内部代理

必须以**实际运行 `new-api` 的应用服务器**为准检查网络，不要只在本机或跳板机验证。

发布前至少确认：

- 应用服务器可以访问 PostgreSQL
- 应用服务器可以访问 Redis
- 应用服务器可以访问**渠道里实际配置的 `base_url`**
- 如果渠道配置的是**内部代理地址**，则只需要验证这些内部代理地址可达

也就是说，检查目标不是固定写死的外部公网域名，而是：

- 你在渠道里配置的地址
- 你们内网代理实际提供的地址

可用下面命令验证（把地址替换成你们真实的内部代理地址）：

```bash
curl -sv --connect-timeout 10 http://你的内部代理地址/ -o /dev/null
curl -sv --connect-timeout 10 https://你的内部代理地址/ -o /dev/null
```

---

## 二、发布后

### 1. 创建部署目录

建议统一部署到：

```bash
/app/new-api
```

初始化目录：

```bash
sudo mkdir -p /app/new-api/{logs,backups}
sudo chown -R appuser:appuser /app/new-api
```

> `appuser` 请替换为实际部署用户。

### 2. 上传并解压发布包

如果上传的是压缩包：

```bash
cd /app/new-api
tar -xzf /path/to/newapi-runtime.tar.gz
```

如果上传的是单独文件，则上传到：

- `/app/new-api/new-api`
- `/app/new-api/.env`

### 3. 设置权限

```bash
chmod 755 /app/new-api/new-api
chmod 600 /app/new-api/.env
```

### 4. 配置 systemd

新建：

```text
/etc/systemd/system/new-api.service
```

内容示例：

```ini
[Unit]
Description=new-api Service
After=network.target

[Service]
User=appuser
Group=appuser
WorkingDirectory=/app/new-api
EnvironmentFile=/app/new-api/.env
ExecStart=/app/new-api/new-api --log-dir /app/new-api/logs
Restart=always
RestartSec=5
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
```

说明：

- `WorkingDirectory` 建议固定为 `/app/new-api`
- `PORT` 通过 `.env` 提供即可
- 服务器上不需要重新编译

### 5. 是否需要 Nginx

当前项目**不依赖 Nginx 才能运行**。

也就是说，你可以直接让 `new-api` 监听 `PORT` 对外提供服务。

只有在以下场景下，才需要额外加 Nginx：

- 需要绑定域名
- 需要做 HTTPS 证书终止
- 需要统一入口转发

如果你们当前内网发布不需要这些能力，可以**不配 Nginx，直接跳过这一节**。

如果后续确实需要 Nginx，再参考下面示例：

```nginx
server {
    listen 80;
    server_name newapi.intra.example.com;

    location / {
        proxy_pass http://127.0.0.1:3000;
        proxy_http_version 1.1;

        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-Host $host;
    }
}
```

如果走 HTTPS，再补 443 和证书配置即可。

### 6. 启动服务

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now new-api
sudo systemctl status new-api --no-pager
```

### 7. 首次初始化

当前分支不会自动创建固定管理员账号。

首次发布后需要通过下面方式完成初始化：

- 浏览器打开：`http://你的域名或IP:端口/`
- 按页面引导完成 `/api/setup`

也可以直接调用接口：

```bash
curl -X POST http://127.0.0.1:3000/api/setup \
  -H 'Content-Type: application/json' \
  -d '{
    "username": "admin",
    "password": "YourStrongPassword",
    "confirmPassword": "YourStrongPassword"
  }'
```

要求：

- 用户名不超过 12 个字符
- 密码至少 8 位

### 8. 发布后验收

至少检查以下项目：

1. 服务状态

```bash
sudo systemctl status new-api --no-pager
```

2. 健康检查

```bash
curl http://127.0.0.1:3000/api/status
```

3. 页面访问

- 页面能打开
- 未初始化时能进入初始化流程
- 已初始化时能正常登录后台

4. 依赖连通

- 应用日志中没有 PostgreSQL 连接错误
- 应用日志中没有 Redis ping 失败

5. 上游代理验证

确认应用服务器仍可访问渠道里实际配置的上游地址。

- 如果渠道配置的是内部代理地址，就验证内部代理地址
- 不要再按固定公网域名做验收

如果以上都正常，这次发布即可视为完成。
