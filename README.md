# DDatHome-go
DD@Home in golang

#### 直接运行
```
./DDatHome
```

#### 作为系统服务安装
支持Windows和Linux，需要管理员权限，服务名：DDatHome
```
./DDatHome install
```
卸载服务
```
./DDatHome uninstall
```

#### 配置文件 "config.json"
配置文件需要和主程序放在同一个目录下
```
{
  "nickname":"DD", //这里是昵称
  "interval":500   //这里是任务处理间隔(单位: ms)
}
```
