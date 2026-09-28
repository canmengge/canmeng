@echo off
setlocal
REM ===========================================================================
REM  启动开发版（磁盘资源模式）—— 仅作为 HMR 不可用时的回退
REM  - 日常请用「HMR 热更新\启动HMR热更新.cmd」（唯一日常启动入口，2026-09-28 规定）
REM  - 界面资源不读 exe 内嵌，而是实时读 frontend\dist-dev
REM  - 因此前端改动只要重新 vite build，不必再 go build 打包 exe
REM  - Go 侧代码改动才需要重新构建这个程序
REM ===========================================================================
set "ROOT=%~dp0"
set "V2=%ROOT%..\01-源码"
REM  磁盘资源目录固定用 frontend\dist-dev（与正式构建产物 dist 分离，
REM  这样即使程序正在运行，也能重新构建，不会因文件被占用而失败）
set "DIST=%V2%\frontend\dist-dev"

if not exist "%DIST%\index.html" (
  echo [错误] 找不到前端资源：%DIST%\index.html
  echo 请先双击「刷新前端并启动.cmd」，或在 %V2%\frontend 下执行 npm run build:fast
  pause
  exit /b 1
)

REM  优先启动**最新构建**：按修改时间挑最新的 PVF工坊-开发人员专用*.exe。
REM  这样即使主名 exe 正被旧实例占用、新内核只能落到带版本号的文件上，也能用上最新版。
set "EXE="
for /f "delims=" %%F in ('dir /b /o-d "%ROOT%PVF工坊-开发人员专用*.exe" 2^>nul') do (
  if not defined EXE set "EXE=%ROOT%%%F"
)
if not defined EXE set "EXE=%ROOT%..\05-构建输出\pvfine-devkit.exe"
if not exist "%EXE%" (
  echo [错误] 找不到程序：%EXE%
  pause
  exit /b 1
)

set "PVFINE_DEV_DIST=%DIST%"
REM 数据目录固定到「测试客户端」（与正式版一致）：无论这个 exe 从哪里启动，
REM 注释数据 / 缓存 / 知识库都读同一份，避免换 exe 位置后左树注释丢失。
set "PVFINE_ANNOTATION_DIR=%ROOT%注释数据"
set "PVFINE_CACHE_DIR=%ROOT%pvfine-main\HC"
set "PVFINE_KNOWLEDGE_DIR=%ROOT%知识库"
REM 日志固定落在 04-运行环境\日志（与缓存/索引目录分离，便于直接查看）
set "PVFINE_LOG_DIR=%ROOT%日志"
if not exist "%ROOT%日志" mkdir "%ROOT%日志"
echo 资源目录: %DIST%
echo 日志目录: %ROOT%日志
echo 启动程序: %EXE%
start "" "%EXE%"
endlocal
