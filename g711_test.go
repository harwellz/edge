package main

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

func TestAlawFromUlawTable(t *testing.T) {
	for _, test := range []struct{ ulaw, alaw byte }{
		{0xFF, 0xD5}, // +0
		{0x7F, 0xD5}, // -0
		{0x80, 0xAA}, // dương cực đại
		{0x00, 0x2A}, // âm cực đại
	} {
		if got := alawFromUlaw[test.ulaw]; got != test.alaw {
			t.Fatalf("ulaw %#x -> alaw %#x, muốn %#x", test.ulaw, got, test.alaw)
		}
	}
}

func TestForwardLocalPacketConvertsUlawAudio(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	bridge := newViewBridge(ctx, "", "front", nil, viewBridgeOffer{}, func(json.RawMessage) {})
	bridge.audioUlaw.Store(true)
	var written []byte
	bridge.writeRTP = func(_ *webrtc.TrackLocalStaticRTP, packet *rtp.Packet) error {
		written = append([]byte(nil), packet.Payload...)
		return nil
	}
	audio, _ := newRTPTrack(webrtc.RTPCodecCapability{
		MimeType: webrtc.MimeTypePCMA, ClockRate: 8000, Channels: 1,
	}, "audio", "edge")
	bootstrapReady := false
	var once sync.Once
	ok := bridge.forwardLocalPacket(webrtc.RTPCodecTypeAudio, audio, &rtp.Packet{
		Header:  rtp.Header{Version: 2, PayloadType: 0, SequenceNumber: 1, Timestamp: 160, SSRC: 3},
		Payload: bytes.Repeat([]byte{0xFF}, 160), // ulaw im lặng
	}, &bootstrapReady, &once, make(chan struct{}))
	if !ok {
		t.Fatal("audio bị drop")
	}
	if len(written) != 160 {
		t.Fatalf("ghi %d byte, muốn 160", len(written))
	}
	for i, sample := range written {
		if sample != 0xD5 {
			t.Fatalf("byte %d = %#x, muốn 0xD5 (alaw im lặng)", i, sample)
		}
	}
}
