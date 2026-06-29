# 决策:列表类 RPC 统一游标分页

> 状态:已落地(2026-06-29)
> 范围:mail / guild / trade 三个全量返回的列表接口
> 决策人:人拍板,Claude 实现 + 验证

## 1. 旧问题

以下列表 RPC 一次性全量返回,无翻页,稳态高基数会撑大单条 message、放大序列化与 Envoy 转发尾延迟,也存在被刷成放大攻击的风险:

- 邮件 `ListMail`:全收件箱一把梭(只有 `player_id`)
- 公会成员 `ListMembers` / 申请 `ListJoinRequests`:全员/全申请返回
- 交易订单 `ListMyOrders`:仅 `active_only`,无翻页

排行榜 `GetRange` 已有 `offset+limit`,拍卖 `ListMarket` 已有 `limit`,好友/聊天/战斗历史均已分页,不在本轮。排行榜底座 Redis ZSET 原生 `ZREVRANGE offset count` + `ZCARD`,翻页近零成本,无需改。

## 2. 新方案:游标分页(cursor + limit → next_cursor)

业务 ID 为 snowflake `uint64` 单调(§9.11),用游标比 `offset` 更稳(插入不错位):

- 请求加 `uint64 cursor`(0=首页)+ `int32 limit`(0=服务端默认,按上限收敛)
- 响应加 `uint64 next_cursor`(0=无更多)
- 默认 50,上限 100;游标方向按各表自然排序(mail/order 按 id DESC,member/request 按 id ASC)

mail 特殊:三类合并视图。系统/公会邮件靠 watermark 天然有界,仅首页(cursor=0)拼接;翻页仅对个人邮件(`mail_id < cursor` DESC),消灭无界增长。

## 3. 迁移成本

新增字段全为可选,旧客户端不传 = 首页默认上限,向后兼容。proto 改动需同步 UE 仓库(Codex,标 `[proto]`)。

## 4. 验收

- limit=0 取默认 50、>100 收敛到 100;cursor 透传 next_cursor 可连续翻页;无更多返回 0
- 各服务 build/vet/test 通过
