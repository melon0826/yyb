# YYB Go 项目介绍

## 1. 项目概览

YYB Go 是一个基于 Go 开发的 Web 控制台，用于通过微信扫码接入账号，并在服务端代调用若干微信小程序相关能力。项目同时提供浏览器管理界面和 OpenAPI 风格接口，适合在受控环境中做账号接入、会话复用和接口联调测试。

从代码结构看，这个项目的核心目标有三件事：

1. 生成微信扫码登录二维码，并轮询扫码状态。
2. 在用户手机确认后，由服务端接收并保存可复用的账号凭据。
3. 基于保存的登录态，调用 `getCode`、`getPhoneNumber`、`operateWxData` 等 wxapp 能力。

当前工作区里保存的是一个压缩包，实际源码内容位于归档 `当前工作区/.monkeycode-tmp-files/30d199ef-yyb_go(2)-1.rar` 中。本说明基于归档内源码阅读整理。

## 2. 技术栈

- 语言：Go 1.23
- Web 框架：Gin
- 数据库：SQLite
- 接口文档：Swagger UI + OpenAPI JSON
- 前端形态：服务端直接提供的静态 HTML 页面

依赖定义见 `go.mod`，主依赖包括：

- `github.com/gin-gonic/gin`
- `github.com/swaggo/http-swagger/v2`
- `modernc.org/sqlite`

## 3. 项目结构

项目主要目录如下：

```text
cmd/yyb-go/                 程序入口
internal/httpapi/           HTTP 路由、页面、OpenAPI、资源处理
internal/qr/                微信扫码登录流程
internal/protocol/          登录态转换、长短链路协议调用、代理拨号
internal/store/             SQLite 数据访问
resource/templates/         控制台页面与扫码页面
resource/static/            静态资源目录
resource/db/                默认数据库目录
resource/avatars/           下载后的头像文件目录
resource/qr/                临时二维码图片目录
```

## 4. 核心模块说明

### 4.1 程序入口

`cmd/yyb-go/main.go` 负责：

- 解析启动参数
- 构造 `httpapi.Config`
- 初始化应用
- 启动 HTTP 服务
- 处理优雅退出

支持的关键启动参数包括：

- `--host`：监听地址，默认 `127.0.0.1`
- `--port`：监听端口，默认 `8000`
- `--resource-root`：资源目录，默认 `./resource`
- `--db`：SQLite 文件名
- `--tcp-proxy`：可选 TCP 代理，支持 `socks5://` 和 `http-connect://`

### 4.2 HTTP API 与页面

`internal/httpapi/app.go` 是服务入口层，负责注册路由和组织业务流程。项目同时提供管理界面和接口：

- 页面路由
  - `/`：主控制台
  - `/scan`：扫码添加账号页面
  - `/docs`：Swagger UI
  - `/openapi.json`：OpenAPI 描述

- 基础接口
  - `/health`

- 扫码登录接口
  - `POST /qr`
  - `GET /qr/{session_id}/image`
  - `GET /qr/{session_id}/poll`
  - `POST /qr/{session_id}/confirm`

- 账号管理接口
  - `GET /accounts`
  - `DELETE /accounts?ref=...`
  - `GET /accounts/avatar?ref=...`
  - `POST /accounts/refresh`
  - `POST /accounts/resync`

- wxapp 调用接口
  - `POST /wxapp/getCode`
  - `POST /wxapp/getPhoneNumber`
  - `POST /wxapp/operateWxData`

返回格式统一为：

```json
{
  "code": 0,
  "msg": "success",
  "data": {}
}
```

### 4.3 扫码登录模块

`internal/qr/qr.go` 负责整个扫码接入流程：

1. 请求微信 OAuth 页面，解析出二维码标识。
2. 拉取二维码图片并返回给前端。
3. 长轮询扫码状态，识别 `pending`、`scanned`、`authorized`、`confirmed`、`expired`、`cancelled`。
4. 用户确认后，服务端访问回调地址。
5. 从 Cookie 中提取 `openid`、`accesstoken`、`refreshtoken`。
6. 基于这些凭据换取 `login_buffer`。

这个设计说明，扫码只是用户授权入口，真正被系统保存和继续使用的是服务端拿到的账号凭据与 `login_buffer`。

### 4.4 协议与会话模块

`internal/protocol/` 是项目技术含量最高的部分，负责把扫码得到的 `login_buffer` 转成可复用会话，并发起后续 wxapp 调用。

关键职责包括：

- `loginbuffer.go`
  - 换取 `login_buffer`
  - 刷新 access token / refresh token
  - 拉取用户资料

- `pool.go`
  - 维护账号会话池
  - 读取或写入缓存会话
  - 在会话失效时重登
  - 按账号和代理维度复用会话

- `transport.go`
  - 直接 TCP 连接
  - SOCKS5 代理连接
  - HTTP CONNECT 代理连接

- `ilink.go`、`mmtls_*`、`shortlink.go`
  - 构造登录与业务调用报文
  - 建立长链路/短链路请求
  - 处理协议加密、会话密钥和 PSK

### 4.5 数据存储模块

`internal/store/store.go` 使用 SQLite 保存账号和会话信息。

主要表：

- `wechat_accounts`
  - 保存 `openid`、`uin`、昵称、头像、用户信息、`login_buffer`、凭据、状态

- `sessions`
  - 保存账号对应的协议会话缓存
  - 以 `(wechat_account_id, tcp_proxy)` 做唯一约束

- `features`
  - 保存可调用能力开关

默认能力包括：

- `getCode`
- `getPhoneNumber`
- `operateWxData`

## 5. 主要业务流程

### 5.1 添加账号流程

1. 前端访问 `/scan` 页面。
2. 页面调用 `POST /qr` 获取二维码会话。
3. 用户使用手机微信扫码并确认。
4. 前端轮询 `/qr/{session_id}/poll`。
5. 状态进入已授权后，前端调用 `POST /qr/{session_id}/confirm`。
6. 服务端换取 `login_buffer` 并把账号写入数据库。

### 5.2 调用 wxapp 能力流程

1. 前端选择一个已接入账号。
2. 调用 `/wxapp/getCode` 或其他能力接口。
3. 服务端按账号读取会话缓存。
4. 缓存有效时直接调用。
5. 缓存缺失或失效时，服务端基于 `login_buffer` 重新建会话。
6. 返回调用结果给前端。

### 5.3 账号存活刷新流程

1. 调用 `/accounts/refresh`。
2. 服务端尝试刷新凭据并重新换取 `login_buffer`。
3. 根据结果把账号状态标记为 `alive`、`expired` 或 `unknown`。

## 6. 当前实现特征

从源码可以确认出以下实现特征：

- 这是一个面向受控环境的工具型服务。
- 当前没有单独的后台登录鉴权层。
- 当前没有来源 IP 白名单或账号级访问控制。
- 账号会话由服务端持有并复用。
- 服务端支持统一的 TCP 代理出口。
- 会话缓存按账号和代理维度隔离。

这意味着同一账号在不同 `tcp_proxy` 条件下，可以建立不同的缓存会话。

## 7. 登录与 IP 的关系

这个项目的登录态具有明显的服务端属性：

- 用户手机负责扫码和确认。
- 服务端负责接收 OAuth 回调后的凭据。
- 服务端负责换取 `login_buffer`。
- 服务端负责后续所有 wxapp 调用。

因此，后续对微信相关服务的访问出口，取决于：

1. 当前服务器本身的出口网络。
2. 是否配置了 `--tcp-proxy`。

当前实现支持全局代理出口，尚未实现“按账号绑定不同代理”这类更细粒度的网络策略。

## 8. 运行方式

根据现有项目结构，典型运行命令如下：

```bash
go run ./cmd/yyb-go --host 0.0.0.0 --port 8000
```

项目依赖 `resource/` 目录作为运行时资源根目录，通常需要从项目根目录启动。

基础验证命令：

```bash
go test ./...
```

## 9. 适用场景

这个项目适合以下场景：

- 需要通过扫码方式临时接入微信账号
- 需要在服务端复用登录态做接口联调
- 需要一个带页面的轻量账号管理工具
- 需要为内部测试提供 `getCode`、`getPhoneNumber`、`operateWxData` 调用入口

## 10. 当前边界与后续扩展点

基于当前代码，比较自然的扩展方向包括：

- 账号级代理配置
- 账号级 IP 限制或访问控制
- 管理后台鉴权
- 更细粒度的功能开关
- 操作审计日志
- 账号分组与标签能力

其中“指定账户使用指定出口 IP”的扩展具备实现基础，因为现有会话模型已经把 `tcp_proxy` 作为会话维度之一。

## 11. 一句话理解

YYB Go 本质上是一个“微信扫码接入账号 + 服务端持有登录态 + 代调用 wxapp 能力”的轻量控制台服务。
