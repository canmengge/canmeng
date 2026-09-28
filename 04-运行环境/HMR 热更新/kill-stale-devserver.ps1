# kill-stale-devserver.ps1 —— 清理 HMR 残留，由「启动HMR热更新.cmd」调用
#
# 清理两类残留：
#   ① 占住 9255 的 node 进程 —— 不清掉的话，新 dev server 会报
#      "Port 9255 is already in use"。
#   ② 上一次的 dev server 已经退出、而它的 cmd 窗口卡在 pause 上的「僵尸窗口」
#      （用户会看到两个窗口，其中一个显示「开发服务器已退出 / 请按任意键继续」）。
#      判据：命令行含 _前端开发服务器.cmd，且**没有活着的 node 子进程** ——
#      正在运行的那个 dev server 一定有 node 子进程，所以不会被误杀。
#
# 输出一行：KILLED/WOULD-KILL <明细> 或 CLEAN（供 cmd 的 for /f 捕获后打印）。
# 设环境变量 KILL_STALE_DRY=1 可只观察不实际结束进程（供验证用）。
#
# 路径与端口写死在工作台约定上，不需要传参（避免 cmd 里出现中文/引号转义问题）。

$port = 9255
$dry = ($env:KILL_STALE_DRY -eq '1')
$tag = if ($dry) { 'WOULD-KILL' } else { 'KILLED' }
$killed = New-Object System.Collections.ArrayList

# ① 占用端口的进程（一般是残留的 vite/node）
try {
    $owners = Get-NetTCPConnection -LocalPort $port -State Listen -ErrorAction SilentlyContinue |
        Select-Object -ExpandProperty OwningProcess | Sort-Object -Unique
    foreach ($owner in $owners) {
        if ($owner -and $owner -gt 0) {
            if (-not $dry) { Stop-Process -Id $owner -Force -ErrorAction SilentlyContinue }
            [void]$killed.Add("port:$owner")
        }
    }
}
catch { }

# ② 僵尸 cmd 窗口（跑 _前端开发服务器.cmd 但没有活 node 子进程的）
try {
    $liveNodeParents = @(Get-CimInstance Win32_Process -Filter "Name='node.exe'" -ErrorAction SilentlyContinue |
        Select-Object -ExpandProperty ParentProcessId)
    $staleCmds = Get-CimInstance Win32_Process -Filter "Name='cmd.exe'" -ErrorAction SilentlyContinue |
        Where-Object { $_.CommandLine -and $_.CommandLine -like '*_前端开发服务器.cmd*' }
    foreach ($c in $staleCmds) {
        if ($liveNodeParents -notcontains $c.ProcessId) {
            if (-not $dry) { Stop-Process -Id $c.ProcessId -Force -ErrorAction SilentlyContinue }
            [void]$killed.Add("cmd:$($c.ProcessId)")
        }
    }
}
catch { }

if ($killed.Count -gt 0) {
    Write-Output ($tag + ' ' + ($killed -join ' '))
}
else {
    Write-Output 'CLEAN'
}
exit 0
