# new-api 内网部署说明（本地预打包二进制）

> 本文档只保留我们当前实际使用的部署方式：
>
> - **本地先打包好运行二进制**
> - **服务器只负责上传、解压、配置、启动**
> - **数据库固定 PostgreSQL**
> - **缓存固定 Redis**
>
> 不再展开其他部署方式，避免文档过乱。

---

## 1. 固定部署方案

这次内网部署统一按下面方式执行：

- 应用程序：`new-api`
- 部署方式：**本地预打包二进制上传部署**
- 数据库：**PostgreSQL**
- 缓存：**Redis**
- 进程管理：**systemd**
- 入口代理：**Nginx**
- 前端：**已经内嵌到后端二进制中，不单独部署**

也就是说，服务器侧只做四件事：

1. 上传文件
2. 解压文件
3. 核对或微调 `.env`
4. 启动服务

---

## 2. 交付物

上传到服务器的运行包建议类似：

```text
newapi-runtime.tar.gz
```

解压后至少应包含：

```text
new-api
.env
```

如果不是打成 tar.gz，也可以直接上传以下文件：

- `new-api`
- `.env`

---

## 3. 资源与依赖建议

## 3.1 应用服务器

建议配置：

- CPU：**4 核**
- 内存：**8 GB**
- 磁盘：**50 GB+**

用途：

- 运行 `new-api` 二进制
- 写本地日志
- 通过内网连接 PostgreSQL 和 Redis
- 通过 VPN / 白名单访问外部模型上游

## 3.2 PostgreSQL

建议配置：

- CPU：**4 核**
- 内存：**8 GB**
- 磁盘：**SSD 100 GB+**

推荐版本：

- PostgreSQL **14+**，优先 **15**

## 3.3 Redis

建议配置：

- CPU：**2 核**
- 内存：**4 GB**

推荐版本：

- Redis **6.x / 7.x**

---

## 4. 服务器基础环境

推荐环境：

- Linux x86_64
- systemd
- Nginx
- 具备写目录权限的部署账号

建议安装：

- `curl`
- `tar`
- `gzip`
- `openssl`
- `ca-certificates`
- `tzdata`

---

## 5. 内网外联 / VPN / 白名单要求

由于系统在内网运行，除了本机端口、PostgreSQL、Redis 之外，还要确认：

**运行 `new-api` 的应用服务器本身** 能访问外部模型上游。

> 注意：
> - 不是办公电脑能打开就算打通
> - 不是跳板机能访问就算打通
> - 必须以实际运行 `new-api` 的服务器为准验证

当前需要打通的外部站点如下：

| URL / 站点 | Host | 协议 | 端口 | 说明 |
|---|---|---|---|---|
| `https://api.modelverse.cn` | `api.modelverse.cn` | HTTPS | 443 | 外部模型上游 |
| `https://aigateway.edgecloudapp.com/` | `aigateway.edgecloudapp.com` | HTTPS | 443 | 外部模型网关 |
| `https://api.wenwen-ai.com` | `api.wenwen-ai.com` | HTTPS | 443 | 外部模型上游 |
| `https://api.deepseek.com` | `api.deepseek.com` | HTTPS | 443 | DeepSeek 上游 |

### 5.1 推荐验证命令

直接在应用服务器上执行：

```bash
curl -sv --connect-timeout 10 https://api.modelverse.cn/ -o /dev/null
curl -sv --connect-timeout 10 https://aigateway.edgecloudapp.com/ -o /dev/null
curl -sv --connect-timeout 10 https://api.wenwen-ai.com/ -o /dev/null
curl -sv --connect-timeout 10 https://api.deepseek.com/ -o /dev/null
```

如果需要进一步确认 TLS / SNI：

```bash
openssl s_client -connect api.modelverse.cn:443 -servername api.modelverse.cn </dev/null
openssl s_client -connect aigateway.edgecloudapp.com:443 -servername aigateway.edgecloudapp.com </dev/null
openssl s_client -connect api.wenwen-ai.com:443 -servername api.wenwen-ai.com </dev/null
openssl s_client -connect api.deepseek.com:443 -servername api.deepseek.com </dev/null
```

### 5.2 什么算打通

一般满足下面任意一种，就说明网络路径已通：

- DNS 解析正常
- `curl` 能建立 HTTPS 连接，即使返回 `401 / 403 / 404` 也通常表示链路是通的
- `openssl s_client` 能握手成功并返回证书链

以下情况通常代表没打通：

- `Could not resolve host`
- `Connection timed out`
- `No route to host`
- `Failed to connect`

---

## 6. 服务器目录规划

建议目录：

```bash
/app/new-api/
├── new-api
├── .env
├── logs/
└── backups/
```

说明：

- `new-api`：主程序
- `.env`：运行配置
- `logs/`：程序日志目录
- `backups/`：升级或回滚时备份

---

## 7. 关键环境变量

按本次部署方案，重点关注以下变量：

| 变量 | 是否必填 | 说明 |
|---|---|---|
| `PORT` | 建议填写 | 服务监听端口，默认 3000 |
| `SESSION_SECRET` | **必填** | 会话密钥，不能用 `random_string` |
| `CRYPTO_SECRET` | **必填** | Redis 相关加密密钥 |
| `SQL_DSN` | **必填** | PostgreSQL 连接串 |
| `REDIS_CONN_STRING` | **必填** | Redis 连接串 |
| `TZ` | 建议填写 | 例如 `Asia/Shanghai` |

### 7.1 密钥生成

生成 `SESSION_SECRET`：

```bash
openssl rand -hex 32
```

生成 `CRYPTO_SECRET`：

```bash
openssl rand -hex 32
```

### 7.2 PostgreSQL 连接串示例

```bash
postgresql://newapi:StrongPassword@10.0.0.10:5432/newapi?sslmode=disable
```

### 7.3 Redis 连接串示例

```bash
redis://:StrongPassword@10.0.0.11:6379/0
```

---

## 8. `.env` 标准示例

```env
PORT=3000
TZ=Asia/Shanghai

SESSION_SECRET=REPLACE_WITH_A_LONG_RANDOM_SECRET
CRYPTO_SECRET=REPLACE_WITH_ANOTHER_LONG_RANDOM_SECRET

SQL_DSN=postgresql://newapi:StrongPassword@10.0.0.10:5432/newapi?sslmode=disable
REDIS_CONN_STRING=redis://:StrongPassword@10.0.0.11:6379/0

ERROR_LOG_ENABLED=true
BATCH_UPDATE_ENABLED=true
```

---

## 9. 部署步骤

## 9.1 准备服务器目录

```bash
sudo mkdir -p /app/new-api/{logs,backups}
sudo chown -R appuser:appuser /app/new-api
```

> `appuser` 请替换成实际部署用户。

## 9.2 上传运行包或运行文件

如果拿到的是运行包，例如：

```text
newapi-runtime.tar.gz
```

默认建议该压缩包内直接包含：

```text
new-api
.env
```

上传到服务器后解压：

```bash
cd /app/new-api
tar -xzf /path/to/newapi-runtime.tar.gz
```

如果拿到的是单独文件，则直接上传：

- `new-api`
- `.env`

## 9.3 设置权限

```bash
chmod 755 /app/new-api/new-api
chmod 600 /app/new-api/.env
```

## 9.4 配置 systemd

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

## 9.5 启动服务

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now new-api
sudo systemctl status new-api --no-pager
```

说明：

- `WorkingDirectory` 建议固定为 `/app/new-api`
- `PORT` 通过 `.env` 提供即可
- 压缩包内建议直接携带目标环境可用的 `.env`
- 服务器上不需要重新编译

---

## 10. Nginx 反向代理示例

如果通过域名访问，建议由 Nginx 把 80/443 反代到本地应用端口。

示例：

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

        proxy_read_timeout 600s;
        proxy_send_timeout 600s;
    }
}
```

如果要上 HTTPS，再在 443 server 上补证书配置即可。

---

## 11. 首次初始化

当前分支的真实流程是：

- 首次启动不会自动创建固定管理员账号
- 系统通过 `/api/setup` 完成初始化

### 11.1 页面初始化

浏览器访问：

```text
http://你的域名或IP:端口/
```

按页面引导完成初始化即可。

### 11.2 也可以直接调初始化接口

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

- 用户名长度不要超过 12 个字符
- 密码长度至少 8 位

---

## 12. 验收步骤

## 12.1 服务状态

```bash
sudo systemctl status new-api --no-pager
```

## 12.2 端口监听

```bash
ss -lntp | grep 3000
```

## 12.3 健康检查

```bash
curl http://127.0.0.1:3000/api/status
```

## 12.4 页面访问

确认：

- 页面能打开
- 未初始化时能进入初始化流程
- 已初始化时能正常登录后台

## 12.5 PostgreSQL / Redis 连通

确认应用日志中：

- 没有 PostgreSQL 连接错误
- 没有 Redis ping 失败

## 12.6 外联网络验证

确认应用服务器：

- 能访问 `api.modelverse.cn`
- 能访问 `aigateway.edgecloudapp.com`
- 能访问 `api.wenwen-ai.com`
- 能访问 `api.deepseek.com`

---

## 13. 常见问题

### 13.1 `SESSION_SECRET` 还在用默认值

如果设置成 `random_string`，程序会直接拒绝启动。

### 13.2 PostgreSQL 连接串或权限有问题

常见现象：

- DSN 写错
- 库名不存在
- 用户名/密码错误
- 数据库用户没有建表或迁移权限

### 13.3 Redis 不通

当前代码会在启动时 Ping Redis；不通会直接启动失败。

### 13.4 VPN / 白名单只在跳板机验证，没有在应用服务器验证

这是内网环境最常见的误判之一。必须以实际运行 `new-api` 的服务器为准。

---

## 14. 最终结论

本文只保留这一种部署方式：

- **本地预打包二进制运行包上传到服务器**
- **服务器只做解压、配置、启动**
- **数据库固定 PostgreSQL**
- **缓存固定 Redis**
- **通过 systemd + Nginx 落地**

如果没有特殊情况，就按这套执行即可。