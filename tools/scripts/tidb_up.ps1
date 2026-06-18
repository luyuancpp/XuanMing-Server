# Pandora TiDB 集群一键起服 + 装载好友图 schema(2026-06-18)
#
# 好友图迁 TiDB(docs/design/friend-distributed-scaling.md §8 / §14)。
# 本脚本把"起 TiDB 集群 + 建账号 + 装载 pandora_social schema"收敛成一条命令。
#
# 用法:
#   pwsh tools/scripts/tidb_up.ps1                  # 起集群 + 建账号 + 装载 DDL
#   pwsh tools/scripts/tidb_up.ps1 -Pull            # 先拉最新镜像再起
#   pwsh tools/scripts/tidb_up.ps1 -Down            # 停集群(保留数据卷)
#   pwsh tools/scripts/tidb_up.ps1 -Down -Volumes   # 停 + 删数据卷(彻底重来)
#
# 前置:本机已装 Docker;首次起会拉 pingcap/{pd,tikv,tidb} 镜像(需联网)。
# 起好后:friend --conf services/social/friend/etc/friend-dev-tidb.yaml
#
# 注:按 AGENTS.md §11.1,本脚本由 Codex / 人执行(起重服务 / 拉镜像属环境动作)。

param(
    [switch]$Pull,
    [switch]$Down,
    [switch]$Volumes
)

$ErrorActionPreference = "Stop"

$ProjectRoot = Resolve-Path "$PSScriptRoot/../.."
$ComposeFile = "$ProjectRoot/deploy/docker-compose.tidb.yml"
$DdlFile     = "$ProjectRoot/deploy/tidb-init/01-social-tidb.sql"
$Network     = "pandora-tidb-net"
$ProjectName = "pandora-tidb"

# friend-dev-tidb.yaml DSN 里的账号(改 DSN 时同步改这里)
$DbUser = "pandora"
$DbPwd  = "pandora_dev_pwd"

if (-not (Test-Path $ComposeFile)) {
    Write-Host "[ERR] compose file not found: $ComposeFile" -ForegroundColor Red
    exit 1
}

# ---- 停集群 ----
if ($Down) {
    Write-Host "===== Pandora TiDB down =====" -ForegroundColor Cyan
    if ($Volumes) {
        docker compose -p $ProjectName -f $ComposeFile down -v
        Write-Host "[OK] TiDB stopped, volumes removed." -ForegroundColor Green
    } else {
        docker compose -p $ProjectName -f $ComposeFile down
        Write-Host "[OK] TiDB stopped, volumes kept." -ForegroundColor Green
    }
    exit $LASTEXITCODE
}

Write-Host "===== Pandora TiDB up =====" -ForegroundColor Cyan
Write-Host "Compose: $ComposeFile"
Write-Host "DDL:     $DdlFile"
Write-Host ""

# ---- 1. validate ----
Write-Host "[1/5] Validating compose file..." -ForegroundColor Yellow
docker compose -p $ProjectName -f $ComposeFile config --quiet
if ($LASTEXITCODE -ne 0) { Write-Host "[ERR] compose invalid" -ForegroundColor Red; exit 1 }

# ---- 2. pull (可选) ----
if ($Pull) {
    Write-Host "[2/5] Pulling images..." -ForegroundColor Yellow
    docker compose -p $ProjectName -f $ComposeFile pull
    if ($LASTEXITCODE -ne 0) { Write-Host "[ERR] pull failed" -ForegroundColor Red; exit 1 }
} else {
    Write-Host "[2/5] Skipping pull (use -Pull to refresh)" -ForegroundColor Yellow
}

# ---- 3. up ----
Write-Host "[3/5] Starting TiDB cluster..." -ForegroundColor Yellow
docker compose -p $ProjectName -f $ComposeFile up -d
if ($LASTEXITCODE -ne 0) { Write-Host "[ERR] compose up failed" -ForegroundColor Red; exit 1 }

# ---- 4. 等 TiDB :4000 可连(用一次性 mysql client 容器 ping)----
Write-Host "[4/5] Waiting for TiDB SQL layer (:4000)..." -ForegroundColor Yellow
$ready = $false
for ($i = 1; $i -le 30; $i++) {
    docker run --rm --network $Network mysql:8.4 `
        mysqladmin ping -h tidb -P 4000 -u root --silent 2>$null
    if ($LASTEXITCODE -eq 0) { $ready = $true; break }
    Start-Sleep -Seconds 3
    Write-Host "  ...still waiting ($i/30)"
}
if (-not $ready) {
    Write-Host "[ERR] TiDB not ready after ~90s. Check: docker compose -p $ProjectName -f $ComposeFile logs tidb" -ForegroundColor Red
    exit 1
}
Write-Host "  TiDB is up." -ForegroundColor Green

# ---- 5. 建账号 + 装载 schema ----
Write-Host "[5/5] Creating user + loading schema..." -ForegroundColor Yellow

# 5a. 建 pandora 账号并授权 pandora_social(TiDB root 默认无密码)
$bootstrapSql = @"
CREATE DATABASE IF NOT EXISTS ``pandora_social`` DEFAULT CHARACTER SET utf8mb4 DEFAULT COLLATE utf8mb4_bin;
CREATE USER IF NOT EXISTS '$DbUser'@'%' IDENTIFIED BY '$DbPwd';
GRANT ALL PRIVILEGES ON ``pandora_social``.* TO '$DbUser'@'%';
FLUSH PRIVILEGES;
"@
$bootstrapSql | docker run --rm -i --network $Network mysql:8.4 `
    mysql -h tidb -P 4000 -u root
if ($LASTEXITCODE -ne 0) { Write-Host "[ERR] bootstrap user/db failed" -ForegroundColor Red; exit 1 }

# 5b. 装载好友图 + chat schema
Get-Content -Raw $DdlFile | docker run --rm -i --network $Network mysql:8.4 `
    mysql -h tidb -P 4000 -u root
if ($LASTEXITCODE -ne 0) { Write-Host "[ERR] load schema failed" -ForegroundColor Red; exit 1 }

Write-Host ""
Write-Host "===== TiDB ready =====" -ForegroundColor Green
Write-Host "  SQL:    127.0.0.1:4000 (user=$DbUser db=pandora_social)"
Write-Host "  Status: http://127.0.0.1:10080/status"
Write-Host ""
Write-Host "Start friend against TiDB:" -ForegroundColor Cyan
Write-Host "  friend --conf services/social/friend/etc/friend-dev-tidb.yaml"
