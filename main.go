package main

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/goccy/go-json"
	"github.com/kardianos/service"
	"golang.org/x/net/websocket"
)

const PROGRAM_NAME = "DDatHome-go"
const VERSION = "1.1.1"

type Program struct {
	Configs Config
	ws      *websocket.Conn
}

type Config struct {
	NickName string `json:"NickName"`
	Interval int    `json:"Interval"`
	UUID     string `json:"UUID"`
	URL      string `json:"UpstreamURL"`
	Hide     bool   `json:"HidePlatformInfo"`
}

type Result struct {
	Key  string `json:"key"`
	Data struct {
		Type string `json:"type"`
		URL  string `json:"url"`
	} `json:"data"`
}

type Response struct {
	Key   string `json:"key"`
	Data  string `json:"data"`
	Error string `json:"error"`
}

func (c *Config) getUpstreamURL() string {
	u, err := url.Parse(c.URL)
	if err != nil {
		panic(err)
	}

	v := url.Values{}
	if !c.Hide {
		v.Add("runtime", "go")
		v.Add("version", VERSION)
		v.Add("platform", runtime.GOOS)
	}
	if c.UUID != "" {
		re := regexp.MustCompile("^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$")
		if re.Match([]byte(c.UUID)) {
			v.Add("uuid", c.UUID)
		} else {
			fmt.Println("Incorrect uuid format, ignore it")
		}
	}
	if c.NickName != "" {
		v.Add("name", c.NickName)
	}

	u.RawQuery = v.Encode()
	return u.String()
}

func (p *Program) Start(s service.Service) error {
	go p.run()
	return nil
}

func (p *Program) Stop(s service.Service) error {
	return nil
}

func (p *Program) run() {
	urls := p.Configs.getUpstreamURL()

	fmt.Println("Dial", urls)
	connect := func() error {
		conn, err := websocket.Dial(urls, "", "https://cluster.vtbs.moe")
		if err != nil {
			return err
		}
		p.ws = conn
		return nil
	}
	if err := connect(); err != nil {
		panic(err)
	}
	for {
		time.Sleep(time.Millisecond * time.Duration(p.Configs.Interval))
		_, err := p.ws.Write([]byte("DDhttp"))
		if err != nil {
			_ = p.ws.Close()
			for connect() != nil {
				_ = p.ws.Close()
				time.Sleep(time.Millisecond * time.Duration(500))
			}
			fmt.Println("reconnect success.")
			continue
		}
		buf := make([]byte, 1024*100) //100k
		dataLen, err := p.ws.Read(buf)
		if err != nil {
			fmt.Println("error to read websocket:", err)
			continue
		}
		data, key, err := Processor(buf[:dataLen])
		res := &Response{
			Key:  key,
			Data: data,
		}
		if err != nil {
			res.Error = err.Error()
		}
		json, err := json.Marshal(res)
		if err != nil {
			fmt.Println("json error:", err)
			continue
		}
		_, err = p.ws.Write(json)
		if err != nil {
			fmt.Println("error to write websocket:", err)
			continue
		}
	}
}

func main() {
	fmt.Printf("%s - v%s, running using %s %s.\n", PROGRAM_NAME, VERSION, runtime.GOOS, runtime.GOARCH)
	svcConfig := &service.Config{
		Name:        PROGRAM_NAME,
		DisplayName: "DD@Home",
		Description: "DD@home Service",
	}
	prg := &Program{}
	prg.Configs = GetConfig()
	s, err := service.New(prg, svcConfig)
	if err != nil {
		fmt.Println(err)
	}

	if len(os.Args) > 1 {
		if os.Args[1] == "install" {
			err := s.Install()
			if err != nil {
				fmt.Println("Service install failed:", err.Error())
				return
			}
			fmt.Println("Service install successfully!")
			return
		}

		if os.Args[1] == "uninstall" {
			err := s.Uninstall()
			if err != nil {
				fmt.Println("Service uninstall failed", err.Error())
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
	var loadedPayload Result
	err := json.Unmarshal(payload, &loadedPayload)
	if err != nil {
		fmt.Println("error:", err)
		return "", "", err
	}

	if loadedPayload.Data.Type != "http" {
		fmt.Println("task", loadedPayload.Key, "un-support type", loadedPayload.Data.Type)
		return "", loadedPayload.Key, errors.New("un-support data type")
	}
	data, err := GetString(loadedPayload.Data.URL)
	if err != nil {
		fmt.Println("task", loadedPayload.Key, "error:", err)
		return "", loadedPayload.Key, err
	}
	fmt.Println("task", loadedPayload.Key, "handled, url:", loadedPayload.Data.URL)
	return data, loadedPayload.Key, nil
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
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if strings.Contains(resp.Header.Get("Content-Encoding"), "gzip") {
		buffer := bytes.NewBuffer(body)
		r, _ := gzip.NewReader(buffer)
		unCom, err := io.ReadAll(r)
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

func Exists(path string) bool {
	_, err := os.Stat(path)
	if err != nil {
		return os.IsExist(err)
	}
	return true
}

func GetConfig() Config {
	var readedConfig []byte
	var getedConfigs Config
	var err error
	workingDir, _ := os.Getwd()
	fileName, _ := filepath.Abs(workingDir + "/config.json")
	if Exists(fileName) {
		readedConfig, err = os.ReadFile(fileName)
		if err != nil {
			panic(err)
		}
	} else {
		readedConfig = []byte(
			`{
	"NickName": "goDD",
	"Interval": 1280,
	"UUID": null,
	"UpstreamURL": "wss://cluster.vtbs.moe/",
	"HidePlatformInfo": false
}`)
		os.WriteFile(fileName, readedConfig, 0644)
	}
	err = json.Unmarshal(readedConfig, &getedConfigs)
	if err != nil {
		panic(err)
	}
	return getedConfigs
}
