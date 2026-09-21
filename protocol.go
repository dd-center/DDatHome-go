package main

// Bilibili wire format and event mapping follow bilibili-live-ws and
// DDatHome-nodejs (MIT); see THIRD_PARTY_NOTICES.md.
import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/andybalholm/brotli"
)

func encodePacket(op uint32, body []byte) []byte {
	b := make([]byte, 16+len(body))
	binary.BigEndian.PutUint32(b, uint32(len(b)))
	binary.BigEndian.PutUint16(b[4:], 16)
	binary.BigEndian.PutUint16(b[6:], 1)
	binary.BigEndian.PutUint32(b[8:], op)
	binary.BigEndian.PutUint32(b[12:], 1)
	copy(b[16:], body)
	return b
}

type livePacket struct {
	op   uint32
	body []byte
}

func decodePackets(input []byte) ([]livePacket, error) {
	var result []livePacket
	budget, count := maxBodyBytes, 5000
	var unpack func([]byte, int) error
	unpack = func(b []byte, depth int) error {
		if depth > 3 || len(b) > budget {
			return errors.New("live decode budget exceeded")
		}
		budget -= len(b)
		for len(b) > 0 {
			count--
			if len(b) < 16 || count < 0 {
				return errors.New("invalid live packet header")
			}
			length, header := int64(binary.BigEndian.Uint32(b)), int64(binary.BigEndian.Uint16(b[4:]))
			ver, op := binary.BigEndian.Uint16(b[6:]), binary.BigEndian.Uint32(b[8:])
			if header < 16 || length < header || length > int64(len(b)) {
				return errors.New("invalid live packet length")
			}
			body := b[header:length]
			if ver == 2 || ver == 3 {
				var reader io.Reader
				var closeReader io.Closer
				if ver == 2 {
					r, err := zlib.NewReader(bytes.NewReader(body))
					if err != nil {
						return err
					}
					reader = r
					closeReader = r
				} else {
					reader = brotli.NewReader(bytes.NewReader(body))
				}
				expanded, err := io.ReadAll(io.LimitReader(reader, int64(budget)+1))
				if closeReader != nil {
					closeReader.Close()
				}
				if err != nil {
					return err
				}
				if err = unpack(expanded, depth+1); err != nil {
					return err
				}
			} else if ver <= 1 {
				if op == 3 && len(body) < 4 {
					return errors.New("short live heartbeat")
				}
				if (op == 5 || op == 8) && !json.Valid(body) {
					return errors.New("invalid live JSON")
				}
				if op == 3 || op == 5 || op == 8 {
					result = append(result, livePacket{op, body})
				}
			} else {
				return fmt.Errorf("unknown live protocol version %d", ver)
			}
			b = b[length:]
		}
		return nil
	}
	err := unpack(input, 0)
	return result, err
}

type relayEvent struct {
	RoomID int64  `json:"roomid"`
	Event  string `json:"e"`
	Data   any    `json:"data,omitempty"`
	Token  string `json:"token,omitempty"`
}

func mapEvent(room int64, body []byte) *relayEvent {
	var msg struct {
		Cmd  string         `json:"cmd"`
		Data map[string]any `json:"data"`
		Info []any          `json:"info"`
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	if d.Decode(&msg) != nil {
		return nil
	}
	cmd := strings.Split(msg.Cmd, ":")[0]
	e := &relayEvent{RoomID: room, Event: cmd}
	prefix := fmt.Sprintf("%d_%s_", room, cmd)
	data := msg.Data
	switch cmd {
	case "LIVE", "PREPARING", "ROUND":
		return e
	case "ROOM_CHANGE":
		title, ok := data["title"].(string)
		if !ok {
			return nil
		}
		e.Data = title
		e.Token = prefix + title
	case "DANMU_MSG":
		if len(msg.Info) < 3 {
			return nil
		}
		meta, ok := msg.Info[0].([]any)
		if !ok || len(meta) < 5 {
			return nil
		}
		if len(meta) > 9 && truthy(meta[9]) {
			return nil
		}
		user, ok := msg.Info[2].([]any)
		if !ok || len(user) < 2 {
			return nil
		}
		message, ok := msg.Info[1].(string)
		if !ok {
			return nil
		}
		if _, ok := user[0].(json.Number); !ok {
			return nil
		}
		if _, ok := user[1].(string); !ok {
			return nil
		}
		if _, ok := meta[4].(json.Number); !ok {
			return nil
		}
		e.Data = map[string]any{"message": message, "uname": user[1], "mid": user[0], "timestamp": meta[4]}
		e.Token = prefix + fmt.Sprint(user[0]) + "_" + fmt.Sprint(meta[4])
	case "SEND_GIFT":
		if !hasFields(data, "coin_type", "giftId", "total_coin", "uname", "uid", "tid") {
			return nil
		}
		e.Data = map[string]any{"coinType": data["coin_type"], "giftId": data["giftId"], "totalCoin": data["total_coin"], "uname": data["uname"], "mid": data["uid"]}
		e.Token = prefix + fmt.Sprint(data["uid"]) + "_" + fmt.Sprint(data["tid"])
	case "GUARD_BUY":
		if !hasFields(data, "uid", "username", "num", "price", "gift_id", "guard_level", "start_time") {
			return nil
		}
		e.Data = map[string]any{"mid": data["uid"], "uname": data["username"], "num": data["num"], "price": data["price"], "giftId": data["gift_id"], "level": data["guard_level"]}
		e.Token = prefix + fmt.Sprint(data["uid"]) + "_" + fmt.Sprint(data["start_time"])
	default:
		return nil
	}
	return e
}
func hasFields(m map[string]any, keys ...string) bool {
	for _, k := range keys {
		if m[k] == nil {
			return false
		}
	}
	return true
}
func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case json.Number:
		return x != "0"
	}
	return true
}
