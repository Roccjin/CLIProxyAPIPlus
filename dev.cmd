@echo off
setlocal
cd /d "%~dp0"

where pwsh >nul 2>nul
if errorlevel 1 goto :missing

pwsh -NoProfile -ExecutionPolicy Bypass -File "%~dp0dev.ps1" %*
exit /b %ERRORLEVEL%

:missing
echo Need PowerShell 7+ ^(pwsh^) to run dev.ps1.
exit /b 1
