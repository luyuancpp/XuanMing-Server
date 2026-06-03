# Pandora 开发环境状态查看
#
# 用法:
#   pwsh tools/scripts/dev_status.ps1

$ErrorActionPreference = "Stop"

$ProjectRoot = Resolve-Path "$PSScriptRoot/../.."
$ComposeFile = "$ProjectRoot/deploy/docker-compose.dev.yml"
$EnvFile     = "$ProjectRoot/deploy/env/dev.env"

Write-Host "===== Pandora dev infra status =====" -ForegroundColor Cyan
docker compose -f $ComposeFile --env-file $EnvFile ps

Write-Host ""
Write-Host "===== 端口监听 =====" -ForegroundColor Cyan
$ports = @(3307, 6380, 9093, 2380, 2381, 9091, 3001)
foreach ($p in $ports) {
    $r = Test-NetConnection -ComputerName 127.0.0.1 -Port $p -WarningAction SilentlyContinue
    if ($r.TcpTestSucceeded) {
        Write-Host "  [OK] :$p" -ForegroundColor Green
    } else {
        Write-Host "  [--] :$p" -ForegroundColor DarkGray
    }
}
