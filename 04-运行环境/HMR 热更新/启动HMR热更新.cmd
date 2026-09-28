@echo off
setlocal
REM ============================================================================
REM  PVF工坊 - HMR 热更新模式（日常改前端/UI 用这个，双击即测）
REM   1) 起前端开发服务器（另开一个窗口，别关它）
REM   2) 以「外部开发服务器」模式启动程序：界面资源实时取源码，不走内嵌资源
REM   3) 之后凡是改前端源码 → 保存 → 程序界面约 1 秒自动更新
REM  - 本模式不需要构建前端、不需要重新打包 exe
REM  - 后端接口（打开 PVF / 保存 / 索引 / 注释 / AI）仍由程序自身处理，不受影响
REM  - 正式测试仍用同目录上级的「启动开发版.cmd」（内嵌资源模式）
REM ============================================================================
set "HERE=%~dp0"
for %%I in ("%HERE%..") do set "CLIENT=%%~fI"
for %%I in ("%HERE%..\..") do set "ROOT=%%~fI"
set "FE=%ROOT%\01-源码\frontend"
set "PORT=9255"

if not exist "%FE%\package.json" (
  echo [错误] 找不到前端源码目录：%FE%
  echo        本脚本必须放在 04-运行环境\HMR 热更新\ 下
  pause
  exit /b 1
)

echo === 1/3 启动前端开发服务器（另开窗口，请勿关闭）===
start "PVF工坊-前端开发服务器(勿关)" "%HERE%_前端开发服务器.cmd"

echo === 2/3 等待开发服务器就绪 127.0.0.1:%PORT% ...
powershell -NoProfile -ExecutionPolicy Bypass -Command "$end=(Get-Date).AddSeconds(180); while((Get-Date) -lt $end){ try { $c=New-Object Net.Sockets.TcpClient; $c.Connect('127.0.0.1',%PORT%); $c.Close(); exit 0 } catch { Start-Sleep -Milliseconds 400 } }; exit 1"
if errorlevel 1 (
  echo [错误] 开发服务器 180 秒内未就绪，请看「前端开发服务器」窗口的报错信息
  pause
  exit /b 1
)

echo === 3/3 启动 PVF工坊（热更新模式）===
set "FRONTEND_DEVSERVER_URL=http://localhost:%PORT%"
set "PVFINE_ANNOTATION_DIR=%CLIENT%\注释数据"
set "PVFINE_CACHE_DIR=%CLIENT%\pvfine-main\HC"
set "PVFINE_KNOWLEDGE_DIR=%CLIENT%\知识库"

set "EXE="
for /f "delims=" %%F in ('dir /b /o-d "%CLIENT%\PVF工坊-开发人员专用*.exe" 2^>nul') do (
  if not defined EXE set "EXE=%CLIENT%\%%F"
)
if not defined EXE set "EXE=%ROOT%\05-构建输出\pvfine-devkit.exe"
if not exist "%EXE%" (
  echo [错误] 找不到程序：%EXE%
  pause
  exit /b 1
)
echo 启动程序: %EXE%
start "" "%EXE%"
endlocal
