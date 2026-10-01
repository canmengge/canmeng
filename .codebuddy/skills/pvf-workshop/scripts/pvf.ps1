<#
PVF 内容分析工具（PVF工坊 技能附脚本）

用途：对 DNF 的 Script.pvf 做「列文件 / 统计扩展名 / 读单文件 / 搜索路径 / 读清单 / 按清单提取 / 一键导出全部 .lua」。
原理：封装项目自带的 CLI `01-源码\cmd\pvf-cli`（无需自己写解析代码）。

注意：每次调用都会重新打开归档（110 版约 540MB，约 10 秒）。一次调用只做一个动作；
需要多次分析时，先用 `-Action files -Out <清单>` 导出路径清单，后续复用那个文件。

示例：
  # 扩展名分布（统计归档里有什么）
  .\pvf.ps1 -Action extstat

  # 列出所有路径到文件（后续复用）
  .\pvf.ps1 -Action files -Out D:\temp\files.txt

  # 读一个文件（.lua / .skl / .act 都是明文）
  .\pvf.ps1 -Action read -Path "skill/Swordman/TripleSlash.skl"

  # 搜索路径
  .\pvf.ps1 -Action search -Keyword ".lua" -Prefix "clientonly/"

  # 读清单（技能 ID ↔ 文件）
  .\pvf.ps1 -Action lst -Path "skill/swordmanskill.lst"

  # 一键导出全部 .lua 到目录（供词频/结构分析）
  .\pvf.ps1 -Action lua -Out D:\temp\lua-src
#>
[CmdletBinding()]
param(
  [Parameter(Mandatory = $true)]
  [ValidateSet("files", "extstat", "read", "search", "lst", "extract", "lua")]
  [string]$Action,

  [string]$Pvf = "D:\115us\DFO_2.31.1.117\Script.pvf",
  [string]$ProjectRoot = "d:\110AI",

  [string]$Path,      # read / lst：归档内路径
  [string]$Keyword,   # search
  [string]$Prefix,    # search 的目录前缀；files 的路径前缀
  [string]$List,      # extract：路径清单文件
  [string]$Out,       # files / extstat / lua / extract 的输出
  [int]$Limit = 100,
  [int]$TopExt = 40
)

$ErrorActionPreference = "Stop"

$src = Join-Path $ProjectRoot "01-源码"
if (-not (Test-Path (Join-Path $src "go.mod"))) { throw "找不到源码目录：$src" }
if (-not (Test-Path $Pvf)) { throw "找不到 PVF 文件：$Pvf" }

function Invoke-PvfCli {
  param([string[]]$CliArgs)
  & go -C $src run ./cmd/pvf-cli @CliArgs
  if ($LASTEXITCODE -ne 0) { throw "pvf-cli 执行失败（exit $LASTEXITCODE）" }
}

function New-TempPath([string]$Tag) {
  Join-Path $env:TEMP ("pvf-" + $Tag + "-" + [guid]::NewGuid().ToString("N").Substring(0, 8) + ".txt")
}

# 统计扩展名分布（按「行数」而非整篇读入，540MB 清单也吃得下）
function Get-ExtStat {
  param([string]$FilePath, [int]$Top)
  $counts = @{}
  foreach ($line in [System.IO.File]::ReadLines($FilePath)) {
    $dot = $line.LastIndexOf(".")
    $slash = $line.LastIndexOf("/")
    if ($dot -ge 0 -and $dot -gt $slash) { $ext = $line.Substring($dot).ToLower() } else { $ext = "(noext)" }
    $counts[$ext] = [int]$counts[$ext] + 1
  }
  $counts.GetEnumerator() | Sort-Object Value -Descending | Select-Object -First $Top
}

Write-Host "归档：$Pvf" -ForegroundColor Cyan
Write-Host "动作：$Action（每次都要重新打开归档，约 10 秒）" -ForegroundColor DarkGray

switch ($Action) {
  "files" {
    if (-not $Out) { $Out = New-TempPath "files" }
    if ($Prefix) { Invoke-PvfCli @("files", $Pvf, $Prefix, "--out", $Out) }
    else { Invoke-PvfCli @("files", $Pvf, "--out", $Out) }
    Write-Host "路径清单已导出：$Out" -ForegroundColor Green
  }

  "extstat" {
    $tmp = New-TempPath "files"
    Invoke-PvfCli @("files", $Pvf, "--out", $tmp)
    $rows = Get-ExtStat -FilePath $tmp -Top $TopExt
    Write-Host "`n扩展名分布（前 $TopExt）：" -ForegroundColor Cyan
    foreach ($r in $rows) { "{0,10}  {1}" -f $r.Value, $r.Key }
    if ($Out) {
      $rows | ForEach-Object { "{0}`t{1}" -f $_.Value, $_.Key } | Set-Content -LiteralPath $Out -Encoding UTF8
      Write-Host "`n已写入：$Out" -ForegroundColor Green
    }
    Remove-Item -LiteralPath $tmp -Force
  }

  "read" {
    if (-not $Path) { throw "read 需要 -Path <归档内路径>" }
    Invoke-PvfCli @("read", $Pvf, $Path)
  }

  "search" {
    if (-not $Keyword) { throw "search 需要 -Keyword <关键词>" }
    $a = @("search", $Pvf, $Keyword)
    if ($Prefix) { $a += $Prefix }
    $a += @("--limit", "$Limit")
    Invoke-PvfCli $a
  }

  "lst" {
    if (-not $Path) { throw "lst 需要 -Path <lst 路径>" }
    Invoke-PvfCli @("lst", $Pvf, $Path)
  }

  "extract" {
    if (-not $Out -or -not $List) { throw "extract 需要 -Out <目录> -List <路径清单文件>" }
    Invoke-PvfCli @("extract", $Pvf, $Out, $List)
  }

  "lua" {
    # 一键：列全部路径 → 筛 .lua → 导出文本（供 AI/技能/词频分析）
    $all = New-TempPath "files"
    Invoke-PvfCli @("files", $Pvf, "--out", $all)

    $man = New-TempPath "lua-manifest"
    $writer = [System.IO.StreamWriter]::new($man, $false, [System.Text.UTF8Encoding]::new($false))
    $n = 0
    foreach ($line in [System.IO.File]::ReadLines($all)) {
      if ($line.EndsWith(".lua")) { $writer.WriteLine($line); $n++ }
    }
    $writer.Close()
    Write-Host "命中 .lua：$n 个" -ForegroundColor Cyan
    if ($n -eq 0) { Remove-Item -LiteralPath $all, $man -Force; return }

    if ($Out) {
      Invoke-PvfCli @("extract", $Pvf, $Out, $man)
      Write-Host "文本已导出到：$Out" -ForegroundColor Green
    } else {
      Write-Host "未指定 -Out，仅生成清单：$man（复用这个文件可避免重复列目录）" -ForegroundColor Yellow
    }
    Remove-Item -LiteralPath $all -Force
  }
}
