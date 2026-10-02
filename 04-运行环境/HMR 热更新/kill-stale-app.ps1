# kill-stale-app.ps1 —— 启动前关闭已有编辑器实例，由「启动HMR热更新.cmd」调用
#
# 为什么要关：外置注释数据（注释数据\fields\*.json、paths\*.json、relations\*.json）
# 是 **Go 侧在程序启动时**读取的，HMR 热更新只覆盖前端，不覆盖注释数据。
# 旧实例不关掉，界面会一直停留在旧注释/旧关联上（2026-10-02 反复踩的坑，踩坑表 #36）。
# 而启动脚本用 start 开新实例、不会关掉已有实例，所以必须在这里显式清理。
#
# 只清理「PVF工坊-开发人员专用*.exe」—— 正式交付版（PVF工坊<版本> ·编辑器110优化版…）
# 不在清理范围内，避免误伤正在给客户演示的正式版。
#
# 输出一行：KILLED <数量> 或 CLEAN（供 cmd 的 for /f 捕获后打印）。
# 设环境变量 KILL_STALE_APP_DRY=1 可只观察、不实际结束进程（供验证用）。
#
# 路径写死在工作台约定上，不需要传参（避免 cmd 里出现中文/引号转义问题）。

$dry = ($env:KILL_STALE_APP_DRY -eq '1')

$procs = @(Get-CimInstance Win32_Process -ErrorAction SilentlyContinue |
    Where-Object { $_.Name -like 'PVF工坊-开发人员专用*.exe' })

foreach ($p in $procs) {
    if (-not $dry) { Stop-Process -Id $p.ProcessId -Force -ErrorAction SilentlyContinue }
}

if ($procs.Count -gt 0) {
    Write-Output ('KILLED ' + $procs.Count)
}
else {
    Write-Output 'CLEAN'
}
exit 0
