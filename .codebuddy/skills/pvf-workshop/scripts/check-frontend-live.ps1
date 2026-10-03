<#
前端「可见性」自检（PVF工坊 技能附脚本）

用途：**改完前端后必须跑一次**，回答一个问题：
      「dev server 现在吐给我的，是我刚改的代码，还是旧的？」

为什么需要它（2026-10-03 真实事故）：
  改完 FormView.vue（纯前端，按规则不构建），只说了"保存即热更新生效"。
  但那个「前端开发服务器」已经跑了很久，**一直吐旧模块**；用户重启程序后
  看到/测到的仍是**旧界面**（中间大片空白、拖列宽无效），白跑一轮。
  事后用 HTTP 直查 dev server 才确认它当时确实在吐旧代码。

判据只有一个：**直接向 dev server 要那个文件，看响应里有没有本轮的标记串**。
  ⇒ 所以每轮前端改动都必须留下一个可被 grep 的标记串
    （新加的 class 名 / 函数名 / 中文文案）。

用法：
  # 最常用：标记串在不在（多个标记全部命中才算通过）
  .\check-frontend-live.ps1 -Marker "fv-table--fixed"

  # 换文件
  .\check-frontend-live.ps1 -File "src/components/ToolBar.vue" -Marker "visualMenuOptions"

  # 只看 dev server 在不在
  .\check-frontend-live.ps1

退出码：0 = 通过（dev server 吐的是最新代码，可以告诉用户"能测了"）
        1 = 不通过（先重跑 启动HMR热更新.cmd 重启 dev server，再重跑本脚本）
#>
[CmdletBinding()]
param(
  [string]$File = "src/components/FormView.vue",
  [string[]]$Marker = @(),
  [int]$Port = 9255,
  [string]$ProjectRoot = "d:\110AI"
)

$ok = $true
$rel = $File -replace "\\", "/"
$local = Join-Path (Join-Path $ProjectRoot "01-源码\frontend") ($rel -replace "/", "\")

Write-Output "=== 前端可见性自检 ==="

# ① dev server 在不在
$listening = $null
try {
  $listening = Get-NetTCPConnection -State Listen -ErrorAction SilentlyContinue |
    Where-Object { $_.LocalPort -eq $Port }
} catch {
  $listening = $null
}
if (-not $listening) {
  Write-Output "[X] 端口 $Port 没有监听 —— 前端开发服务器没在跑"
  Write-Output "    此时程序界面用的是 exe 内嵌的前端（那份永远是旧的，build-dev 只编 Go）"
  Write-Output "    处置：让用户双击 04-运行环境\HMR 热更新\启动HMR热更新.cmd"
  exit 1
}
$vitePid = $listening[0].OwningProcess
Write-Output "[1] dev server 在跑：127.0.0.1:$Port（PID $vitePid）"

# ② 源码最后修改时间 vs dev server 启动时间（辅助信息，判据在 ③）
if (Test-Path $local) {
  $srcTime = (Get-Item $local).LastWriteTime
  Write-Output "[2] 源码 $rel 最后修改：$srcTime"
  $vite = Get-Process -Id $vitePid -ErrorAction SilentlyContinue
  if ($vite) {
    Write-Output "    dev server 启动：$($vite.StartTime)"
    if ($vite.StartTime -gt $srcTime) {
      Write-Output "    （dev server 比源码新 —— watcher 一定看得到本轮改动）"
    } else {
      Write-Output "    （dev server 比源码旧 —— 常见且正常，vite 靠 watcher 感知；以 ③ 的标记检查为准）"
    }
  }
} else {
  Write-Output "[2] !! 源码文件不存在：$local"
  $ok = $false
}

# ③ 核心判据
$url = "http://127.0.0.1:$Port/$rel"
try {
  $resp = Invoke-WebRequest $url -UseBasicParsing -TimeoutSec 30
  $content = $resp.Content
  Write-Output "[3] GET $url -> HTTP $($resp.StatusCode)，响应 $($content.Length) 字符"
} catch {
  Write-Output "[X] GET $url 失败：$($_.Exception.Message)"
  exit 1
}

if ($Marker.Count -eq 0) {
  Write-Output "[4] 未给标记串，跳过内容核对（务必至少给一个：本轮新加的 class / 函数名 / 文案）"
} else {
  foreach ($m in $Marker) {
    if ($content.Contains($m)) {
      Write-Output "[4] 标记 [$m] => 有"
    } else {
      Write-Output "[4] 标记 [$m] => **没有**"
      $ok = $false
    }
  }
}

if ($ok) {
  Write-Output "结论：通过 —— dev server 吐的就是最新代码，可以让用户测了。"
  exit 0
}

Write-Output "结论：**不通过** —— dev server 还在吐旧代码，此时告诉用户"可以测"就是让他测旧版。"
Write-Output "处置顺序："
Write-Output "  1) 让用户重跑 04-运行环境\HMR 热更新\启动HMR热更新.cmd（它启动前会杀掉旧 dev server）"
Write-Output "  2) 等它打印「前端开发服务器 127.0.0.1:9255」后，重跑本脚本"
Write-Output "  3) 仍不通过才兜底：npm run build 重建 dist + 重编 exe（并核验 exe 内嵌了新标记）"
exit 1
