<#
.SYNOPSIS
  Pandora 发布前预检（release preflight）。
  扫描所有 dev-only 开关 / 自签证书 / 弱配置是否已切换到生产安全值。
  任何一项不安全 => 打印 FAIL，并以退出码 1 结束，用于在 CI / 打包脚本里**阻止发布**。

.DESCRIPTION
  这个脚本只读扫描，不改任何环境、不碰 git。它把"人容易忘记的 dev hack"自动化拦截：
    - UE 客户端:GatewayHost / bDevInsecureTls / bAutoLoginForDev / DevLogin* 残留
    - 后端服务:login.dev_skip_password / server.grpc.enable_reflection
    - 边缘证书:是否仍是 mkcert 自签 / SAN 是否含 IP（生产必须公网 CA + 域名）

  设计原则:**fail-safe**。
    - UE GatewayHost / bAutoLoginForDev 等默认仍偏 dev 态,所以生产 ini 必须**显式**写成安全值;
      对必须显式关闭的项,ini 里查不到 => 判定为"没关" => FAIL。
    - 生产配置文件不存在 => FAIL（而不是"跳过=通过"）。

.PARAMETER UeGameIni
  UE 打包用的生产 Game ini（覆盖 PandoraBackendSubsystem 默认值的那一份）。
  默认 C:\work\Pandora\Config\DefaultGame.ini。如果你用单独的 Shipping/Platform ini，传它。

.PARAMETER BackendConfigDir
  后端服务配置根目录（递归找生产 yaml）。默认 services 目录。

.PARAMETER ConfigGlob
  生产配置文件名匹配。默认 '*-prod.yaml'（**注意:不要指向 *-dev.yaml**）。

.PARAMETER EnvoyCert
  生产边缘证书 cert.pem 路径（可选）。给了就校验 Issuer 不是 mkcert、SAN 不含 IP。

.PARAMETER ExpectedGatewayHost
  期望的生产网关域名（可选）。给了就校验 UE GatewayHost 精确等于它。

.EXAMPLE
  pwsh tools/scripts/release_preflight.ps1 `
    -UeGameIni C:\work\Pandora\Config\DefaultGame.ini `
    -BackendConfigDir E:\work\Pandora\services `
    -ConfigGlob '*-prod.yaml' `
    -EnvoyCert C:\deploy\prod\cert.pem `
    -ExpectedGatewayHost gateway.yourgame.com
#>
[CmdletBinding()]
param(
    [string]$UeGameIni = 'C:\work\Pandora\Config\DefaultGame.ini',
    [string]$BackendConfigDir = 'E:\work\Pandora\services',
    [string]$ConfigGlob = '*-prod.yaml',
    [string]$EnvoyCert = '',
    [string]$ExpectedGatewayHost = ''
)

$ErrorActionPreference = 'Stop'
$script:Results = @()

function Add-Result {
    param([string]$Name, [bool]$Ok, [string]$Detail)
    $script:Results += [pscustomobject]@{ Name = $Name; Ok = $Ok; Detail = $Detail }
}

function Get-IniValue {
    # 读取 [Section] 下 Key=Value（取最后一次出现，UE ini 后值覆盖前值）
    param([string]$Path, [string]$Section, [string]$Key)
    if (-not (Test-Path $Path)) { return $null }
    $lines = Get-Content $Path
    $inSection = $false
    $val = $null
    foreach ($raw in $lines) {
        $line = $raw.Trim()
        if ($line -match '^\[(.+)\]$') {
            $inSection = ($Matches[1] -eq $Section)
            continue
        }
        if ($inSection -and $line -match "^$([regex]::Escape($Key))\s*=\s*(.*)$") {
            $val = $Matches[1].Trim()
        }
    }
    return $val
}

Write-Host ''
Write-Host '==================== Pandora 发布前预检 ====================' -ForegroundColor Cyan
Write-Host ''

# ---------------------------------------------------------------------------
# A. UE 客户端 dev 开关（GatewayHost / 自动登录等生产必须显式覆盖）
# ---------------------------------------------------------------------------
$ueSection = '/Script/Pandora.PandoraBackendSubsystem'

if (-not (Test-Path $UeGameIni)) {
    Add-Result 'UE ini 存在' $false "找不到生产 Game ini: $UeGameIni"
}
else {
    # A1 GatewayHost 必须显式且非本机
    $gh = Get-IniValue $UeGameIni $ueSection 'GatewayHost'
    if ([string]::IsNullOrEmpty($gh)) {
        Add-Result 'UE GatewayHost' $false "ini 未显式设置 GatewayHost → 取头文件默认 127.0.0.1（玩家连不上）。生产必须写真实域名。"
    }
    elseif ($gh -match '^(127\.0\.0\.1|localhost|::1|host\.docker\.internal)$') {
        Add-Result 'UE GatewayHost' $false "GatewayHost=$gh 是本机地址,玩家连不上。改成真实域名。"
    }
    elseif ($gh -match '^\d{1,3}(\.\d{1,3}){3}$') {
        Add-Result 'UE GatewayHost' $false "GatewayHost=$gh 是裸 IP。公网 CA 不给 IP 签证书,必须用域名。"
    }
    elseif ($ExpectedGatewayHost -and $gh -ne $ExpectedGatewayHost) {
        Add-Result 'UE GatewayHost' $false "GatewayHost=$gh 与期望域名 $ExpectedGatewayHost 不符。"
    }
    else {
        Add-Result 'UE GatewayHost' $true "= $gh"
    }

    # A2 bDevInsecureTls 需为 False（头文件默认已是 false，未设即安全；若显式设了必须是 False）
    $tls = Get-IniValue $UeGameIni $ueSection 'bDevInsecureTls'
    if ([string]::IsNullOrEmpty($tls)) {
        Add-Result 'UE bDevInsecureTls' $true '未显式设置 → 取头文件默认 false（强制校验，安全）'
    }
    elseif ($tls -notmatch '^(False|0)$') {
        Add-Result 'UE bDevInsecureTls' $false "bDevInsecureTls=$tls（放开 TLS 校验）。生产必须 =False 或不设。"
    }
    else {
        Add-Result 'UE bDevInsecureTls' $true '= False（强制校验证书）'
    }

    # A3 bAutoLoginForDev 必须 False
    $auto = Get-IniValue $UeGameIni $ueSection 'bAutoLoginForDev'
    if ([string]::IsNullOrEmpty($auto)) {
        Add-Result 'UE bAutoLoginForDev' $false 'ini 未显式设置 → 取头文件默认 true（自动用 test 账号登录）。生产必须 =False。'
    }
    elseif ($auto -notmatch '^(False|0)$') {
        Add-Result 'UE bAutoLoginForDev' $false "bAutoLoginForDev=$auto。生产必须 =False。"
    }
    else {
        Add-Result 'UE bAutoLoginForDev' $true '= False'
    }

    # A4 DevLoginPasswordHash 不该带进包（残留 dev 弱口令）
    $pw = Get-IniValue $UeGameIni $ueSection 'DevLoginPasswordHash'
    if (-not [string]::IsNullOrEmpty($pw) -and $pw -ne '""') {
        Add-Result 'UE DevLoginPasswordHash' $false "ini 残留 DevLoginPasswordHash=$pw。生产应清空（autologin 关了也别带 dev 口令）。"
    }
    else {
        Add-Result 'UE DevLoginPasswordHash' $true '未残留'
    }
}

# ---------------------------------------------------------------------------
# B. 后端服务生产配置（dev_skip_password / enable_reflection）
# ---------------------------------------------------------------------------
$prodYamls = @()
if (Test-Path $BackendConfigDir) {
    $prodYamls = Get-ChildItem $BackendConfigDir -Recurse -Filter $ConfigGlob -ErrorAction SilentlyContinue
}

if ($prodYamls.Count -eq 0) {
    Add-Result '后端生产配置' $false "在 $BackendConfigDir 下没找到 $ConfigGlob。生产必须有独立的 *-prod.yaml(不要直接用 *-dev.yaml 上线)。"
}
else {
    foreach ($y in $prodYamls) {
        $text = Get-Content $y.FullName -Raw
        $rel = $y.Name

        # B1 dev_skip_password 不能为 true
        $skipBad = $false
        foreach ($m in [regex]::Matches($text, '(?im)^\s*dev_skip_password\s*:\s*(\S+)')) {
            if ($m.Groups[1].Value -match '^(true|1|yes)$') { $skipBad = $true }
        }
        if ($skipBad) {
            Add-Result "后端 dev_skip_password [$rel]" $false '免密登录已开!任意账号名可登录任意 player_id。生产必须 false 或删除。'
        }
        else {
            Add-Result "后端 dev_skip_password [$rel]" $true '关闭/未设'
        }

        # B2 enable_reflection 不能为 true
        $reflBad = $false
        foreach ($m in [regex]::Matches($text, '(?im)^\s*enable_reflection\s*:\s*(\S+)')) {
            if ($m.Groups[1].Value -match '^(true|1|yes)$') { $reflBad = $true }
        }
        if ($reflBad) {
            Add-Result "后端 enable_reflection [$rel]" $false 'gRPC reflection 已开,会泄露服务 schema。生产必须关。'
        }
        else {
            Add-Result "后端 enable_reflection [$rel]" $true '关闭/未设'
        }
    }
}

# ---------------------------------------------------------------------------
# C. 边缘 TLS 证书（生产必须公网 CA + 域名 SAN，不能是 mkcert 自签 / IP SAN）
# ---------------------------------------------------------------------------
if ([string]::IsNullOrEmpty($EnvoyCert)) {
    Add-Result 'TLS 证书' $false '未提供 -EnvoyCert。生产证书必须显式校验:公网 CA(Let''s Encrypt/商业)+ 域名 SAN。'
}
elseif (-not (Test-Path $EnvoyCert)) {
    Add-Result 'TLS 证书' $false "找不到证书: $EnvoyCert"
}
else {
    $dump = & certutil -dump $EnvoyCert 2>$null | Out-String
    $issuerMkcert = $dump -match 'mkcert'
    $sanHasIp = $dump -match 'IP Address='
    if ($issuerMkcert) {
        Add-Result 'TLS 证书 Issuer' $false '证书 Issuer 仍是 mkcert(本地自签),玩家不信任。换公网 CA。'
    }
    else {
        Add-Result 'TLS 证书 Issuer' $true '非 mkcert'
    }
    if ($sanHasIp) {
        Add-Result 'TLS 证书 SAN' $false '证书 SAN 含 IP Address。生产证书 SAN 只写域名。'
    }
    else {
        Add-Result 'TLS 证书 SAN' $true '无 IP SAN'
    }
}

# ---------------------------------------------------------------------------
# 汇总
# ---------------------------------------------------------------------------
Write-Host ''
Write-Host '------------------------- 结果 -------------------------' -ForegroundColor Cyan
$fail = 0
foreach ($r in $script:Results) {
    if ($r.Ok) {
        Write-Host ('  [PASS] ' + $r.Name + '  ' + $r.Detail) -ForegroundColor Green
    }
    else {
        Write-Host ('  [FAIL] ' + $r.Name + '  ' + $r.Detail) -ForegroundColor Red
        $fail++
    }
}
Write-Host '--------------------------------------------------------' -ForegroundColor Cyan
Write-Host ''

if ($fail -gt 0) {
    Write-Host ("✗ 预检未通过:$fail 项不安全。修复后再发布。") -ForegroundColor Red
    Write-Host '  详细步骤见 docs/ops/release-checklist.md' -ForegroundColor Yellow
    exit 1
}
else {
    Write-Host '✓ 预检全部通过,可以进入发布流程。' -ForegroundColor Green
    exit 0
}
