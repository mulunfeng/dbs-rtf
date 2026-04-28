# MySQL HA Proxy + VIP 方案

## 架构

```
业务应用 ──→ 127.0.0.1:3308 (MySQL Proxy) ──→ mysql-primary (127.0.0.1:3306)
                                                 ↓ 故障切换时自动切换
                                             mysql-replica (127.0.0.1:3307)
```

**核心思路**：业务不直接连接 MySQL IP，而是通过代理。代理自动检测 MySQL 健康状态并切换目标。

## 组件

| 组件 | 端口 | 功能 |
|------|------|------|
| mysql-proxy | 3308 | TCP 代理，转发 MySQL 连接 |
| management API | 8081 | `/status` 查看当前目标，`/switch` 手动切换 |

## 使用

### 1. 启动 Proxy

```bash
cd D:\ai\dbs-rtf\.worktrees\impl
infra\mysql-proxy\mysql-proxy.exe
```

### 2. 业务连接

将所有业务的 MySQL 连接地址改为：

```yaml
# 原来
host: 10.0.1.5
port: 3306

# 改为
host: 127.0.0.1
port: 3308   # ← Proxy 端口
```

### 3. 自动故障切换

Proxy 每 5 秒检测一次当前目标的健康状态：
- 如果当前主库不可达，自动切换到备库
- 主库恢复后不会自动切回（防止抖动）
- 切换过程对新连接生效，已有连接不受影响

### 4. 手动切换

```bash
# 查看当前目标
curl http://localhost:8081/status

# 手动切换到备库
curl -X POST http://localhost:8081/switch -d '{"host":"127.0.0.1","port":"3307"}'
```

## 与 HA Supervisor 集成

HA Supervisor 通过 webhook 通知 Proxy 切换：

```yaml
ha:
  notification:
    webhook_url: "http://127.0.0.1:8081/switch"
```

当 Supervisor 检测到故障并完成 failover 后，会 POST 事件到 Proxy，Proxy 自动更新目标。

## 生产环境部署

### 方案 A：独立 Proxy 服务器

```
VIP: 10.0.1.100:3306
       ↓
  mysql-proxy (HAProxy / ProxySQL)
       ↓
  ┌────┴────┐
  primary   replica
```

Proxy 部署在独立服务器上，业务连接 VIP。推荐使用 **ProxySQL** 或 **HAProxy**。

### 方案 B：keepalived + VIP

```
keepalived-master (priority 100) ──→ holds VIP 10.0.1.100
keepalived-backup  (priority 90)
```

keepalived 运行在每台 MySQL 宿主机上，通过 VRRP 协议浮动 VIP。
`infra/keepalived/` 目录提供 Docker 配置示例。

### 方案 C：云厂商方案

- AWS: RDS Multi-AZ + 自动 DNS 切换
- 阿里云: RDS 高可用版 + 内网域名
- 腾讯云: CDB 高可用版 + 代理地址

## 目录结构

```
infra/
├── mysql-proxy/
│   ├── main.go              # Go 实现的 MySQL 代理
│   ├── Dockerfile           # Docker 镜像构建
│   └── docker-compose.yml   # Docker Compose 配置
├── keepalived/
│   ├── Dockerfile
│   ├── keepalived-master.conf
│   ├── keepalived-replica.conf
│   ├── check_mysql.sh       # MySQL 健康检查脚本
│   └── notify.sh            # 状态变更通知脚本
└── vip-manager.sh           # Docker 环境 VIP 管理脚本
```
