package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/tidwall/gjson"
	"golang.org/x/net/websocket"
	"io/ioutil"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"
)

type GoResult struct {
	Key   string `json:"key"`
	Data  string `json:"data"`
	Error string `json:"error"`
}

var (
	ws *websocket.Conn
)

func main() {
	name := "dd-go"
	if len(os.Args) > 1 {
		name = strings.Join(os.Args[1:], " ")
	}
	url := "wss://cluster.vtbs.moe/?runtime=" + runtime.Version() + "&version=0.3&platform=" + runtime.GOOS + "@" + runtime.GOARCH + "&name=" + name
	fmt.Println("Dial", url)
	connect := func() error {
		conn, err := websocket.Dial(url, "", "https://cluster.vtbs.moe")
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
		time.Sleep(time.Millisecond * 500)
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
	fmt.Println("task", key, "handled, url:", json.Get("data.url").Str)
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
