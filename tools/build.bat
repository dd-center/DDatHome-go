@ECHO OFF
mkdir dist

::windows x64
set GOOS=windows
set GOARCH=amd64
go build -ldflags "-s -w" -o .\dist\DDatHome-windows-amd64.exe

::windows x32
set GOOS=windows
set GOARCH=386
go build -ldflags "-s -w" -o .\dist\DDatHome-windows-386.exe

::linux x64
set GOOS=linux
set GOARCH=amd64
go build -ldflags "-s -w" -o .\dist\DDatHome-linux-amd64

::linux x32
set GOOS=linux
set GOARCH=386
go build -ldflags "-s -w" -o .\dist\DDatHome-linux-386

::linux arm7
set GOOS=linux
set GOARCH=arm
set GOARM=7
go build -ldflags "-s -w" -o .\dist\DDatHome-linux-arm7

::linux arm64
set GOOS=linux
set GOARCH=arm64
go build -ldflags "-s -w" -o .\dist\DDatHome-linux-arm64
