# YYB Go 接口文档摘要

## 1. 文档目的

本文档提供 YYB Go 对外 HTTP 接口的快速摘要，便于测试、联调和二次开发。完整 OpenAPI 描述由服务运行后通过 `/openapi.json` 和 `/docs/index.html` 提供。

## 2. 通用规则

### 2.1 返回结构

所有 JSON 接口统一返回：

```json
{
  "code": 0,
  "msg": "success",
  "data": {}
}
```

字段说明：

- `code`：业务状态码，`0` 表示成功
- `msg`：提示信息
- `data`：实际业务数据

错误时通常返回：

```json
{
  "code": 400,
  "msg": "ref is required",
  "data": null
}
```

### 2.2 账号引用规则

多个接口使用 `ref` 标识账号，支持以下形式：

- 账号 ID
- UIN
- openid

### 2.3 主要错误码

- `400`：参数错误或 JSON 非法
- `404`：资源不存在
- `405`：请求方法错误
- `409`：当前状态不允许操作，例如二维码尚未完成授权，或账号登录态已失效
- `500`：服务端内部错误
- `502`：上游调用失败

## 3. 健康检查

### `GET /health`

用途：检查服务是否存活。

成功响应：

```json
{
  "code": 0,
  "msg": "success",
  "data": {
    "ok": true
  }
}
```

## 4. 扫码登录接口

### `POST /qr`

用途：创建扫码登录会话并生成二维码。

查询参数：

- `as_base64`：可选，`true` 时同时返回 `image_base64`

成功响应字段：

- `session_id`
- `status`
- `image_url`
- `image_base64`

示例：

```json
{
  "code": 0,
  "msg": "success",
  "data": {
    "session_id": "abc123",
    "status": "pending",
    "image_url": "/qr/abc123/image",
    "image_base64": null
  }
}
```

### `GET /qr/{session_id}/image`

用途：获取二维码图片。

响应类型：`image/jpeg`

### `GET /qr/{session_id}/poll`

用途：轮询二维码状态。

状态枚举：

- `pending`：等待扫码
- `scanned`：已扫码，待手机确认
- `authorized`：手机侧已授权
- `confirmed`：服务端已取得 `login_buffer`
- `expired`：二维码过期
- `cancelled`：用户取消
- `unknown`：未知状态

示例：

```json
{
  "code": 0,
  "msg": "success",
  "data": {
    "status": "scanned",
    "errcode": 404
  }
}
```

### `POST /qr/{session_id}/confirm`

用途：确认一个已授权会话，并把账号写入数据库。

成功后返回账号公开信息，包括：

- `id`
- `openid`
- `uin`
- `alias`
- `nickname`
- `avatar`
- `status`
- `created_at`
- `updated_at`

常见失败：

- `409`：二维码尚未授权，无法取回 `login_buffer`

## 5. 账号管理接口

### `GET /accounts`

用途：获取已保存账号列表。

返回：`AccountPublic[]`

每个账号主要字段：

- `id`
- `openid`
- `uin`
- `alias`
- `nickname`
- `avatar`
- `status`
- `last_checked_at`
- `created_at`
- `updated_at`

### `DELETE /accounts?ref=...`

用途：删除指定账号。

必填查询参数：

- `ref`

成功响应：

```json
{
  "code": 0,
  "msg": "success",
  "data": {
    "deleted": 1,
    "openid": "openid-example"
  }
}
```

### `GET /accounts/avatar?ref=...`

用途：获取账号头像。

行为说明：

- 本地头像存在时直接返回图片
- 头像是远程 URL 时返回 `302` 跳转
- 无头像时返回 `404`

### `POST /accounts/refresh`

用途：刷新账号存活状态。

请求体可选：

```json
{
  "ref": "openid-or-id-or-uin"
}
```

行为说明：

- 不传 `ref` 时刷新全部账号
- 传 `ref` 时仅刷新指定账号

响应可能是单对象，也可能是数组。

刷新结果字段：

- `id`
- `openid`
- `uin`
- `nickname`
- `status`

### `POST /accounts/resync`

用途：重新同步账号资料。

请求体可选：

```json
{
  "ref": "openid-or-id-or-uin"
}
```

行为说明：

- 不传 `ref` 时同步全部账号
- 传 `ref` 时同步指定账号

响应返回更新后的账号公开信息，单账号时为对象，全量时为数组。

## 6. wxapp 调用接口

这三类接口都要求使用 `POST`，并且都依赖一个已接入且可用的账号。

### 通用请求字段

- `ref`：账号 ID、UIN 或 openid
- `app_id`：目标小程序 AppID

### `POST /wxapp/getCode`

请求示例：

```json
{
  "ref": "openid-example",
  "app_id": "wx1234567890"
}
```

成功返回：

```json
{
  "code": 0,
  "msg": "success",
  "data": {
    "openid": "openid-example",
    "result": {
      "code": "xxxxx",
      "errMsg": "login:ok"
    }
  }
}
```

### `POST /wxapp/getPhoneNumber`

请求结构与 `getCode` 相同。

返回结果为上游解析后的手机号相关响应对象，实际字段取决于上游返回内容。

### `POST /wxapp/operateWxData`

除 `ref` 和 `app_id` 外，还需要 `payload`：

```json
{
  "ref": "openid-example",
  "app_id": "wx1234567890",
  "payload": {
    "api_name": "getUserInfo",
    "data": {},
    "env": 1
  }
}
```

行为说明：

- `payload` 为完整的请求 JSON
- 服务端只做透传和协议包装

常见失败：

- `400`：缺少 `ref`、`app_id` 或 `payload`
- `409`：账号 `login_buffer` 已失效，需要重新扫码
- `502`：会话建立失败或上游调用失败

## 7. 页面入口

虽然页面不是开放 API，但在测试时经常需要用到：

- `/`：主控制台
- `/scan`：扫码添加账号页面
- `/docs/index.html`：Swagger UI
- `/openapi.json`：OpenAPI 原始描述

## 8. 使用建议

建议的测试顺序：

1. 访问 `/health` 确认服务正常。
2. 打开 `/scan` 添加一个账号。
3. 用 `/accounts` 确认账号已入库。
4. 调用 `/accounts/refresh` 确认状态为 `alive`。
5. 用已接入账号测试 `/wxapp/getCode` 或其他能力。
