# 2026-09-04 main 全量整合验证

## 代码范围与提交状态

- 目标上游：`3a9f41ee85cc369f5b8d7fe6e62ff4e7bf3a9ec8`，`origin/main` 与 `upstream/main` 一致。
- 原 `prod`：`b734c0e826276d0d0592aafc7334d3b49f590f02`；共同祖先：`8f6961c675932f406260ff0c218bc2aa0603e9b2`。
- 整合上游分叉后的全部 41 个提交的代码，保留现有 OOIOO 二开及双订阅修复。
- 用户随后授权 rebase：本地 `prod` 已更新为 `8fcd80a8fd170e5f092352827e4a5c9ae6216002`，直接父提交为上述最新官方 main。`git rev-list --left-right --count origin/main...prod` 为 `0 1`。
- 原有 OOIOO 二开及必要兼容调整由 `8fcd80a8f` 承载；双订阅修复在用户授权后作为后续独立 OOIOO 提交保留在官方 main 之上，未 push、未部署。
- 后续 review 发现的两个 P2 回退链问题已修复，并纳入正式测试和真实三数据库验证，见 [订阅代码 review 与修复记录](reviews/2026-09-04-subscription-review.md)。新增修复随本次授权的双订阅修复提交保存。

## 冲突处理与兼容

- 7 个冲突文件按功能整合：新版渠道约束与任务插件路由、类型化日志权限，与原有倍率保护、渠道测试标记并存。
- 日志中的渠道测试标记继续放在管理员权限域，价格保护提示保留在允许公开的字段。
- 原文归档保留在常规 relay 路由，并随 Responses / Suno 等入口迁入新版任务路由；视频二进制内容不额外接入归档。
- 任务表达式计费也经过 API key 的倍率上限校验。
- 双订阅权限、按分组扣费、独立到期/取消/删除、原子退款与陈旧用户缓存保护全部保留。
- 未部署，未修改线上账户、配置或数据。

## 自动验证

- `go test ./... -count=1 -timeout=180s`：通过，40 个有测试的包。
- `GOWORK=off go build ./...`（relaykit 目录）：通过。
- `bun run typecheck`、`bun run build`：通过。
- `bun run test --maxWorkers=2`：65 个测试文件、419 项测试通过。
- 本次涉及的 124 个前端文件 oxlint 检查无 error；全仓 lint 仍有未涉及文件的存量问题，不宣称全仓 lint 通过。
- `git diff --check`：通过。

## 真实数据库验证

| 数据库 | 实测版本 | 验证结果 |
| --- | --- | --- |
| SQLite | 3.50.4（Go 驱动实际返回） | 新库启动两次、旧 prod 数据升级两次、订阅回归通过 |
| MySQL | 8.4.11 | 新库启动两次、旧 prod 数据升级两次、订阅回归及 key/prefill/session 迁移通过 |
| PostgreSQL | 17.11，UTF8 | 新库启动两次、旧 prod 数据升级两次、订阅回归及 key/prefill/session 迁移通过 |

隔离测试实例仅监听本机：MySQL 53339、PostgreSQL 55439。MySQL 8.4 是本次为验证补装的本地工具，未注册开机服务。

执行命令（仅适用于专用测试库）：

```sh
TEST_MYSQL_DSN='root@tcp(127.0.0.1:53339)/newapi_matrix?charset=utf8mb4&parseTime=true&loc=Local' \
TEST_POSTGRES_DSN='host=127.0.0.1 port=55439 user=matrix dbname=newapi_matrix sslmode=disable' \
go test -p 1 ./model ./controller -run 'DatabaseMatrix|Migrate.*Uniqueness|UserSession.*Migration|TokenMigration' -count=1 -timeout=180s -v
```

控制器的旧 key 迁移测试另外用全空的 `newapi_token_migration` 数据库重跑，三个方言均通过，无因已有 tokens 表而跳过。

启动升级验证使用原 prod 和整合后源码分别编译的检查程序，调用 `common.InitEnv()`、`model.InitDB()`、`model.InitLogDB()`。MySQL/PostgreSQL 同时配置独立日志库。旧库预置用户余额、key、倍率上限和有效订阅，升级后逐字段比对一致；新版启动连续执行两次。上游最新发布版本 `v1.0.0-rc.31` 到目标 main 的 model/go.mod/go.sum 无差异，已覆盖其迁移及驱动路径。

## 本地浏览器与接口验证

- 登录正常，任务插件管理页正常列出 10 个出厂插件。
- 同一账户同时选择 `codex-订阅` 与 `国产模型-订阅`，分别成功创建 key；倍率保护随分组正确更新。
- 使用本地模拟上游调用两种 key，均返回 HTTP 200。结算完成后，Codex 请求扣订阅 1 的 3 quota，国产请求扣订阅 2 的 42 quota；另一份套餐未被串扣。
- 两份订阅取消后，钱包仍显示真实保存的“仅用订阅”偏好，并提示“当前无生效订阅，请求将被拒绝”。
- 390×844 移动视口中钱包提示可见，页面宽度为 390px，无横向溢出。
- 浏览器控制台无 error。没有调用真实付费模型或外部视频供应商。
- 临时应用、模拟上游和三库测试实例已关闭，测试数据已清理；原代码备份及验收日志保留。

## 上游提交清单

- `cae3676ec` feat: glm chanel /v1/responses (#7050)
- `ba2e9287b` feat(ollama): passthrough Claude Messages and OpenAI Responses (#7051)
- `e468b7391` docs: update PR template and remove PR Check workflow (#7053)
- `692e8d6ee` fix(web): restore admin unbinding for built-in providers (#6987)
- `ac381acf4` fix(billing): 修复时间规则恒真表达式导致倍率全天生效 (#6934)
- `7037ac15b` fix(docker): add relaykit go.mod to dev build context (#7072)
- `eb48396d5` feat(task): replace built-in task adaptors with a sandboxed JS plugin system (#7076)
- `0f2a2075a` fix(relay): 请求参数校验错误返回 HTTP 400 (#6774)
- `98d50d538` fix(web): recheck setup status after page reload (#6968)
- `b80d633cf` feat(auth): encrypt password login transport
- `8454082f9` feat(chat): add AQBot preset (#7079)
- `918427d8a` feat(auth): make password encryption opt-in #6743
- `6c22550ea` feat(task): resolve channel-mapped aliases and case variants for plugin models
- `66031a09d` fix(model): disable PostgreSQL prepared statements for pooler compatibility
- `0bee5d441` fix(ali): honor image response format (#5513) (#7048)
- `dc4732cfe` feat(web): factory task plugins update only with the system
- `b5b94bc68` fix(subscription): 无有效订阅时前端如实显示「仅用订阅」偏好 (#6222) (#7086)
- `1751f43ee` fix(sqlite): enable WAL + working busy timeout + _txlock=immediate to stop concurrent write lockouts (#7030)
- `6eb6f35ed` fix(model): return string from JSON column Valuers for pg simple protocol
- `b518d0033` fix(relay): bound the wait for upstream response headers (fixes unbounded heap growth → OOM) (#6949)
- `74158715c` fix initialize database
- `69a41eead` fix(model): drop leftover prefill_groups unique constraints before AutoMigrate (#7100)
- `2bf0820f4` Revert "fix(model): drop leftover prefill_groups unique constraints before Au…" (#7101)
- `2b6f1dfef` fix(model): drop leftover prefill_groups unique constraints before AutoMigrate
- `8c8c4153d` fix(log): preserve quota in usage statistics (#7108)
- `27ff6a876` fix(model): migrate legacy token key constraints
- `67a0585d0` fix(docs): correct Video API links across localized READMEs (#7116)
- `b7017c251` fix(model): do not treat no-op system task state writes as lock loss (#7135)
- `0ed497f06` feat(relay): hosted-tool conversion fidelity, reasoning normalization, and billing usage integrity (#7137)
- `bbd97446c` fix(relay): follow-up billing integrity and conversion completions (#7170)
- `aece11d2f` feat(plugin): add MiniMax-H3 /v2 video generation to the hailuo task … (#7168)
- `d8ca0ed0b` chore: let owners use human PR templates
- `73afad588` fix(plugin): account for MiniMax-H3 input media usage (#7171)
- `057f71c23` fix(logs): isolate privileged metadata
- `219c9e063` 优化匿名冷启动与公开内容接口的重复回源请求 (#7166)
- `9f506dd7f` refactor(logs): simplify LogOther projection and dedupe sensitive keys
- `9df450fe5` feat(task): give polling hooks a real query context, host HTTP classification, and bounded poll failures
- `36dbbf0f7` fix: keep ETag valid across different JSON packages
- `8f5ab8e40` fix(ci): resolve release version from trigger tag
- `32c261923` fix(task): explain 503 when a plugin-claimed model has no channel
- `3a9f41ee8` fix: temp disable /messages/count_tokens
