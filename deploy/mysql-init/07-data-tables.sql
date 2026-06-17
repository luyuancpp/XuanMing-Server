-- Pandora data_service 数据网关表结构(2026-06-16)
--
-- 装载方式:容器 entrypoint 自动扫 /docker-entrypoint-initdb.d/*.sql 顺序执行
-- (01-create-databases.sql 先建库 + grant,本文件接着建表)。
--
-- 表清单(对齐 docs/design/go-services.md §2.3 data_service):
--   player_data  玩家数据统一读写网关的版本化 blob(player_id PK,乐观锁 version)
--
-- 约定:
--   - data_service 是内网数据网关,MySQL 是事实源,Redis 仅旁路缓存(cache-aside)
--   - data 列是上层业务序列化好的不透明 bytes(PlayerProfile 等),data_service 不解释内容
--   - 乐观锁:WritePlayer 走 UPDATE ... WHERE player_id=? AND version=? SET version=version+1
--     受影响行 0 → ErrDataVersionMismatch(10001);version=0 视为新建走 INSERT
--   - 与 04-player-tables.sql 的 players 表互补:players 是结构化档案列,
--     player_data 是整块序列化快照(给 data_service cache-aside 网关用)

USE `pandora_player`;

CREATE TABLE IF NOT EXISTS `player_data` (
    `player_id`  BIGINT UNSIGNED NOT NULL,
    `version`    INT             NOT NULL DEFAULT 1 COMMENT '乐观锁版本号,每次写 +1',
    `data`       BLOB            NOT NULL COMMENT '序列化的玩家数据 bytes(不透明)',
    `updated_at` DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (`player_id`)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COMMENT = 'data_service 玩家数据版本化 blob';
