# YYB Go 登录与会话机制说明

## 1. 文档目的

本文档解释 YYB Go 如何完成扫码登录、凭据持久化、`login_buffer` 获取、协议会话建立以及后续复用。这部分是整个项目最关键的运行链路。

## 2. 总体思路

项目并不实现一个传统的“用户名密码登录后台”。它采用的是“手机微信扫码授权 + 服务端持有登录态”的模式。

完整链路分成两层：

1. 用户授权层
   - 用户用手机微信扫码并确认
2. 服务端会话层
   - 服务端拿到授权后的凭据
   - 服务端换取 `login_buffer`
   - 服务端建立协议会话并缓存
   - 服务端继续代调用 wxapp 能力

## 3. 扫码授权链路

### 3.1 创建二维码

`internal/qr/qr.go` 中的 `CreateSession` 会：

1. 创建带 CookieJar 的 HTTP 客户端。
2. 请求微信 OAuth 页面。
3. 从页面内容中解析二维码 UUID。
4. 生成一个本地 `session_id` 用于前端轮询。

随后 `FetchQRCodeImage` 根据二维码地址拉取图片，前端通过 `/qr/{session_id}/image` 显示。

### 3.2 轮询扫码状态

前端周期性请求 `/qr/{session_id}/poll`，服务端调用 `PollQRCode` 向微信长轮询接口确认状态。

状态流转大致如下：

- `pending`
- `scanned`
- `authorized`
- `confirmed`

异常状态：

- `expired`
- `cancelled`
- `unknown`

### 3.3 确认授权并获取凭据

当扫码状态进入 `authorized` 后，前端调用 `/qr/{session_id}/confirm`。

此时服务端会：

1. 访问 OAuth 回调地址。
2. 从 CookieJar 中读取：
   - `openid`
   - `accesstoken`
   - `refreshtoken`
3. 组装 `LoginBufferCredentials`。
4. 调用登录缓冲接口换取 `login_buffer`。

最终得到两份关键数据：

- 凭据集合 `credentials`
- `login_buffer`

## 4. 为什么 `login_buffer` 重要

`login_buffer` 是项目后续建立协议会话的核心输入。代码中后续所有 wxapp 能力调用都围绕它展开。

项目并不在每次请求时都重新做扫码授权，而是：

1. 扫码得到一次可用的 `login_buffer`
2. 基于它建立更底层的协议会话
3. 把会话缓存起来重复使用

## 5. 账号入库与持久化

在 `handleQR -> confirm -> storeFromScan` 这条路径里，账号会被写入 `wechat_accounts` 表。

主要持久化字段包括：

- `openid`
- `uin`
- `nickname`
- `avatar`
- `user_info`
- `login_buffer`
- `credentials`
- `status`

这里的 `credentials` 是后续刷新登录态的基础。

## 6. 会话建立机制

### 6.1 触发时机

调用 `/wxapp/getCode`、`/wxapp/getPhoneNumber`、`/wxapp/operateWxData` 时，都会进入 `internal/protocol/pool.go` 的 `run` 流程。

### 6.2 读取缓存

`Pool.state` 会先查询数据库中的 `sessions` 表：

- 如果已有有效会话，直接复用
- 如果没有，会开始重新建会话

### 6.3 重新建会话

建会话的核心步骤如下：

1. 读取账号记录和 `login_buffer`
2. 解析 `login_buffer` 中的关键字段
3. 获取 LongLink 候选地址
4. 发起 ManualAuth 登录
5. 从响应中提取：
   - `SendKey`
   - `RecvKey`
   - `UIN`
   - `Ticket`
   - `DeviceID`
   - `HostAppID`
   - `PSK`
6. 获取 ShortLink 目标列表
7. 组装为 `WmpfSession`
8. 写入 `sessions` 表缓存

### 6.4 会话缓存粒度

会话缓存按下面的组合隔离：

- `wechat_account_id`
- `tcp_proxy`

这说明：

- 同一账号在不同代理出口下，可以存在不同会话
- 当前代码已经具备“基于网络出口隔离会话”的基础设计

## 7. 会话失效与恢复

### 7.1 普通失效

如果已有会话调用失败，服务端会：

1. 使当前会话失效
2. 尝试重新建立会话
3. 再执行一次请求

### 7.2 凭据失效

如果 `credentials` 已无法刷新，项目会把账号状态标记为 `expired`。这时调用接口通常会返回冲突错误，提示需要重新扫码。

### 7.3 刷新存活状态

`/accounts/refresh` 的逻辑会：

1. 使用保存的 `refresh_token` 刷新 access token
2. 再次换取新的 `login_buffer`
3. 更新数据库中的凭据和状态

成功时状态为 `alive`，失败时通常会变成 `expired` 或 `unknown`。

## 8. 登录态与服务器 IP 的关系

这是理解该项目部署方式的关键点。

### 8.1 用户手机的作用

用户手机只负责：

- 扫码
- 确认授权

### 8.2 服务端的作用

服务端负责：

- 访问 OAuth 回调
- 持有 Cookie 与 token
- 换取 `login_buffer`
- 建立底层协议会话
- 执行后续 wxapp 调用

因此，从系统行为看，后续业务访问主要发生在服务端网络出口上。

### 8.3 代理支持

项目支持全局 `tcp_proxy`，支持：

- `socks5://host:port`
- `http-connect://host:port`

如果设置了代理，则相关 TCP 连接优先走代理出口。

## 9. 当前安全边界

从当前代码可以确认：

- 没有后台登录鉴权
- 没有账号级来源 IP 限制
- 没有权限模型
- 没有操作审计

因此，当前设计更适合：

- 本地测试环境
- 受控内网环境
- 小范围工具使用

## 10. 关键结论

可以把这个项目的登录与会话机制概括为：

1. 手机扫码负责授权入口。
2. 服务端负责持有和刷新登录态。
3. `login_buffer` 是建立协议会话的关键凭据。
4. 协议会话会被缓存并复用。
5. 缓存维度已经包含 `tcp_proxy`，为后续账号级出口策略提供了实现基础。
