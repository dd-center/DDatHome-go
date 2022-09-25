#!/usr/bin/fish

set projectName DDatHome-go
set dest dist
set ldflags "-s -w"
set targets windows/386 windows/amd64 darwin/arm64 darwin/amd64 linux/386 linux/amd64 linux/arm64 linux/mips64 linux/mips64le

if test -d $dest;
    rm -rv $dest;
end
mkdir -pv $dest;

for target in $targets;
    set splitedTarget (string split / $target)
    set platform $splitedTarget[1]
    set arch $splitedTarget[2]
    set fileName $projectName-$platform-$arch(if test $platform = windows; echo ".exe"; else; echo ""; end)
    GOOS=$platform GOARCH=$arch go build -ldflags $ldflags -o $dest/$fileName;
    echo "已构建 '$fileName'"
end
