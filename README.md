# DDatHome-go
DD@Home in golang

## 如何直接使用
#### 首先下载发布成品
https://github.com/dd-center/DDatHome-go/releases


#### 直接运行相应操作系统版本
举例
```
./DDatHome-go-linux-amd64
```
来在64位linux下运行

windows你可以直接双击exe开始运行
#### 作为系统服务安装
支持Windows和Linux，需要管理员权限，服务名：DDatHome
```
./DDatHome-go-linux-amd64 install
```
卸载服务
```
./DDatHome-go-linux-amd64 uninstall
```

#### 创建并且配置文件 "config.json"
配置文件需要和主程序放在同一个目录下
```
{
  "nickname":"DD", //这里是昵称
  "interval":500   //这里是任务处理间隔(单位: ms)
}
```

## Docker版DDatHome-go
Pull下载
```
sudo docker pull imlonghao/ddathome-go
```
测试运行
```
sudo docker run imlonghao/ddathome-go
```
长期后台运行
```
sudo docker run -d imlonghao/ddathome-go
```
## 如何从头编译
1. 去[官网下载](https://go.dev/dl/)并安装符合你操作系统的Go

2. 下载本项目：https://github.com/dd-center/DDatHome-go/archive/refs/heads/master.zip

3. 解压，在Windows上可以直接双击build.bat来编译，在linux或者Mac上用
```
go build main.go
```

4. 去编译好后的bin文件夹运行你需要的版本，可按照上方使用说明进行操作
