package main

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

// App (libwebrtc) liệt kê Opus trước, PCMA gần cuối trong offer audio. Pion giữ
// thứ tự codec của remote khi answer, nên gói PCMA phải ra đúng PT 8 — nếu Pion
// gắn sai codec (Opus 111) thì iOS decode rác và im tiếng. Test này khoá đúng
// shape offer đó để chứng minh audio từ go2rtc tới được client.
func TestViewBridgeForwardsAudioToLibwebrtcShapedClient(t *testing.T) {
	go2rtc, tracks := fakeGo2RTCServer(t, true)

	client, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	for i := 0; i < 2; i++ {
		if _, err = client.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{
			Direction: webrtc.RTPTransceiverDirectionRecvonly,
		}); err != nil {
			t.Fatal(err)
		}
	}
	audioTransceiver, err := client.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio, webrtc.RTPTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionRecvonly,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = audioTransceiver.SetCodecPreferences([]webrtc.RTPCodecParameters{
		{RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2}},
		{RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeG722, ClockRate: 8000}},
		{RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypePCMU, ClockRate: 8000}},
		{RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypePCMA, ClockRate: 8000, Channels: 1}},
	}); err != nil {
		t.Fatal(err)
	}
	var audioPackets atomic.Int64
	var audioPayloadType atomic.Int32
	client.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		if track.Kind() != webrtc.RTPCodecTypeAudio {
			for {
				if _, _, err := track.ReadRTP(); err != nil {
					return
				}
			}
		}
		audioPayloadType.Store(int32(track.PayloadType()))
		for {
			if _, _, err := track.ReadRTP(); err != nil {
				return
			}
			audioPackets.Add(1)
		}
	})
	offer, err := client.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	gather := webrtc.GatheringCompletePromise(client)
	if err = client.SetLocalDescription(offer); err != nil {
		t.Fatal(err)
	}
	<-gather

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	bridge := newViewBridge(ctx, go2rtc.URL, "front", nil, viewBridgeOffer{SDP: client.LocalDescription().SDP}, func(payload json.RawMessage) {
		applyBridgeSignal(t, client, payload)
	})
	defer bridge.close()
	video, _ := newRTPTrack(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264, ClockRate: 90000}, "video", "edge")
	audio, _ := newRTPTrack(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypePCMA, ClockRate: 8000, Channels: 1}, "audio", "edge")
	hd, _ := newRTPTrack(video.Codec(), "video-hd", "edge-hd")
	bridge.hdVideo = hd
	if _, err = bridge.connectRemote(ctx, video, audio); err != nil {
		t.Fatal(err)
	}
	close(bridge.remoteReady)
	if _, err = bridge.connectLocal(ctx, video, audio, "front"); err != nil {
		t.Fatal(err)
	}
	// go2rtc xếp Opus trước G.711 khi offer có Opus → leg này không được offer Opus.
	if sdp := bridge.local.LocalDescription().SDP; !strings.Contains(sdp, "m=audio 9 UDP/TLS/RTP/SAVPF 8 0\r\n") ||
		strings.Contains(sdp, "opus/48000") {
		t.Fatalf("local offer phải chỉ G.711 (8 0), được:\n%s", sdp)
	}

	media := <-tracks
	for seq := 0; audioPackets.Load() == 0 && seq < 100; seq++ {
		_ = media.audio.WriteRTP(&rtp.Packet{Header: rtp.Header{
			Version: 2, PayloadType: 8, SequenceNumber: uint16(seq),
			Timestamp: uint32(seq)*160 + 1000, SSRC: 4,
		}, Payload: make([]byte, 160)})
		time.Sleep(20 * time.Millisecond)
	}
	if got := audioPackets.Load(); got == 0 {
		t.Fatal("client nhận 0 gói audio: edge không chuyển audio tới client")
	}
	if got := audioPayloadType.Load(); got != 8 {
		t.Fatalf("audio payload type %d, muốn 8 (PCMA)", got)
	}
}
