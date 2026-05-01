@echo off
REM Run train.exe (assumes build.bat already produced it)

if not exist train.exe (
    echo train.exe not found, running build.bat first
    call build.bat
    exit /b
)
if not exist .env (
    echo ERROR: .env file not found
    pause
    exit /b 1
)

for /f "usebackq tokens=1,* delims==" %%a in (.env) do (
    echo %%a | findstr /r "^#" >nul
    if errorlevel 1 (
        if not "%%a"=="" set "%%a=%%b"
    )
)

echo Starting on http://localhost:%PORT%
train.exe
