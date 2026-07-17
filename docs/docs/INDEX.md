# YYB Go 文档索引

## 1. 文档说明

本目录保存 YYB Go 项目的阅读文档，内容基于当前工作区中归档源码整理，适合用于项目理解、联调测试和后续扩展设计。

当前源码来源：

- `当前工作区/.monkeycode-tmp-files/30d199ef-yyb_go(2)-1.rar`

## 2. 阅读顺序

建议按下面顺序阅读：

1. `PROJECT_OVERVIEW.md`
   用于快速理解项目定位、技术栈、目录结构和整体能力。

2. `ARCHITECTURE.md`
   用于理解系统分层、模块关系、主流程和数据流向。

3. `API_SUMMARY.md`
   用于联调接口、确认请求结构和错误语义。

4. `LOGIN_AND_SESSION.md`
   用于理解扫码授权、`login_buffer`、协议会话和缓存机制。

5. `EXTENSION_PROPOSAL.md`
   用于评估后续改造方向，特别是账号级代理、来源 IP 限制和安全补强。

## 3. 文档地图

### `PROJECT_OVERVIEW.md`

覆盖内容：

- 项目目标
- 技术栈
- 目录结构
- 主要模块
- 运行方式
- 登录与 IP 的关系

### `ARCHITECTURE.md`

覆盖内容：

- 系统分层
- 核心模块职责
- 关键时序
- 数据存储模型
- 网络出口与代理位置

### `API_SUMMARY.md`

覆盖内容：

- JSON 返回结构
- 账号引用规则
- 扫码接口
- 账号管理接口
- wxapp 调用接口
- 建议测试顺序

### `LOGIN_AND_SESSION.md`

覆盖内容：

- 扫码授权链路
- 凭据提取方式
- `login_buffer` 作用
- 会话建立机制
- 会话缓存与恢复
- 服务端出口 IP 关系

### `EXTENSION_PROPOSAL.md`

覆盖内容：

- 当前边界
- 扩展优先级
- 账号级代理方案
- 来源 IP 控制方案
- 最小改造建议

## 4. 面向不同读者的入口

### 项目使用者

优先阅读：

1. `PROJECT_OVERVIEW.md`
2. `API_SUMMARY.md`

### 联调与测试人员

优先阅读：

1. `API_SUMMARY.md`
2. `LOGIN_AND_SESSION.md`

### 后端开发者

优先阅读：

1. `ARCHITECTURE.md`
2. `LOGIN_AND_SESSION.md`
3. `EXTENSION_PROPOSAL.md`

### 需求与改造负责人

优先阅读：

1. `PROJECT_OVERVIEW.md`
2. `ARCHITECTURE.md`
3. `EXTENSION_PROPOSAL.md`

## 5. 当前结论摘要

这个项目是一个“微信扫码接入账号 + 服务端持有登录态 + 代调用 wxapp 能力”的轻量控制台。

当前能力重点在：

- 快速接入账号
- 复用服务端会话
- 发起受控接口调用

当前主要改进空间在：

- 鉴权
- 账号级代理出口
- 来源 IP 访问控制
- 审计日志
