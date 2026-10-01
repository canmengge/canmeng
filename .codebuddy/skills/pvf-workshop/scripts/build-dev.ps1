<#
构建「开发人员专用」测试版 exe（PVF工坊 技能附脚本）

做的事与项目脚本 `03-脚本\构建开发版.cmd` 一致的 3 步，外加两件 AI 必须做的收尾：
  1. go build → 05-构建输出\pvfine-devkit.exe
  2. 落带时间戳的新文件名到 04-运行环境（永不覆盖正在运行的文件）
  3. 尽力更新主名（被占用则跳过）
  4. 【核验】最新 exe 是否比 Go 源码新 —— 防止"改了 Go 没重编，跑的还是旧内核"
  5. 【必做】打印必须告知用户的原话（不允许只说"重启工具"）

只在**改了 Go**（main.go / services / internal）时才需要跑本脚本。
纯前端（.vue/.ts/.css）改动请走 HMR，不要构建。

用法：
  .\build-dev.ps1
  .\build-dev.ps1 -Version 4.6.3
#>
[CmdletBinding()]
param(
  [string]$ProjectRoot = "d:\110AI",
  [string]$Version = "dev"
)

$ErrorActionPreference = "Stop"

$src = Join-Path $ProjectRoot "01-源码"
$outDir = Join-Path $ProjectRoot "05-构建输出"
$runDir = Join-Path $ProjectRoot "04-运行环境"

if (-not (Test-Path (Join-Path $src "go.mod"))) { throw "找不到源码目录：$src" }
foreach ($d in @($outDir, $runDir)) { if (-not (Test-Path $d)) { New-Item -ItemType Directory -Force -Path $d | Out-Null } }

# ---- 1. 构建 Go 内核 ----
Write-Host "=== 1/4 构建 Go 内核 ===" -ForegroundColor Cyan
$devkit = Join-Path $outDir "pvfine-devkit.exe"
& go -C $src build -trimpath -ldflags "-s -w" -o $devkit .
if ($LASTEXITCODE -ne 0) { throw "go build 失败（exit $LASTEXITCODE）" }

# ---- 2. 落新文件名 ----
Write-Host "=== 2/4 落新文件名 ===" -ForegroundColor Cyan
$ts = Get-Date -Format "yyyyMMdd-HHmmss"
$name = "PVF工坊-开发人员专用-$Version-$ts.exe"
$target = Join-Path $runDir $name
Copy-Item -LiteralPath $devkit -Destination $target -Force
Write-Host "  新文件：$name"

# ---- 3. 尽力更新主名 ----
Write-Host "=== 3/4 更新主名（被占用则跳过）===" -ForegroundColor Cyan
try {
  Copy-Item -LiteralPath $devkit -Destination (Join-Path $runDir "PVF工坊-开发人员专用.exe") -Force -ErrorAction Stop
  Write-Host "  主名已更新"
} catch {
  Write-Host "  主名被占用，已跳过（启动时会自动挑最新的 $name）" -ForegroundColor Yellow
}

# ---- 4. 核验：最新 exe 是否比 Go 源码新 ----
Write-Host "=== 4/4 核验 exe 新旧 ===" -ForegroundColor Cyan
$newestExe = Get-ChildItem (Join-Path $runDir "PVF工坊-开发人员专用*.exe") |
  Sort-Object LastWriteTime -Descending | Select-Object -First 1
$newestGo = Get-ChildItem $src -Recurse -File -Include *.go,*.mod,*.json |
  Where-Object { $_.FullName -notmatch "\\frontend\\" } |
  Sort-Object LastWriteTime -Descending | Select-Object -First 1

Write-Host ("  最新 exe : " + $newestExe.Name + "  " + $newestExe.LastWriteTime)
Write-Host ("  最新源码 : " + $newestGo.FullName.Replace($src, "01-源码") + "  " + $newestGo.LastWriteTime)

$stale = $newestGo.LastWriteTime -gt $newestExe.LastWriteTime
if ($stale) {
  Write-Host "  [失败] exe 比源码旧 —— 构建产物没落到运行区，先排查再让用户测" -ForegroundColor Red
} else {
  Write-Host "  [OK] exe 是最新的" -ForegroundColor Green
}

# ---- 5. 必须告知用户的原话 ----
Write-Host ""
Write-Host "================ 请把下面这段话原样告知用户 ================" -ForegroundColor Yellow
Write-Host "已构建完成：$name"
Write-Host "请【先关闭正在运行的工具窗口】，再双击："
Write-Host "  d:\110AI\04-运行环境\HMR 热更新\启动HMR热更新.cmd"
Write-Host "（Go 改动必须换 exe，重启程序不会重新编译；HMR 脚本会自动挑最新的 exe 启动）"
Write-Host "==========================================================" -ForegroundColor Yellow

if ($stale) { exit 1 }
