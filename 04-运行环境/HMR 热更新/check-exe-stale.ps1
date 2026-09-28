# check-exe-stale.ps1 — 供「启动HMR热更新.cmd」调用的一致性自检
#
# 背景（2026-09-29 真实事故）：启动脚本只挑「文件时间最新」的 exe 启动，
# 从不重新编译 Go。若改了 Go 却没先重新构建，跑的就是旧内核 —— 用户会误
# 以为修复无效、白跑一轮。
#
# 输出（仅一行，供 cmd 的 for /f 捕获）：
#   STALE = 01-源码 下的 .go 比目标目录里最新的 exe 更新 → 需要先构建
#   OK    = 一致（或无法判断，宁可静默不误报）
#
# 路径通过环境变量传入，避免本文件里出现中文（PowerShell 5.1 按 ANSI 读无 BOM 文件）：
#   PVF_CHK_SRC        Go 源码根目录
#   PVF_CHK_EXE_DIR    存放 exe 的目录
#   PVF_CHK_EXE_PAT    exe 文件名模式

$src = $env:PVF_CHK_SRC
$dir = $env:PVF_CHK_EXE_DIR
$pat = $env:PVF_CHK_EXE_PAT

if ([string]::IsNullOrWhiteSpace($src) -or [string]::IsNullOrWhiteSpace($dir)) {
    Write-Output 'OK'
    exit 0
}

try {
    $newestGo = Get-ChildItem -LiteralPath $src -Recurse -Include *.go -File -ErrorAction SilentlyContinue |
        Sort-Object LastWriteTime -Descending | Select-Object -First 1
    $newestExe = Get-ChildItem -LiteralPath $dir -Filter $pat -File -ErrorAction SilentlyContinue |
        Sort-Object LastWriteTime -Descending | Select-Object -First 1
    if ($newestGo -and $newestExe -and ($newestGo.LastWriteTime -gt $newestExe.LastWriteTime)) {
        Write-Output 'STALE'
    }
    else {
        Write-Output 'OK'
    }
}
catch {
    Write-Output 'OK'
}
exit 0
