@echo off
setlocal
REM ============================================================
REM  构建开发版 —— 改了 Go 内核（main.go / services / internal）后双击这个
REM    源码 = 01-源码     构建产物 = 05-构建输出     运行区 = 04-运行环境
REM    用法：直接双击；或命令行带版本号：  构建开发版.cmd 4.4
REM  前端若也改了：先双击 04-运行环境\刷新前端并启动.cmd（或走 HMR 通道）
REM ============================================================
set "ROOT=%~dp0.."
set "SRC=%ROOT%\01-源码"
set "OUT=%ROOT%\05-构建输出"
set "RUN=%ROOT%\04-运行环境"
set "VER=%~1"
if "%VER%"=="" set "VER=dev"

if not exist "%SRC%\go.mod" (
  echo [错误] 找不到源码目录：%SRC%
  pause
  exit /b 1
)

echo === 1/3 构建 Go 内核 ===
pushd "%SRC%"
go build -trimpath -ldflags "-s -w" -o "%OUT%\pvfine-devkit.exe" .
if errorlevel 1 (
  echo [错误] go build 失败
  popd
  pause
  exit /b 1
)
popd

for /f "delims=" %%i in ('powershell -NoProfile -Command "Get-Date -Format yyyyMMdd-HHmmss"') do set "TS=%%i"
set "NAME=PVF工坊-开发人员专用-%VER%-%TS%.exe"

echo === 2/3 落新文件名（永不覆盖正在运行的文件）===
copy /y "%OUT%\pvfine-devkit.exe" "%RUN%\%NAME%" >nul
if errorlevel 1 (
  echo [错误] 复制失败：%RUN%\%NAME%
  pause
  exit /b 1
)

echo === 3/3 尽力更新主名（被占用则跳过、不报错）===
copy /y "%OUT%\pvfine-devkit.exe" "%RUN%\PVF工坊-开发人员专用.exe" >nul 2>nul
if errorlevel 1 (
  echo   （主名被占用，已跳过；启动时会自动挑最新的 %NAME%）
) else (
  echo   主名已更新
)

echo.
echo 完成。双击 04-运行环境\启动开发版.cmd 测试。
pause
endlocal
