# Requirements Document

## Introduction

将 YYB Go 升级为对标 smallcat 的完整微信小程序账号管理面板。保留现有内置 wx_server 能力，新增控制台鉴权、仪表盘、Docker 一键部署。

## Glossary

- **YYB Go**: 微信小程序账号代理管理服务
- **控制台**: YYB Go 的 Web 管理面板
- **账号**: 已通过扫码登录的微信账号
- **Session**: 微信登录后的会话令牌，用于调用小程序接口
- **code**: 微信小程序临时登录凭证，有效期 5 分钟

## Requirements

### Requirement 1: 控制台登录鉴权

**User Story:** AS 管理员, I want 通过密码登录控制台, so that 未授权用户无法访问管理面板

#### Acceptance Criteria

1. WHEN 服务启动时未配置用户名密码, THE 系统 SHALL 跳过鉴权，所有页面可直接访问（保持向后兼容）
2. WHEN 环境变量 YYB_USERNAME 与 YYB_PASSWORD 均已配置, THE 系统 SHALL 对除 /api 外的所有页面请求进行 Basic Auth 鉴权
3. WHEN 用户访问受保护页面且未认证, THE 系统 SHALL 返回 401 并弹出浏览器标准登录对话框
4. IF 连续 5 次认证失败, THE 系统 SHALL 拒绝同 IP 的请求 60 秒

### Requirement 2: 仪表盘

**User Story:** AS 管理员, I want 在首页看到所有账号概览, so that 快速了解系统运行状态

#### Acceptance Criteria

1. THE 仪表盘 SHALL 显示账号总数、在线数、离线数、代理绑定数
2. THE 仪表盘 SHALL 展示每个账号的昵称、头像、openid、状态、代理、最后活跃时间
3. WHEN 账号状态变更（上线/下线/过期）, THE 仪表盘 SHALL 在 10 秒内自动刷新

### Requirement 3: Docker 一键部署

**User Story:** AS 运维, I want 一条 docker run 命令启动服务, so that 无需手动编译或配置

#### Acceptance Criteria

1. THE Docker 镜像 SHALL 通过环境变量 YYB_USERNAME、YYB_PASSWORD、YYB_PORT 进行配置
2. WHEN 仅执行 `docker run -d -p 8000:8000 yyb-go`, THE 服务 SHALL 正常启动且无鉴权
3. THE 镜像 SHALL 通过 volume 挂载持久化 SQLite 数据库

### Requirement 4: 脚本 API 保持无鉴权

**User Story:** AS 青龙脚本用户, I want 直接调用 /wx/code 和 /wxapp/getCode, so that 无需额外配置 token

#### Acceptance Criteria

1. THE /wx/code 接口 SHALL 无需认证即可访问
2. THE /wxapp/getCode 接口 SHALL 无需认证即可访问
3. THE 接口返回格式 SHALL 保持与旧版 wx_server 完全兼容

### Requirement 5: 账号自动续期

**User Story:** AS 管理员, I want 已登录账号自动保持在线, so that 无需频繁重新扫码

#### Acceptance Criteria

1. WHILE 账号状态为 alive, THE 系统 SHALL 每 25 分钟自动刷新 session
2. WHEN session 刷新失败, THE 系统 SHALL 标记账号为 expired 并在仪表盘显示
3. THE 系统 SHALL 在日志中记录每次刷新结果

### Requirement 6: 多账号代理隔离

**User Story:** AS 管理员, I want 不同账号走不同代理, so that IP 不冲突

#### Acceptance Criteria

1. WHEN 账号配置了 bound_proxy, THE 系统 SHALL 优先使用该代理
2. WHEN 账号未配置代理但服务配置了全局 --tcp-proxy, THE 系统 SHALL 使用全局代理
3. WHEN 两者均未配置, THE 系统 SHALL 直连

### Requirement 7: 账号管理 CRUD

**User Story:** AS 管理员, I want 通过 API 管理账号生命周期, so that 可以在面板上完成增删改查

#### Acceptance Criteria

1. THE 系统 SHALL 支持 POST /accounts/disable 切换账号禁用状态 (openid + disabled bool)
2. THE 系统 SHALL 支持 POST /accounts/remark 修改账号备注名 (openid + displayName)
3. THE 系统 SHALL 支持 POST /accounts/status 批量查询账号状态 (openid 数组)
4. THE 系统 SHALL 支持 DELETE /accounts 删除账号 (已实现)
5. THE 数据库 SHALL 包含 disabled 字段用于标记禁用状态

### Requirement 8: 微信 API 补全

**User Story:** AS 青龙脚本用户, I want 通过标准微信 API 接口调用各项功能, so that 脚本开发和调试更方便

#### Acceptance Criteria

1. THE 系统 SHALL 提供 POST /wx/getuserinfo 获取用户信息 (ref + app_id + encrypted_data + iv)
2. THE 系统 SHALL 提供 POST /wx/encryptkey 获取加密密钥 (ref + app_id)
3. THE 系统 SHALL 提供 POST /wx/oauth 处理 OAuth 回调 (code + state + ref)
4. THE 系统 SHALL 提供 POST /wx/autoauth 自动 OAuth 登录 (code + app_id + ref)
5. THE 系统 SHALL 提供 POST /wx/heart 心跳保活端点
6. THE 系统 SHALL 提供 POST /wx/qrcodeauth 二维码认证回调处理
7. THE 系统 SHALL 提供 POST /wx/code 获取小程序登录 code (已实现，兼容旧格式)
8. 以上 /wx/* 端点 SHALL 无需 Basic Auth 鉴权
