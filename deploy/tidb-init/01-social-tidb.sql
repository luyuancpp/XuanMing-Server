-- Pandora 社交库表结构 —— TiDB 版(friend 服务迁 TiDB,2026-06-18)
--
-- 背景:好友图扩容存储路线拍板 = (A) TiDB
--   (docs/design/friend-distributed-scaling.md §8 / §14)。
--   人工拍板「真把好友服切到 TiDB」覆盖原「不提前引入」,见 pandora-arch.md §11。
--
-- 与 deploy/mysql-init/06-social-tables.sql 的差异(只改 DDL,业务 SQL / Go 代码不变):
--   1. friendships / blocks 代理主键 id 从 AUTO_INCREMENT 改 AUTO_RANDOM —— 打散写热点
--      (§8.2:TiDB 单调主键集中写同一 Region → 热点;AUTO_RANDOM 高位随机分散)。
--      Go data 层不读 id / 不依赖 LastInsertId(friend_repo.go 全走 INSERT IGNORE +
--      player_id 查询),故改随机主键无副作用。
--   2. friend_requests / chat_private_messages 主键是显式雪花 ID(uint64,业务 ID 不变量
--      §9.11 不能改),无法用 AUTO_RANDOM。改用「主键 NONCLUSTERED + SHARD_ROW_ID_BITS
--      + PRE_SPLIT_REGIONS」:行实际按随机 _tidb_rowid 落盘,避开雪花时间序写热点;
--      代价是按 request_id / message_id 的点查多一次回表(这两表点查频率低,可接受)。
--   3. ENGINE=InnoDB / 字符集声明 TiDB 兼容接受;collation 用 utf8mb4_bin
--      (全部业务键是数值 BIGINT,大小写不敏感无意义;utf8mb4_bin 全 TiDB 版本可用,
--      避免老版本不支持 utf8mb4_0900_ai_ci)。
--
-- 跨人强一致保留:AcceptRequest 的 BEGIN / SELECT...FOR UPDATE / 多表写 / COMMIT
--   在 TiDB 悲观事务下跨节点原生可跑,maxFriends 硬上限语义不变(§8.1)。
--
-- 装载:TiDB 集群就绪后由 Codex / 人执行(见本目录 README / PROGRESS Codex 交接);
--   mysql -h <tidb-host> -P 4000 -u root < 01-social-tidb.sql
--   不进 mysql-init 自动装载流程(那条线连的是单 MySQL 容器)。

CREATE DATABASE IF NOT EXISTS `pandora_social`
    DEFAULT CHARACTER SET utf8mb4
    DEFAULT COLLATE utf8mb4_bin;

USE `pandora_social`;

-- 好友关系(双向各一行,uk player_id+friend_id,便于 ListFriends 单表查)。
-- 代理主键 id 用 AUTO_RANDOM 打散写热点;Go 侧不读 id。
CREATE TABLE IF NOT EXISTS `friendships` (
    `id`         BIGINT UNSIGNED NOT NULL AUTO_RANDOM,
    `player_id`  BIGINT UNSIGNED NOT NULL COMMENT '关系归属玩家',
    `friend_id`  BIGINT UNSIGNED NOT NULL COMMENT '好友玩家',
    `created_at` DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '成为好友时间(since_ms 来源)',
    PRIMARY KEY (`id`),
    UNIQUE KEY `uk_player_friend` (`player_id`, `friend_id`),
    KEY `idx_player` (`player_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin
  COMMENT='Pandora 好友关系(双向各一行,TiDB)';

-- 好友请求。主键是显式雪花 request_id,改 NONCLUSTERED + 行 ID 随机分片避热点。
CREATE TABLE IF NOT EXISTS `friend_requests` (
    `request_id`   BIGINT UNSIGNED NOT NULL COMMENT 'snowflake 好友请求 ID(uint64)',
    `requester_id` BIGINT UNSIGNED NOT NULL COMMENT '发起方',
    `target_id`    BIGINT UNSIGNED NOT NULL COMMENT '接收方',
    `status`       TINYINT         NOT NULL DEFAULT 1 COMMENT '1 pending / 2 accepted / 3 rejected / 4 expired',
    `created_at`   DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
    `updated_at`   DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (`request_id`) /*T![clustered_index] NONCLUSTERED */,
    UNIQUE KEY `uk_requester_target` (`requester_id`, `target_id`),
    KEY `idx_target_status` (`target_id`, `status`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin
  COMMENT='Pandora 好友请求(挂起 / 接受 / 拒绝,TiDB)'
  SHARD_ROW_ID_BITS=4 PRE_SPLIT_REGIONS=4;

-- 黑名单。代理主键 id 用 AUTO_RANDOM 打散写热点。
CREATE TABLE IF NOT EXISTS `blocks` (
    `id`         BIGINT UNSIGNED NOT NULL AUTO_RANDOM,
    `player_id`  BIGINT UNSIGNED NOT NULL COMMENT '拉黑发起方',
    `blocked_id` BIGINT UNSIGNED NOT NULL COMMENT '被拉黑玩家',
    `created_at` DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uk_player_blocked` (`player_id`, `blocked_id`),
    KEY `idx_player` (`player_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin
  COMMENT='Pandora 黑名单(TiDB)';

-- chat 私聊历史。主键是显式雪花 message_id,同 friend_requests 处理(NONCLUSTERED + 分片)。
-- 与好友图同库 pandora_social,迁 TiDB 时一并迁,避免拆库。
CREATE TABLE IF NOT EXISTS `chat_private_messages` (
    `message_id`   BIGINT UNSIGNED NOT NULL COMMENT 'snowflake 消息 ID(uint64)',
    `sender_id`    BIGINT UNSIGNED NOT NULL COMMENT '发送方玩家',
    `receiver_id`  BIGINT UNSIGNED NOT NULL COMMENT '接收方玩家',
    `content`      VARCHAR(512)    NOT NULL COMMENT '消息内容(服务端已校验长度 + 敏感词)',
    `send_time_ms` BIGINT          NOT NULL COMMENT '发送时间(毫秒,排序 / 翻页游标)',
    PRIMARY KEY (`message_id`) /*T![clustered_index] NONCLUSTERED */,
    KEY `idx_pair_time` (`sender_id`, `receiver_id`, `send_time_ms`),
    KEY `idx_receiver_time` (`receiver_id`, `send_time_ms`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin
  COMMENT='Pandora 私聊历史(离线 PullHistory,TiDB)'
  SHARD_ROW_ID_BITS=4 PRE_SPLIT_REGIONS=4;
