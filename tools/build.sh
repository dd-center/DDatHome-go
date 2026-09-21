#!/bin/sh
set -eu
mkdir -p dist
version=${VERSION:-2.0.0-dev}
for target in windows/386 windows/amd64 windows/arm64 darwin/amd64 darwin/arm64 linux/386 linux/amd64 linux/arm linux/arm64 linux/mips64 linux/mips64le; do
  target_os=${target%/*}
  target_arch=${target#*/}
  suffix=
  if [ "$target_os" = windows ]; then suffix=.exe; fi
  CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" GOARM=7 \
    go build -trimpath -ldflags="-s -w -X main.version=$version" -o "dist/DDatHome-go-$target_os-$target_arch$suffix" .
done
