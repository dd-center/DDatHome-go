package main

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"encoding/json"
	"strings"
	"testing"

	"github.com/andybalholm/brotli"
)

func compressedPacket(ver uint16, body []byte) []byte {
	var b bytes.Buffer
	if ver == 2 {
		w := zlib.NewWriter(&b)
		w.Write(body)
		w.Close()
	} else {
		w := brotli.NewWriter(&b)
		w.Write(body)
		w.Close()
	}
	p := encodePacket(5, b.Bytes())
	binary.BigEndian.PutUint16(p[6:], ver)
	return p
}
func TestLivePacketCompressionAndConcatenation(t *testing.T) {
	for _, ver := range []uint16{2, 3} {
		body := append(encodePacket(8, []byte(`{"code":0}`)), encodePacket(5, []byte(`{"cmd":"LIVE"}`))...)
		p, err := decodePackets(compressedPacket(ver, body))
		if err != nil || len(p) != 2 || p[1].op != 5 {
			t.Fatal(p, err)
		}
	}
	p, err := decodePackets(encodePacket(3, []byte{0, 0, 0, 7}))
	if err != nil || binary.BigEndian.Uint32(p[0].body) != 7 {
		t.Fatal(p, err)
	}
}
func TestLivePacketRejectsBadAndOversizedFrames(t *testing.T) {
	badLength := encodePacket(5, []byte(`{}`))
	binary.BigEndian.PutUint32(badLength, 99999)
	badHeader := encodePacket(5, nil)
	binary.BigEndian.PutUint16(badHeader[4:], 1)
	tooDeep := encodePacket(5, []byte(`{}`))
	for range 5 {
		tooDeep = compressedPacket(2, tooDeep)
	}
	for _, b := range [][]byte{{1}, badLength, badHeader, encodePacket(3, nil), encodePacket(5, []byte(`{`)), compressedPacket(2, []byte(strings.Repeat("x", maxBodyBytes+1))), compressedPacket(3, []byte(strings.Repeat("x", maxBodyBytes+1))), tooDeep, bytes.Repeat(encodePacket(5, []byte(`{}`)), 5001)} {
		if _, err := decodePackets(b); err == nil {
			t.Fatal("accepted invalid/bomb packet")
		}
	}
}

func TestRelayMappingMatchesNode(t *testing.T) {
	for _, tc := range []struct{ raw, expected string }{
		{`{"cmd":"LIVE"}`, `{"roomid":1,"e":"LIVE"}`},
		{`{"cmd":"PREPARING"}`, `{"roomid":1,"e":"PREPARING"}`},
		{`{"cmd":"ROUND"}`, `{"roomid":1,"e":"ROUND"}`},
		{`{"cmd":"ROOM_CHANGE","data":{"title":"标题"}}`, `{"roomid":1,"e":"ROOM_CHANGE","data":"标题","token":"1_ROOM_CHANGE_标题"}`},
		{`{"cmd":"DANMU_MSG:4:0:2:2:2:0","info":[[0,0,0,0,123,0,0,0,0,0],"你好",[9007199254740991,"DD"]]}`, `{"roomid":1,"e":"DANMU_MSG","data":{"message":"你好","uname":"DD","mid":9007199254740991,"timestamp":123},"token":"1_DANMU_MSG_9007199254740991_123"}`},
		{`{"cmd":"SEND_GIFT","data":{"coin_type":"gold","giftId":2,"total_coin":300,"uname":"DD","uid":9,"tid":"abc"}}`, `{"roomid":1,"e":"SEND_GIFT","data":{"coinType":"gold","giftId":2,"totalCoin":300,"uname":"DD","mid":9},"token":"1_SEND_GIFT_9_abc"}`},
		{`{"cmd":"GUARD_BUY","data":{"uid":9,"username":"DD","num":1,"price":100,"gift_id":2,"guard_level":3,"start_time":123}}`, `{"roomid":1,"e":"GUARD_BUY","data":{"mid":9,"uname":"DD","num":1,"price":100,"giftId":2,"level":3},"token":"1_GUARD_BUY_9_123"}`},
	} {
		got := mapEvent(1, []byte(tc.raw))
		b, _ := json.Marshal(got)
		var wantMap, gotMap any
		json.Unmarshal([]byte(tc.expected), &wantMap)
		json.Unmarshal(b, &gotMap)
		want, _ := json.Marshal(wantMap)
		actual, _ := json.Marshal(gotMap)
		if string(want) != string(actual) {
			t.Fatalf("%s\nwant %s\ngot %s", tc.raw, want, actual)
		}
	}
	for _, raw := range []string{`null`, `{}`, `{"cmd":"DANMU_MSG","info":[[]]}`, `{"cmd":"SEND_GIFT"}`, `{"cmd":"DANMU_MSG","info":[[0,0,0,0,123,0,0,0,0,1],"emote",[1,"DD"]]}`} {
		if mapEvent(1, []byte(raw)) != nil {
			t.Fatal(raw)
		}
	}
}

func FuzzDecodePackets(f *testing.F) {
	f.Add(encodePacket(5, []byte(`{"cmd":"LIVE"}`)))
	f.Add(compressedPacket(3, encodePacket(8, []byte(`{"code":0}`))))
	f.Add([]byte{0, 0, 0, 0})
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) <= maxBodyBytes {
			decodePackets(b)
		}
	})
}
