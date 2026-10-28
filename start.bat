@echo off
setlocal
pushd "%~dp0"
if errorlevel 1 exit /b 1

if not exist "aistudio2api.exe" (
    echo Missing aistudio2api.exe. Download this fork's Windows Release or build the desktop app first.
    echo Source requirements: Go 1.27+ and Node.js 24.15+ with npm 11. See README.md.
    popd
    exit /b 1
)

rem Wait for both desktop and setup commands, forwarding current CLI options unchanged.
start "" /wait "%CD%\aistudio2api.exe" %*
set "EXIT_CODE=%ERRORLEVEL%"
popd
exit /b %EXIT_CODE%
