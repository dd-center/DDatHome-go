package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/kardianos/service"
	"github.com/tidwall/gjson"
	"golang.org/x/net/websocket"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type program struct{}

func (p *program) Start(s service.Service) error {
	go p.run()
	return nil
}

type GoResult struct {
	Key   string `json:"key"`
	Data  string `json:"data"`
	Error string `json:"error"`
}

var (
	ddName   string  = "DD"
	interval float64 = 500
	version  string  = "1.0.0"
	ws       *websocket.Conn
)

func (p *program) run() {
	FileName := getCurrentDirectory() + "/config.json"
	if Exists(FileName) {
		b, err := ioutil.ReadFile(FileName)
		if err != nil {
			panic(err)
		}
		jsons := gjson.Parse(string(b))
		ddName = jsons.Get("nickname").Str
		interval = jsons.Get("interval").Num
	}

	urls := "wss://cluster.vtbs.moe/?runtime=go&version=" + version + "&platform=" + runtime.GOOS + "&name=" + url.QueryEscape(ddName)

	fmt.Println("Dial", urls)
	connect := func() error {
		conn, err := websocket.Dial(urls, "", "https://cluster.vtbs.moe")
		if err != nil {
			return err
		}
		ws = conn
		return nil
	}
	if err := connect(); err != nil {
		panic(err)
	}
	for {
		time.Sleep(time.Millisecond * time.Duration(interval))
		_, err := ws.Write([]byte("DDhttp"))
		if err != nil {
			_ = ws.Close()
			if err := connect(); err != nil {
				panic(err)
			}
			fmt.Println("reconnect success.")
			continue
		}
		buf := make([]byte, 1024*100) //100k
		dataLen, err := ws.Read(buf)
		if err != nil {
			fmt.Println("error to read websocket:", err)
			continue
		}
		data, key, err := Processor(buf[:dataLen])
		res := &GoResult{
			Key:  key,
			Data: data,
		}
		if err != nil {
			res.Error = err.Error()
		}
		json, _ := json.Marshal(res)
		_, _ = ws.Write(json)
	}
}

func (p *program) Stop(s service.Service) error {
	return nil
}

func main() {
	svcConfig := &service.Config{
		Name:        "DDatHome",
		DisplayName: "DD@Home",
		Description: "DD@home Service",
	}
	prg := &program{}
	s, err := service.New(prg, svcConfig)
	if err != nil {
		fmt.Println(err)
	}

	if err != nil {
		fmt.Println(err)
	}

	if len(os.Args) > 1 {
		if os.Args[1] == "install" {
			err := s.Install()
			if err != nil {
				fmt.Println("Service install failed: " + err.Error())
				return
			}
			fmt.Println("Service install successfully!")
			return
		}

		if os.Args[1] == "uninstall" {
			err := s.Uninstall()
			if err != nil {
				fmt.Println("Service uninstall failed" + err.Error())
				return
			}
			fmt.Println("Service uninstall successfully!")
			return
		}
	}

	err = s.Run()
	if err != nil {
		fmt.Println(err)
	}
}

func Processor(payload []byte) (string, string, error) {
	json := gjson.Parse(string(payload))
	key := json.Get("key").Str
	if json.Get("data.type").Str != "http" {
		fmt.Println("task", key, "un-support type", json.Get("data.type").Str)
		return "", key, errors.New("un-support data type")
	}
	data, err := GetString(json.Get("data.url").Str)
	if err != nil {
		fmt.Println("task", key, "error:", err)
		return "", key, err
	}
	//fmt.Println("task", key, "handled, url:", json.Get("data.url").Str)
	return data, key, nil
}

func GetBytes(url string) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if strings.Contains(resp.Header.Get("Content-Encoding"), "gzip") {
		buffer := bytes.NewBuffer(body)
		r, _ := gzip.NewReader(buffer)
		unCom, err := ioutil.ReadAll(r)
		return unCom, err
	}
	return body, nil
}

func GetString(url string) (string, error) {
	bytes, err := GetBytes(url)
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}

func getCurrentDirectory() string {
	dir, err := filepath.Abs(filepath.Dir(os.Args[0]))
	if err != nil {
		panic(err)
	}
	return strings.Replace(dir, "\\", "/", -1)
}

func Exists(path string) bool {
	_, err := os.Stat(path)
	if err != nil {
		if os.IsExist(err) {
			return true
		}
		return false
	}
	return true
}
