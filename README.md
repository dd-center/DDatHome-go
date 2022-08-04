# DDatHome-go

DD@Home in golang

## 如何直接使用

### 首先下载发布成品

* [Releases 版](https://github.com/dd-center/DDatHome-go/releases)
* [Ci 版](https://github.com/dd-center/DDatHome-go/actions/workflows/go.yml)

#### 直接运行相应操作系统版本

* Windows: 双击 exe 文件直接运行

* Linux:

```sh
# 请先确认可执行文件的路径
# 此处默认你的可执行文件在当前位置且文件名为 DDatHome-go-linux-amd64

# 在 Linux 环境下你可能需要先为其添加可执行权限
chmod +x DDatHome-go-linux-amd64

# 执行以启动 DDatHome-go
./DDatHome-go-linux-amd64
```

#### 作为系统服务安装

支持Windows和Linux，需要管理员权限，服务名：DDatHome

```sh
./DDatHome-go-linux-amd64 install
```

卸载服务

```sh
./DDatHome-go-linux-amd64 uninstall
```

#### Docker

Pull下载

```sh
sudo docker pull imlonghao/ddathome-go
```

测试运行

```sh
sudo docker run imlonghao/ddathome-go
```

长期后台运行

```sh
sudo docker run -d imlonghao/ddathome-go
```

## 配置文件

配置文件 (config.json) 需要和主程序放在同一个目录下

```json
{
 "NickName": null, // 昵称
 "Interval": 1280, // 任务处理间隔 (单位: ms)
 "UUID": null, // UUID, 用于数据追踪
 "UpstreamURL": "wss://cluster.vtbs.moe/", // 上游地址
 "HidePlatformInfo": false // 隐藏有关本机的相关信息, 包括运行时名称，本程序版本与平台名
}
```

## 如何从头编译

1. 去[官网](https://go.dev/dl/)下载并安装符合你操作系统的Go

2. 下载[本项目](https://github.com/dd-center/DDatHome-go/archive/refs/heads/master.zip)

3. 解压，在Windows上可以直接双击build.bat来编译，在linux或者Mac上用

   ```sh
   go build
   ```

4. 去编译好后的bin文件夹运行你需要的版本，可按照上方使用说明进行操作

## 依赖

|库名称                                          |版本  |
|-----------------------------------------------|------|
|[go-json](https://github.com/goccy/go-json)    |0.9.10|
|[service](https://github.com/kardianos/service)|1.2.1 |
