@echo off
setlocal
if not exist dist mkdir dist
set CGO_ENABLED=0
set GOARM=7
for %%T in (windows/386 windows/amd64 windows/arm64 darwin/amd64 darwin/arm64 linux/386 linux/amd64 linux/arm linux/arm64 linux/mips64 linux/mips64le) do (
  for /f "tokens=1,2 delims=/" %%A in ("%%T") do (
    set GOOS=%%A
    set GOARCH=%%B
    if "%%A"=="windows" (
      go build -trimpath -ldflags "-s -w" -o dist\DDatHome-go-%%A-%%B.exe .
    ) else (
      go build -trimpath -ldflags "-s -w" -o dist\DDatHome-go-%%A-%%B .
    )
    if errorlevel 1 exit /b 1
  )
)
