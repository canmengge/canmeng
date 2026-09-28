@echo off
setlocal
REM ============================================================
REM  一键备份到服务器
REM   把整个 git 仓库（源码 + 全部提交历史 + 全部标签）打成一个
REM   bundle 文件，再用 scp 上传到服务器。
REM
REM  · 只上传，不在服务器上删除任何东西
REM  · 上传失败也会保留本地副本，不会丢
REM  · 想改服务器 / 端口 / 密钥，改下面这 4 行即可
REM ============================================================
set "ROOT=%~dp0.."
set "OUT=%ROOT%\07-归档资料\仓库备份"
set "HOST=root@101.34.252.212"
set "PORT=22"
set "KEY=d:\110AI\本人备份AI不要动\yunfuwuqi.pem"
set "REMOTE=/root/pvf-workbench-backup"

if not exist "%KEY%" (
  echo [错误] 找不到密钥：%KEY%
  pause
  exit /b 1
)
if not exist "%OUT%" mkdir "%OUT%"

for /f "delims=" %%i in ('powershell -NoProfile -Command "Get-Date -Format yyyyMMdd-HHmmss"') do set "TS=%%i"
set "FILE=pvf-workbench-%TS%.bundle"

echo === 1/3 打包仓库（含全部提交与标签）===
pushd "%ROOT%"
git bundle create "%OUT%\%FILE%" --all
if errorlevel 1 (
  echo [错误] 打包失败
  popd
  pause
  exit /b 1
)
popd

echo === 2/3 确保服务器目录存在 ===
ssh -i "%KEY%" -p %PORT% -o StrictHostKeyChecking=accept-new %HOST% "mkdir -p %REMOTE%"
if errorlevel 1 (
  echo [提示] 建目录失败（多半是连不上），仍继续尝试上传
)

echo === 3/3 上传 %FILE% ===
scp -i "%KEY%" -P %PORT% -o StrictHostKeyChecking=accept-new "%OUT%\%FILE%" %HOST%:%REMOTE%/%FILE%
if errorlevel 1 (
  echo.
  echo [错误] 上传失败。请检查：网络 / 端口 %PORT% / 密钥 %KEY%
  echo        本地已保留副本：%OUT%\%FILE%
  pause
  exit /b 1
)

echo.
echo 完成。
echo   服务器：%HOST%:%REMOTE%/%FILE%
echo   本地副本：%OUT%\%FILE%
pause
endlocal
