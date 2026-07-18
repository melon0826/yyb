# YYB Go Smallcat Migration

Feature Name: smallcat-migration
Updated: 2026-07-17

## Description

将 YYB Go 从轻量 wx_server 替代升级为对标 smallcat 的完整管理面板。新增控制台鉴权、仪表盘、Docker 一键部署，保留内置 wx_server 和零依赖特性。

## Architecture

```mermaid
graph TD
    Browser["浏览器:8000"] --> Nginx["Gin Router"]
    Nginx --> Auth["Basic Auth Middleware"]
    Auth --> Index["仪表盘 HTML"]
    Auth --> API["API Handlers"]
    API --> QR["QR 扫码登录"]
    API --> Code["/wx/code /wxapp/getCode"]
    API --> Proxy["代理绑定"]
    API --> Store["SQLite Store"]
    Store --> Session["Session 自动续期"]
    Session --> Wechat["微信 API"]
```

**变更点**：
- 新增：Basic Auth 中间件
- 新增：仪表盘 API（/api/dashboard）
- 修改：Dockerfile 支持环境变量
- 不变：QR 扫码、code 获取、代理绑定、session 续期

## Components and Interfaces

### 1. Auth Middleware (`internal/httpapi/auth.go`)

- Basic Auth 中间件，拦截非 /api 路径的页面请求
- 读取环境变量 YYB_USERNAME / YYB_PASSWORD
- 未配置时跳过鉴权（向后兼容）
- 失败计数 + IP 冷却

### 2. Dashboard API (`internal/httpapi/dashboard.go`)

- `GET /api/dashboard` 返回 JSON 格式账号概览
- 统计字段：total, alive, expired, with_proxy
- 账号列表：id, openid, nickname, avatar_url, status, bound_proxy, last_active

### 3. Config 扩展

- 新增 `YYB_USERNAME` / `YYB_PASSWORD` 环境变量
- 可选 `YYB_PORT` 覆盖默认 8000
- CLI 参数优先级高于环境变量

### 4. 前端仪表盘 (`resource/templates/index.html`)

- 顶部统计卡片（总数/在线/离线/代理）
- 账号表格列表
- 实时刷新（10 秒轮询）
- 代理配置内联编辑

## Data Models

`wechat_accounts` 表新增 `disabled` 字段：

| 字段 | 类型 | 用途 |
|------|------|------|
| id | INTEGER | 主键 |
| openid | TEXT | 微信 openid |
| nickname | TEXT | 昵称 |
| avatar | TEXT | 头像本地路径 |
| status | TEXT | alive/expired/pending |
| bound_proxy | TEXT | 账号绑定代理 |
| disabled | INTEGER | 禁用状态 (0/1, 默认0) |
| login_buffer | TEXT | session 数据 |
| last_checked_at | INTEGER | 最后活跃时间 |

新增 store 方法：
- `SetAccountDisabled(ctx, id, disabled)` - 设置账号禁用状态
- `SetAccountAlias(ctx, id, alias)` - 设置备注名
- `BatchAccountStatus(ctx, openids)` - 批量查询账号状态

## API Endpoints

| 方法 | 路径 | 鉴权 | 说明 |
|------|------|------|------|
| GET | / | Basic Auth | 仪表盘页面 |
| GET | /api/dashboard | 无 | 获取账号概览 JSON |
| GET | /accounts | Basic Auth | 获取账号列表 |
| DELETE | /accounts | Basic Auth | 删除账号 |
| PUT | /accounts/proxy | Basic Auth | 设置账号代理 |
| POST | /accounts/disable | Basic Auth | 切换账号禁用状态 |
| POST | /accounts/remark | Basic Auth | 修改账号备注名 |
| POST | /accounts/status | Basic Auth | 批量查询账号状态 |
| POST | /qr | Basic Auth | 创建扫码二维码 |
| GET | /qr/* | Basic Auth | 轮询扫码状态 |
| POST | /wx/code | 无 | 获取小程序登录 code (兼容旧格式) |
| POST | /wxapp/getCode | 无 | 新版 code 接口 |
| POST | /wxapp/getPhoneNumber | 无 | 获取手机号 |
| POST | /wxapp/operateWxData | 无 | 通用云函数代理 |
| POST | /wx/getuserinfo | 无 | 获取用户信息 |
| POST | /wx/encryptkey | 无 | 获取加密密钥 |
| POST | /wx/oauth | 无 | OAuth 回调处理 |
| POST | /wx/autoauth | 无 | 自动 OAuth 登录 |
| POST | /wx/heart | 无 | 心跳保活 |
| POST | /wx/qrcodeauth | 无 | 二维码认证回调 |

## Docker Configuration

```bash
# 最简部署（无鉴权）
docker run -d -p 8000:8000 --name yyb-go melon0826/yyb-go

# 带鉴权
docker run -d -p 8000:8000 --name yyb-go \
  -e YYB_USERNAME=admin \
  -e YYB_PASSWORD=123456 \
  -v yyb-data:/app/resource/db \
  melon0826/yyb-go
```

环境变量：

| 变量 | 默认值 | 说明 |
|------|--------|------|
| YYB_USERNAME | (空) | 控制台用户名，空=无鉴权 |
| YYB_PASSWORD | (空) | 控制台密码 |
| YYB_PORT | 8000 | 监听端口 |

## Error Handling

- 鉴权失败：401 + IP 冷却
- 账号 session 过期：标记 expired，仪表盘红色显示
- 微信 API 不可达：/wx/code 返回 502，不崩溃
- 数据库写入失败：返回 500，回滚事务

## Test Strategy

- 单元测试：auth 中间件、dashboard API、环境变量解析
- 集成测试：Docker 镜像启动验证
- 手动测试：青龙脚本对接 QLScriptPublic 签到

## Implementation Order

1. auth 中间件 → 2. dashboard API → 3. 前端仪表盘 → 4. Docker 环境变量 → 5. compose 更新
6. 账号管理 CRUD (disable/remark/status-batch) - 已完成
7. 微信 API 补全 (getuserinfo/encryptkey/oauth/autoauth/heart/qrcodeauth) - 已完成

## smallcat 功能覆盖状态 (28/28)

| 端点 | yyb_go | 状态 |
|------|--------|------|
| GET /api/accounts | GET /accounts | 已有 |
| POST /api/accounts/add | QR扫码 | 已有 |
| POST /api/accounts/delete | DELETE /accounts | 已有 |
| POST /api/accounts/disable | POST /accounts/disable | 新增 |
| POST /api/accounts/remark | POST /accounts/remark | 新增 |
| POST /api/accounts/status | POST /accounts/status | 新增 |
| GET /api/auth/validate | Basic Auth | 已有(不同机制) |
| GET /api/health | GET /health | 已有 |
| POST /api/qr/start | POST /qr | 已有 |
| GET /api/qr/status | GET /qr/*/poll | 已有 |
| POST /wx/code | POST /wx/code | 已有 |
| POST /wx/oauth | POST /wx/oauth | 新增 |
| POST /wx/autoauth | POST /wx/autoauth | 新增 |
| POST /wx/qrcodeauth | POST /wx/qrcodeauth | 新增 |
| POST /wx/encryptkey | POST /wx/encryptkey | 新增 |
| POST /wx/getuserinfo | POST /wx/getuserinfo | 新增 |
| POST /wx/heart | POST /wx/heart | 新增 |
| POST /wx/getphonenumber | /wxapp/getPhoneNumber | 已有(不同路径) |
| GET /api/proxies | N/A | 暂缓(后续) |
| POST /api/proxies/* | N/A | 暂缓(后续) |
| POST /wx/appmsgext | N/A | 暂缓(后续) |
| POST /wx/appmsglike | N/A | 暂缓(后续) |
| POST /wx/cloud | /wxapp/operateWxData | 已有(通用) |
| GET /credits/balance | N/A | smallcat特有(不迁移) |
| GET /credits/ledger | N/A | smallcat特有(不迁移) |
