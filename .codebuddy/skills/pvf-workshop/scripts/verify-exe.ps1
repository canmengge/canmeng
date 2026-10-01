<#
正式版 EXE 核验（PVF工坊 技能附脚本）

对应发布流程 §12.5 自检的第 5、6 项。**不要靠眼睛看**，跑这个脚本。

检查内容：
  1. PE 头：签名 PE / 可选头 magic = 0x20B（PE32+）/ subsystem = 2（GUI，无黑窗）
  2. 图标资源：3:6 与 14:1
  3. 内嵌字面量：必须命中（一般给版本号 + 本版新功能关键词）
  4. 禁用字面量：不得出现 VITE_DEV_TOOLS（出现即为误发了开发者面板）
  5. 大小 + MD5 + SHA-256（用于写进《使用说明》与清单核对）

示例：
  .\verify-exe.ps1 -Exe "d:\110AI\06-交付发布\release-4.6\PVF工坊4.6 ·编辑器110优化版 · By 残梦断忆.exe" `
                   -Literals "4.6.0","取消封包","TXT 模式","list 查重"
#>
[CmdletBinding()]
param(
  [Parameter(Mandatory = $true)][string]$Exe,
  [string[]]$Literals = @(),
  [string[]]$ForbiddenLiterals = @("VITE_DEV_TOOLS"),
  [string]$IconProbe,
  [string]$ProjectRoot = "d:\110AI"
)

$ErrorActionPreference = "Stop"

if (-not (Test-Path -LiteralPath $Exe)) { throw "找不到 exe：$Exe" }
$item = Get-Item -LiteralPath $Exe
$bytes = [System.IO.File]::ReadAllBytes($Exe)

$fail = New-Object System.Collections.Generic.List[string]
$pass = New-Object System.Collections.Generic.List[string]

function Add-Result([bool]$Ok, [string]$Text) {
  if ($Ok) { $script:pass.Add($Text) } else { $script:fail.Add($Text) }
}

# ---- 1. PE 头 ----
$peOffset = [BitConverter]::ToInt32($bytes, 0x3C)
$peSig = [System.Text.Encoding]::ASCII.GetString($bytes, $peOffset, 2)
$optOffset = $peOffset + 24
$magic = [BitConverter]::ToUInt16($bytes, $optOffset)
$subsystem = [BitConverter]::ToUInt16($bytes, $optOffset + 68)

Add-Result ($peSig -eq "PE") ("PE 签名 = '$peSig'（期望 PE）")
Add-Result ($magic -eq 0x20B) ("可选头 magic = 0x{0:X}（期望 0x20B）" -f $magic)
Add-Result ($subsystem -eq 2) ("subsystem = $subsystem（期望 2 = GUI 无黑窗；3 = 控制台黑窗）")

# ---- 2. 图标 ----
if (-not $IconProbe) { $IconProbe = Join-Path $ProjectRoot "06-交付发布\icon-replace\probe-res.js" }
if (Test-Path -LiteralPath $IconProbe) {
  $iconOut = (& node $IconProbe $Exe 2>&1) -join "`n"
  $iconOk = ($iconOut -match "3:6") -and ($iconOut -match "14:1")
  Add-Result $iconOk ("图标资源 " + (($iconOut -split "`n" | Select-Object -First 1)))
  if (-not $iconOk) { Write-Host $iconOut -ForegroundColor DarkGray }
} else {
  Write-Host "[提示] 未找到图标核验脚本（$IconProbe），跳过图标检查" -ForegroundColor Yellow
}

# ---- 3/4. 内嵌字面量（exe 内文本为 UTF-8，整篇解码后查子串）----
$text = [System.Text.Encoding]::UTF8.GetString($bytes)
foreach ($lit in $Literals) {
  Add-Result ($text.Contains($lit)) ("内嵌字面量「$lit」")
}
foreach ($lit in $ForbiddenLiterals) {
  Add-Result (-not $text.Contains($lit)) ("不含禁用字面量「$lit」（出现=误发开发者面板）")
}

# ---- 5. 哈希 ----
$md5 = (Get-FileHash -LiteralPath $Exe -Algorithm MD5).Hash
$sha = (Get-FileHash -LiteralPath $Exe -Algorithm SHA256).Hash

Write-Host ""
Write-Host "==================== EXE 核验报告 ====================" -ForegroundColor Cyan
Write-Host ("文件   : " + $item.Name)
Write-Host ("路径   : " + $item.FullName)
Write-Host ("大小   : {0:N0} 字节（{1:N1} MB）" -f $item.Length, ($item.Length / 1MB))
Write-Host ("修改时间: " + $item.LastWriteTime)
Write-Host ("MD5    : " + $md5)
Write-Host ("SHA256 : " + $sha)
Write-Host ""
foreach ($p in $pass) { Write-Host ("  [OK]   " + $p) -ForegroundColor Green }
foreach ($f in $fail) { Write-Host ("  [FAIL] " + $f) -ForegroundColor Red }
Write-Host ""
if ($fail.Count -eq 0) {
  Write-Host "结论：核验通过（$($pass.Count)/$($pass.Count)）" -ForegroundColor Green
  exit 0
} else {
  Write-Host "结论：核验未通过 —— 有 $($fail.Count) 项不合格，不要上传" -ForegroundColor Red
  exit 1
}
