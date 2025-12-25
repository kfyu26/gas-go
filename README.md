# 燃气表数据监控系统

一个基于 Go 语言开发的燃气表数据监控与统计系统，通过 MQTT 协议接收燃气表脉冲数据，提供实时统计、数据可视化、低气量提醒等功能。

## 功能特性

### 核心功能

- **MQTT 数据采集**：订阅 MQTT 主题，实时接收燃气表脉冲数据
- **数据持久化**：使用 SQLite 数据库存储历史事件数据
- **实时统计**：计算今日、本周、本月、累计用气量
- **燃气表读数**：根据脉冲数自动计算燃气表读数
- **剩余燃气监控**：实时显示剩余燃气量
- **低气量通知**：通过 Telegram Bot 发送低气量预警

### 数据可视化

- 今日 24 小时脉冲趋势图
- 当年 12 个月用气量统计
- 最近事件记录列表
- MQTT 连接状态监控

### 系统管理

- 灵活的配置管理（参数可在线修改）
- 数据校准功能（支持重置基准值）
- 调试工具（单条/批量插入数据、删除数据）
- 数据导入页面
- JWT 登录认证（保护配置和调试功能）

## 技术栈

| 类别        | 技术选型                          |
| ----------- | --------------------------------- |
| 语言        | Go 1.22                           |
| Web 框架    | chi v5                            |
| 数据库      | SQLite (modernc.org/sqlite)       |
| MQTT 客户端 | paho.mqtt.golang                  |
| 数值计算    | shopspring/decimal (高精度十进制) |

## 硬件连接示意图

![硬件连接示意图](http://kfyu.free.fr/tc/i/2025/12/23/184639-1.jpg)

该示意图展示了燃气表脉冲传感器与数据采集设备的连接方式，以及整个监测系统的硬件架构布局。

## 快速开始

### 前置要求

- Go 1.22 或更高版本
- MQTT Broker（如 mosquitto、EMQX）

### 安装运行

```bash
# 克隆项目
git clone <repository-url>
cd gasdash-go/V3/backend

# 安装依赖
go mod download

# 运行服务
go run main.go
```

### Docker 部署

```bash
# 构建镜像
docker build -t gasdash-backend .

# 运行容器
docker-compose up -d
```

### 环境变量

| 变量名            | 默认值          | 说明                  |
| ----------------- | --------------- | --------------------- |
| `GAS_SERVER_ADDR` | `:8080`         | HTTP 服务监听地址     |
| `GAS_DB_PATH`     | `./data/gas.db` | SQLite 数据库文件路径 |

## 目录结构

```
gas/
├── main.go          # HTTP 服务入口、路由定义
├── db.go            # SQLite 存储层
├── mqtt.go          # MQTT 客户端工作协程
├── metrics.go       # 脉冲差分统计逻辑
├── settings.go      # 系统配置加载与默认值
├── models.go        # 数据结构与响应模型
├── notify.go        # Telegram 低气量通知逻辑
├── tls.go           # MQTT TLS 配置
├── auth.go          # JWT 认证中间件
├── templates/       # 前端静态页面
│   ├── index.html          # 主面板
│   ├── login.html          # 登录页面
│   └── data-import.html    # 数据导入与校准页面
├── static/          # 静态资源
│   └── chart.js     # Chart.js 可视化库
├── go.mod
├── go.sum
├── Dockerfile
└── docker-compose.yml
```

## API 接口

### 登录认证

```
POST /api/login
GET  /api/auth/status
```

**首次登录（设置管理员）：**

```json
{
  "username": "admin",
  "password": "your_password"
}
```

**响应示例：**

```json
{
  "token": "eyJhbGciOiJIUzI1NiIs...",
  "expires_in": 86400
}
```

**请求头（需要认证的接口）：**

```
Authorization: Bearer <token>
```

**注意：**

- 首次访问会引导设置管理员密码
- Token 有效期为 24 小时
- 环境变量 `GAS_ADMIN_PASSWORD` 可设置初始密码

### 基础指标

```
GET /api/metrics
```

返回今日/本周/本月/累计用气量、燃气表读数、剩余燃气、MQTT 状态等信息。

**响应示例：**

```json
{
  "today_gas": "1.234",
  "week_gas": "5.678",
  "month_gas": "23.456",
  "total_used_gas": "123.456",
  "meter_reading": "1023.456",
  "remain_gas": "876.544",
  "mqtt_status": "connected",
  "last_msg_time": "2024-01-01 12:34:56"
}
```

### 分时统计

```
GET /api/hourly
```

返回当天 24 小时的脉冲数据数组。

### 分月统计

```
GET /api/monthly
```

返回当年 12 个月的脉冲数据数组。

### 配置管理

> ⚠️ 以下接口需要登录认证

```
GET /api/settings
PUT /api/settings
```

获取或更新系统配置。

**配置参数说明：**

| 参数                       | 说明                          |
| -------------------------- | ----------------------------- |
| `gas_per_pulse`            | 每个脉冲对应的燃气量（m³）    |
| `initial_gas`              | 初始剩余燃气量（m³）          |
| `meter_base_m3`            | 燃气表基准读数（m³）          |
| `desired_meter_m3`         | 目标燃气表读数（m³）          |
| `mqtt_host`                | MQTT Broker 地址              |
| `mqtt_port`                | MQTT Broker 端口              |
| `mqtt_tls`                 | 是否启用 TLS                  |
| `tg_threshold`             | 低气量预警阈值（m³）          |
| `tg_notify_times`          | 通知次数限制                  |
| `tg_notify_interval_hours` | 通知间隔（小时）              |
| `tg_api_endpoint`          | Telegram API 端点（支持代理） |

**认证相关配置（存储在数据库）：**

| 参数             | 说明                       |
| ---------------- | -------------------------- |
| `auth_enabled`   | 是否启用认证（true/false） |
| `admin_username` | 管理员用户名               |
| `admin_password` | 管理员密码（bcrypt 加密）  |

### 校准功能

> ⚠️ 需要登录认证

```
POST /api/calibrate
```

重新设置基准值，用于燃气表更换或数据重置场景。

**请求体：**

```json
{
  "initial_gas": "1000.000",
  "meter_base_m3": "123.456",
  "desired_meter_m3": "125.678"
}
```

### 调试接口

> ⚠️ 需要登录认证

```
POST /api/debug/insert-event          # 插入单条事件
POST /api/debug/batch-insert-events    # 批量插入事件
POST /api/debug/delete-event           # 删除指定事件
POST /api/debug/clear-events           # 清空所有事件
GET  /api/debug/events                # 查看事件列表
GET  /api/debug/metrics               # 调试统计数据
```

### 通知测试

> ⚠️ 需要登录认证

```
POST /api/notify/test
```

发送测试通知，验证 Telegram 配置是否正确。

## 数据模型

### Event（事件）

| 字段          | 类型  | 说明                                 |
| ------------- | ----- | ------------------------------------ |
| `timestamp`   | int64 | 事件时间（秒级 Unix 时间戳）         |
| `count`       | int64 | 累计脉冲数（单调递增，可能出现归零） |
| `received_ts` | int64 | 服务接收时间                         |

### Settings（配置）

配置项以键值对形式存储在 `settings` 表中。

### Metrics（指标）

| 字段             | 说明                 |
| ---------------- | -------------------- |
| `today_gas`      | 今日用气量（m³）     |
| `week_gas`       | 本周用气量（m³）     |
| `month_gas`      | 本月用气量（m³）     |
| `total_used_gas` | 累计用气量（m³）     |
| `meter_reading`  | 燃气表当前读数（m³） |
| `remain_gas`     | 剩余燃气量（m³）     |
| `mqtt_status`    | MQTT 连接状态        |
| `last_msg_time`  | 最后消息时间         |

## 指标计算逻辑

### 用气量计算

采用"累计脉冲差值"方式统计：

1. 遍历事件记录，计算相邻事件脉冲差值
2. 当累计值出现回退（如计数器归零）时，使用当前值作为增量
3. 按 day/week/month 的时间窗口聚合

### 燃气表读数与剩余燃气

1. 通过 `events` 计算累计脉冲的总差分值 `totalPulses`
2. 通过 `gas_per_pulse` 转换为用气量
3. 若存在校准：
   - 燃气表读数 = `desired_meter_m3` + 校准后用气量
   - 剩余燃气 = `calibrate_base_gas` - 校准后用气量
4. 若未校准：
   - 燃气表读数 = `desired_meter_m3` + 用气量
   - 剩余燃气 = `initial_gas` - 用气量

## 低气量通知

当剩余燃气低于配置的阈值时，系统会自动发送 Telegram 通知：

- 通知内容包括当前剩余燃气量、预警阈值
- 支持配置通知次数限制和通知间隔
- 通知状态持久化存储，避免频繁重复发送

## MQTT 数据格式

系统订阅的 MQTT 消息格式为 JSON：

```json
{
  "count": 12345,
  "timestamp": 1704067200
}
```

## 许可证

本项目采用 MIT 许可证。

## 贡献

欢迎提交 Issue 和 Pull Request。
