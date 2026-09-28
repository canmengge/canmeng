@echo off
setlocal
REM ===========================================================================
REM  刷新前端并启动（日常改 UI/TS 后双击这个）
REM  1) vite build，跳过 vue-tsc 类型检查（比 npm run build 快约一半）
REM  2) 以磁盘 dist 模式启动开发版，立刻看到改动
REM ===========================================================================
set "ROOT=%~dp0"
set "FE=%ROOT%..\01-源码\frontend"

cd /d "%FE%" || (echo [错误] 目录不存在：%FE% & pause & exit /b 1)

echo === 1/2 构建前端 build:fast ===
set "VITE_DEV_TOOLS=1"
call npm run build:fast
if errorlevel 1 (
  echo [错误] 前端构建失败，未启动
  pause
  exit /b 1
)

echo === 2/2 启动开发版 ===
call "%ROOT%启动开发版.cmd"
endlocal
