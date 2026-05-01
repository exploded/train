@echo off
REM Build and run Train application

echo Building Train...
go build -o train.exe
if %ERRORLEVEL% NEQ 0 (
    echo Build failed!
    pause
    exit /b 1
)

if not exist .env (
    echo WARNING: .env file not found! Creating from .env.example
    copy .env.example .env
    echo Edit .env with your secrets, then run again.
    pause
    exit /b 1
)

echo Loading environment variables from .env...
for /f "usebackq tokens=1,* delims==" %%a in (.env) do (
    echo %%a | findstr /r "^#" >nul
    if errorlevel 1 (
        if not "%%a"=="" set "%%a=%%b"
    )
)

echo Starting on http://localhost:%PORT%
train.exe
