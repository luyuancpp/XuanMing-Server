<#
.SYNOPSIS
  把 Pandora 本地开发 CA(公开证书)装进 **UE 客户端工程**，让 UE 用真证书认证
  (bDevInsecureTls=false)连本地 / 共享 dev 的 Envoy(:8443)。

.DESCRIPTION
  机制(已对照 UE 引擎源码 SslCertificateManager.cpp::BuildRootCertificateArray 确认):
    - UE 用 [SSL] DebuggingCertificatePath 把**一张额外证书叠加到**引擎公网 CA 包**之上**
      (不替换，公网 CA 信任全保留)。仅在非 Shipping(编辑器/Development)生效。
    - 证书放在客户端工程 Config/Certificates/(不在 Content)→ **绝不打进发行包**，玩家拿不到。
    - **不碰共享引擎目录**(D:\UnrealEngine)，引擎升级不受影响，队友靠仓库 + 本脚本复现。

  本脚本做三件事(幂等):
    1. 把公开 dev CA 复制到客户端工程 Config/Certificates/pandora-dev-rootCA.pem;
    2. 确保客户端 Config/DefaultEngine.ini 有 [SSL] DebuggingCertificatePath 指向它;
    3. 清理:若历史上往引擎 cacert.pem 追加过 dev CA，从备份还原(撤销污染)。

  ⚠️ 仅限开发期。生产 Envoy 用公网 CA 签真实域名证书，玩家零配置，不需要这个 dev CA。
     见 docs/ops/release-checklist.md §3。

.PARAMETER ClientRepoDir
  UE 客户端仓库根(含 Config/)。默认 C:\work\Pandora(CLAUDE.md §2 固定约定)。

.PARAMETER CaSource
  公开 dev CA 源文件。默认 deploy/dev-ca/pandora-dev-rootCA.pem(后端仓库内，相对本脚本)。

.PARAMETER UeEngineDir
  UE 引擎根(仅用于清理历史引擎污染)。默认 D:\UnrealEngine。

.EXAMPLE
  pwsh tools/scripts/import_dev_ca.ps1
#>
[CmdletBinding()]
param(
    [string]$ClientRepoDir = 'C:\work\Pandora',
    [string]$CaSource = '',
    [string]$UeEngineDir = 'D:\UnrealEngine'
)

$ErrorActionPreference = 'Stop'

# --- 1. 定位公开 dev CA 源 ---
if ([string]::IsNullOrEmpty($CaSource)) {
    $repoRoot = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)  # tools/scripts -> 后端仓库根
    $CaSource = Join-Path $repoRoot 'deploy\dev-ca\pandora-dev-rootCA.pem'
}
if (-not (Test-Path $CaSource)) {
    Write-Host "✗ 找不到 dev CA 源: $CaSource" -ForegroundColor Red
    exit 1
}
$first = Get-Content $CaSource -TotalCount 1
if ($first -notmatch 'BEGIN CERTIFICATE') {
    Write-Host "✗ $CaSource 不是公开证书(首行: $first)。拒绝。" -ForegroundColor Red
    exit 1
}

# --- 2. 复制到客户端工程 Config/Certificates ---
if (-not (Test-Path $ClientRepoDir)) {
    Write-Host "✗ 找不到客户端仓库: $ClientRepoDir(用 -ClientRepoDir 指定)" -ForegroundColor Red
    exit 1
}
$certDir = Join-Path $ClientRepoDir 'Config\Certificates'
$certDst = Join-Path $certDir 'pandora-dev-rootCA.pem'
New-Item -ItemType Directory -Force -Path $certDir | Out-Null
Copy-Item $CaSource $certDst -Force
Write-Host "✓ dev CA 已放入客户端工程: $certDst" -ForegroundColor Green

# --- 3. 确保 DefaultEngine.ini 有 [SSL] DebuggingCertificatePath ---
$ini = Join-Path $ClientRepoDir 'Config\DefaultEngine.ini'
$certIniPath = ($certDst -replace '\\', '/')
if (-not (Test-Path $ini)) {
    Write-Host "✗ 找不到 $ini" -ForegroundColor Red
    exit 1
}
$iniText = Get-Content $ini -Raw
if ($iniText -match '(?im)^\s*DebuggingCertificatePath\s*=') {
    $currentPath = ([regex]::Match($iniText, '(?im)^\s*DebuggingCertificatePath\s*=\s*(.*?)\s*$')).Groups[1].Value
    if ($currentPath -ne $certIniPath) {
        $updated = [regex]::Replace(
            $iniText,
            '(?im)^(\s*DebuggingCertificatePath\s*=\s*).*$',
            ('${1}' + $certIniPath))
        Set-Content -Path $ini -Value $updated -NoNewline -Encoding utf8
        Write-Host '✓ 已更新 DefaultEngine.ini 的 DebuggingCertificatePath。' -ForegroundColor Green
    }
    else {
        Write-Host '✓ DefaultEngine.ini 的 DebuggingCertificatePath 已正确。' -ForegroundColor Green
    }
}
else {
    $block = @"

[SSL]
; Pandora 本地开发期 TLS 证书认证：把 dev CA 叠加到引擎公网 CA 包之上（不替换，仅非 Shipping 生效）。
; 证书在 Config 下、不在 Content，绝不打进发行包。生产用公网 CA 签真实域名，无需本项。
DebuggingCertificatePath=$certIniPath
"@
    Add-Content -Path $ini -Value $block
    Write-Host '✓ 已写入 [SSL] DebuggingCertificatePath 到 DefaultEngine.ini。' -ForegroundColor Green
}

# --- 4. 清理历史引擎污染（若早期版本往引擎 cacert.pem 追加过） ---
$ueCacert = Join-Path $UeEngineDir 'Engine\Content\Certificates\ThirdParty\cacert.pem'
$ueBak = "$ueCacert.pandora.bak"
if (Test-Path $ueBak) {
    Copy-Item $ueBak $ueCacert -Force
    Remove-Item $ueBak -Force
    Write-Host '✓ 已从备份还原引擎 cacert.pem（撤销历史污染）。' -ForegroundColor Yellow
}

Write-Host ''
Write-Host '完成。重启 UE 编辑器后，bDevInsecureTls=false 即可通过 Envoy:8443 TLS 校验。' -ForegroundColor Cyan
exit 0
