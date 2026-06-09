# Pandora battle_result 出箱链验证 stub（W4 ⑨/⑬，2026-06-09）
#
# 用途：在真 UE Battle DS 就绪前，用 grpcurl 同步上报一场战斗结果，验证 battle_result
# 的「事务出箱（transactional outbox）」补偿链（不变量 §4 第二段：DS 崩溃 → 段位回滚）：
#   1) ReportResult（:50022 同步兜底 RPC）→ 同一 MySQL 事务落 battles + battle_player_stats
#      + player_update_outbox 三表
#   2) battle_result 用标准 Elo 重算 mmr_delta（DS 不可信，不变量 §6），覆盖请求里的值
#   3) 后台 RunOutboxPublisher（2s 间隔）把出箱行投到 pandora.player.update topic
#   4) player 服务幂等消费（mmr_history uk = match_id）写回段位
#
# 本脚本负责 1-2 的触发与确认（ReportResult → GetMatchResult 读回算后 mmr_delta），
# 并打印 3-4 的 Kafka / player 服务验证指引。
#
# 前置：
#   - battle_result 已启动（强依赖 MySQL pandora_battle 库；enable_reflection=true，dev 默认开）
#   - 若要验证 outbox → kafka，还需 kafka 可用 + 启动日志出现 player_update_producer_ready
#
# 用法示例：
#   # 正常结算：A 队（team 0）获胜，5v5，幂等复测（连发两次，第二次 already_recorded=true）
#   pwsh tools/scripts/battle_result_outbox_probe.ps1 -MatchId 987654399 -WinnerTeam 0 -Idempotent
#
#   # DS 崩溃补偿：outcome=ABANDONED → battle_result 强制 mmr_delta 全 0（玩家不掉段）
#   pwsh tools/scripts/battle_result_outbox_probe.ps1 -MatchId 987654400 -Outcome ABANDONED
#
#   # 只读回已落库的结果（不再写）
#   pwsh tools/scripts/battle_result_outbox_probe.ps1 -MatchId 987654399 -ReadOnly

[CmdletBinding()]
param(
    # 对局 ID（uint64，幂等键）。同一 MatchId 只会落库一次。
    [string]$MatchId = "987654399",

    # 每队人数（moba 5v5 = 5）。生成 team0 / team1 各 TeamSize 名玩家。
    [int]$TeamSize = 5,

    # 玩家 ID 基址：team0 = Base..Base+TeamSize-1，team1 = Base+TeamSize..
    [string]$BasePlayerId = "30907585000000000",

    # 胜方：0=A 队（team 0）胜，1=B 队（team 1）胜，2=平局
    [int]$WinnerTeam = 0,

    # 结算类型：NORMAL（正常 Elo 算分）/ ABANDONED（DS 崩溃补偿，mmr_delta 强制 0）
    [ValidateSet("NORMAL", "ABANDONED")]
    [string]$Outcome = "NORMAL",

    # 游戏模式 / 地图（写入 battles 行，展示用）
    [string]$GameMode = "moba_5v5",
    [int]$MapId = 2,

    # DS pod 名（写入 battles.ds_pod_name，展示用）
    [string]$DsPodName = "",

    # 连发两次 ReportResult，验证幂等（第二次 already_recorded=true，不重复落库 / 不重复入出箱）
    [switch]$Idempotent,

    # 只调 GetMatchResult 读回已落库结果，不写
    [switch]$ReadOnly,

    # battle_result gRPC 地址（dev 直连）
    [string]$BattleResultAddr = "127.0.0.1:50022"
)

$ErrorActionPreference = "Stop"

if (-not (Get-Command grpcurl -ErrorAction SilentlyContinue)) {
    Write-Host "[ERR] 未找到 grpcurl，请先安装（见 tools/scripts/install_dev_tools.ps1）" -ForegroundColor Red
    exit 1
}

if ([string]::IsNullOrEmpty($DsPodName)) {
    $DsPodName = "pandora-battle-$MatchId"
}

# grpcurl 调用封装：JSON body 从 stdin 读（PowerShell native exe 引号坑，见 team-debug.md §3）。
# -Depth 8：BattleResult.stats 是嵌套数组，默认 depth 2 会把 stats 截断成字符串。
function Invoke-Grpc {
    param([string]$Method, [hashtable]$Body)
    $json = $Body | ConvertTo-Json -Depth 8 -Compress
    $resp = $json | grpcurl -plaintext -d '@' $BattleResultAddr $Method 2>&1
    return ($resp -join "`n")
}

# 构造 2 队 × TeamSize 名玩家的 PlayerStats。
# mmr_delta 请求里填 0：battle_result 会用 Elo 重算并覆盖（不变量 §6），读回时看算后值。
function Build-Stats {
    $stats = @()
    $base = [uint64]$BasePlayerId
    for ($t = 0; $t -lt 2; $t++) {
        for ($i = 0; $i -lt $TeamSize; $i++) {
            $playerId = $base + [uint64]($t * $TeamSize + $i)
            $isWinner = ($t -eq $WinnerTeam)
            $stats += @{
                playerId   = $playerId.ToString()
                heroId     = 1001 + $i
                team       = $t
                kills      = if ($isWinner) { 8 } else { 3 }
                deaths     = if ($isWinner) { 3 } else { 8 }
                assists    = 10
                damageDealt  = "25000"
                damageTaken  = "18000"
                healing      = "0"
                gold         = "12000"
                mmrDelta     = 0
            }
        }
    }
    return $stats
}

function Report-Result {
    $now = [DateTimeOffset]::UtcNow.ToUnixTimeMilliseconds()
    $body = @{
        result = @{
            matchId     = $MatchId
            startedAtMs = ($now - 1800000).ToString()   # 30 分钟前开局
            endedAtMs   = $now.ToString()
            winnerTeam  = $WinnerTeam
            stats       = (Build-Stats)
            dsPodName   = $DsPodName
            gameMode    = $GameMode
            mapId       = $MapId
            outcome     = "BATTLE_OUTCOME_$Outcome"
        }
    }
    return Invoke-Grpc -Method "pandora.battle.v1.BattleResultService/ReportResult" -Body $body
}

function Get-MatchResult {
    $body = @{ matchId = $MatchId }
    return Invoke-Grpc -Method "pandora.battle.v1.BattleResultService/GetMatchResult" -Body $body
}

Write-Host "===== battle_result 出箱链验证 =====" -ForegroundColor Cyan
Write-Host "  match=$MatchId winnerTeam=$WinnerTeam outcome=$Outcome teamSize=$TeamSize" -ForegroundColor Cyan
Write-Host "  addr=$BattleResultAddr" -ForegroundColor DarkGray
Write-Host ""

if (-not $ReadOnly) {
    Write-Host "----- ① ReportResult（事务落库 + 入出箱）-----" -ForegroundColor Yellow
    $r1 = Report-Result
    Write-Host $r1
    Write-Host ""

    if ($Idempotent) {
        Write-Host "----- ②  ReportResult 再发一次（验幂等，期望 alreadyRecorded=true）-----" -ForegroundColor Yellow
        $r2 = Report-Result
        Write-Host $r2
        if ($r2 -match '"alreadyRecorded"\s*:\s*true') {
            Write-Host "  [OK] 幂等命中：同一 match_id 未重复落库 / 未重复入出箱" -ForegroundColor Green
        }
        else {
            Write-Host "  [WARN] 未见 alreadyRecorded=true，请检查 battle_player_stats uk_match_player" -ForegroundColor Yellow
        }
        Write-Host ""
    }
}

Write-Host "----- ③ GetMatchResult（读回落库结果 + Elo 算后 mmr_delta）-----" -ForegroundColor Yellow
$g = Get-MatchResult
Write-Host $g
Write-Host ""

# 简单核对 outcome 语义
if ($Outcome -eq "ABANDONED") {
    # proto3 省略 0 值:ABANDONED 时 mmrDelta 全 0 会被 JSON 整字段省略,所以「字段缺失」恰是
    # 正确情况。判据改为:stats 存在(有 playerId)且没有任何非 0 mmrDelta,即视作通过。
    $playerCount = ([regex]::Matches($g, '"playerId"')).Count
    $deltas = ([regex]::Matches($g, '"mmrDelta"\s*:\s*(-?\d+)') | ForEach-Object { [int]$_.Groups[1].Value })
    $nonZero = $deltas | Where-Object { $_ -ne 0 }
    if ($playerCount -eq 0) {
        Write-Host "  [WARN] GetMatchResult 未读回任何 stats,无法核对 ABANDONED(请确认 match 已落库)" -ForegroundColor Yellow
    }
    elseif ($nonZero) {
        Write-Host "  [WARN] ABANDONED 应强制 mmr_delta 全 0，但出现非 0 值：$($nonZero -join ',')" -ForegroundColor Yellow
    }
    else {
        Write-Host "  [OK] ABANDONED：mmr_delta 全 0（proto3 省略 0 值）,$playerCount 名玩家不掉段（不变量 §4）" -ForegroundColor Green
    }
}
else {
    Write-Host "  [i] NORMAL：mmr_delta 由 battle_result 标准 Elo（K=32）算出，胜负对称、守恒" -ForegroundColor DarkCyan
}

Write-Host ""
Write-Host "===== 出箱 → Kafka → player 后续验证指引 =====" -ForegroundColor Cyan
Write-Host @"
  ④ 确认 player.update producer ready（battle_result 启动日志）：
       [kafkax] producer ready: topic=pandora.player.update partitions=4 idempotent=false
       player_update_producer_ready topic=pandora.player.update
     若见 player_update_producer_init_failed，说明 kafka 不可用 → 出箱行积压不丢，待 producer 恢复。

  ⑤ 消费 pandora.player.update 确认出箱已投递（RunOutboxPublisher 2s 间隔）：
       docker exec -i pandora-kafka kafka-console-consumer ``
         --bootstrap-server localhost:9092 --topic pandora.player.update ``
         --from-beginning --max-messages $(2 * $TeamSize) --property print.key=true
     期望每名玩家一条（key = player_id），payload = PlayerUpdateEvent。

  ⑥ player 服务（:50002）幂等消费写回段位：
       grpcurl -plaintext -d '{\"playerId\":\"$BasePlayerId\"}' 127.0.0.1:50002 ``
         pandora.player.v1.PlayerService/GetMMR
     再发一次 ReportResult（同 match_id）→ player 经 mmr_history uk=match_id 幂等，段位不二次变动。
"@ -ForegroundColor DarkGray
Write-Host ""
Write-Host "===== 验证结束 =====" -ForegroundColor Cyan
