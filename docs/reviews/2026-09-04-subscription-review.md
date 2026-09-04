# 2026-09-04 订阅代码 Review 与修复验收

## 当前分支

```text
3a9f41ee8  最新 origin/main / upstream/main
└─ 8fcd80a8f  重放后的原有 OOIOO 二开提交，当前本地 prod
   └─ 本次修复提交：双订阅修复、测试与少量 lint 整理
```

- 原提交 `b734c0e82` 保存在本地备份分支 `codex/backup-prod-before-rebase-20260904`。
- 原有未提交文件内容在 rebase 前后逐文件 SHA-256 一致；仅更新提交基底与索引。
- 初审时双订阅改动未提交；两个问题修复并完成验证后，用户授权将其作为独立 OOIOO 提交保存。未 push 或部署。

## 已修复：P2 删除旧周期记录会破坏当前周期的回退基线

初审位置（修复前）：`model/subscription.go:1113-1116`，`AdminDeleteUserSubscription`。

删除历史订阅时，按相同 `prev_user_group` 和更晚的时间批量更新所有后续记录，无法区分后续记录引用的是被删记录还是同组的新订阅。复现数据：旧国产订阅从 default 开始并已过期；后来新国产订阅从 premium 开始；新 Codex 订阅接在新国产订阅后。删除旧国产记录会将新 Codex 的 `prev_user_group` 从国产组误改为 default。当前两份订阅结束后，用户最终变成 default；不删除该历史记录的对照组能正确恢复 premium。

建议只重连实际引用被删订阅的前驱关系，不能按组名覆盖后续独立周期。

## 已修复：P2 历史同名订阅会覆盖人工设置的回退分组

初审位置（修复前）：`model/subscription.go:533-536`，`subscriptionDowngradeTargetTx`。

查找历史前驱只约束组名和开始时间，未验证该历史订阅在当前订阅购买时是否仍提供分组权益。复现数据：某组的旧订阅已结束，管理员之后人工将用户设回该组；再购买新套餐时，记录正确保存了该人工分组为 `prev_user_group`。取消新套餐时，新实现继续追溯早已结束的旧订阅，将账户错误降到 default。重放后的原有二开基线在同一用例下保留人工分组，当前未提交实现则失败。

建议只追溯购买时实际授予分组的订阅前驱，保留独立人工分组设置，不以“历史上出现过同名组”推断归属。

## 初审复现与测试

- 重放后的二开基线：`go test ./... -count=1 -timeout=180s`，40 个有测试的包通过。
- 当前未提交代码原有测试：相同命令，40 个有测试的包通过。
- 补充最小用例：上述两个问题均失败，不删除历史记录的对照用例通过。
- 人工回退组用例在重放后的二开基线通过，证明第二项是未提交改动引入的回归。
- 复现使用本地 SQLite 与 Go overlay，没有修改业务代码、生产数据，也没有将临时失败测试插入正常测试目录。

复现命令：

```sh
go test -overlay /Users/simonsun/.codex/backups/newapi-rebase-review-220dkhxq/review-overlay.json ./model -run '^TestReview' -count=1 -v
```

复现源码与初审日志保存在同一备份目录。当时结论为先修复这两项，再提交双订阅改动；当前两项已修复，见下方最新验收。


## 当前修复与验收

- 用 `subscriptionGroupPredecessorTx` 统一识别前驱：限定同一用户、相同目标分组、早于当前购买的时间/ID，并要求旧订阅的结束时间严格晚于当前购买时间。
- 删除订阅时只重连前驱 ID 确实等于被删记录的后续订阅；不再按组名批量覆盖其他周期。
- 取消订阅前先保存相关后续记录的基线，避免将结束时间改成当前秒后丢失同秒购买的关系。
- 续购从原记录的购买时间解析基线，避免旧前驱在续购前到期后被错误当作独立分组；新套餐仍使用自己的显式降级策略。
- 取消、删除、到期处理统一先锁用户，再处理订阅记录，与购买路径保持锁顺序一致。
- 没有新增数据库字段或表；两个初审问题和新增边界已纳入 `model/subscription_group_lineage_test.go`，并接入真实数据库矩阵。

最新验证：

| 验证 | 结果 |
| --- | --- |
| `go test ./... -count=1 -timeout=180s` | 40 个有测试的包通过 |
| `go build ./...` | 通过 |
| 原始 review overlay 的两个缺陷用例及对照用例 | 全部通过 |
| SQLite 3.50.4 | 正式订阅回归通过 |
| MySQL 8.4.11 | 正式订阅回归与新增 9 个边界场景通过 |
| PostgreSQL 17.11 / UTF8 | 正式订阅回归与新增 9 个边界场景通过 |
| `git diff --check` | 通过 |

三数据库验证沿用原环境中的工具，仅新建本机临时数据库；未访问生产数据。MySQL/PostgreSQL 使用下列命令运行并实际连接专用库，未跳过：

```sh
TEST_MYSQL_DSN='root@tcp(127.0.0.1:53339)/newapi_lineage?charset=utf8mb4&parseTime=true&loc=Local' \
TEST_POSTGRES_DSN='host=127.0.0.1 port=55439 user=matrix dbname=newapi_lineage sslmode=disable' \
go test ./model -run '^TestSubscriptionDatabaseMatrix$' -count=1 -timeout=180s -v
```

临时数据库实例已关闭、测试数据目录已删除。

最新日志目录：`/Users/simonsun/.codex/backups/newapi-lineage-fix-jj9qprwq`。
修复提交位于原有二开 `8fcd80a8f` 之后，底部官方基线仍为 `3a9f41ee8`；未 push 或部署。
