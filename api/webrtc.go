package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/exec"
	"sync"

	"github.com/pion/webrtc/v3"
	"github.com/pion/webrtc/v3/pkg/media"
)

var (
	activeBroadcasters = make(map[string]*Broadcaster)
	broadcasterMutex   sync.RWMutex
)

type Broadcaster struct {
	ID            string
	PeerConnection *webrtc.PeerConnection
	VideoTrack    *webrtc.TrackLocalStaticSample
	AudioTrack    *webrtc.TrackLocalStaticSample
	FFmpegCmd     *exec.Cmd
	StreamKey     string
}

type WebRTCOffer struct {
	SDP       string `json:"sdp"`
	Type      string `json:"type"`
	StreamKey string `json:"stream_key"`
}

type WebRTCAnswer struct {
	SDP  string `json:"sdp"`
	Type string `json:"type"`
}

// Handle WebRTC offer from browser
func handleWebRTCOffer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var offer WebRTCOffer
	if err := json.NewDecoder(r.Body).Decode(&offer); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid offer")
		return
	}

	// Verify stream key (optional - can use JWT from context)
	user := r.Context().Value(userContextKey).(*User)
	if user == nil {
		respondError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	// Create WebRTC peer connection
	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{"stun:stun.l.google.com:19302"},
			},
		},
	}

	peerConnection, err := webrtc.NewPeerConnection(config)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to create peer connection")
		return
	}

	// Create broadcaster instance
	broadcasterID := generateUUID()
	broadcaster := &Broadcaster{
		ID:             broadcasterID,
		PeerConnection: peerConnection,
		StreamKey:      offer.StreamKey,
	}

	// Handle incoming tracks (video and audio from browser)
	peerConnection.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		log.Printf("Received %s track from browser", track.Kind())

		// Start FFmpeg process to convert WebRTC to RTMP
		if track.Kind() == webrtc.RTPCodecTypeVideo {
			go handleVideoTrack(broadcaster, track)
		} else if track.Kind() == webrtc.RTPCodecTypeAudio {
			go handleAudioTrack(broadcaster, track)
		}
	})

	// Handle ICE connection state changes
	peerConnection.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		log.Printf("ICE Connection State: %s", state.String())

		if state == webrtc.ICEConnectionStateFailed || state == webrtc.ICEConnectionStateClosed {
			stopBroadcaster(broadcasterID)
		}
	})

	// Set remote description (offer from browser)
	if err := peerConnection.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  offer.SDP,
	}); err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to set remote description")
		return
	}

	// Create answer
	answer, err := peerConnection.CreateAnswer(nil)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to create answer")
		return
	}

	// Set local description
	if err := peerConnection.SetLocalDescription(answer); err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to set local description")
		return
	}

	// Store broadcaster
	broadcasterMutex.Lock()
	activeBroadcasters[broadcasterID] = broadcaster
	broadcasterMutex.Unlock()

	// Return answer to browser
	respondJSON(w, http.StatusOK, WebRTCAnswer{
		SDP:  answer.SDP,
		Type: answer.Type.String(),
	})

	log.Printf("WebRTC connection established: %s", broadcasterID)
}

// Handle video track and pipe to FFmpeg
func handleVideoTrack(broadcaster *Broadcaster, track *webrtc.TrackRemote) {
	// Start FFmpeg process to convert RTP to RTMP
	rtmpURL := fmt.Sprintf("rtmp://127.0.0.1:1935/live/%s", broadcaster.StreamKey)

	cmd := exec.Command("ffmpeg",
		"-f", "rtp",
		"-i", "pipe:0", // Video input from pipe
		"-c:v", "libx264",
		"-preset", "ultrafast",
		"-tune", "zerolatency",
		"-b:v", "1200k",
		"-maxrate", "1200k",
		"-bufsize", "2400k",
		"-g", "60",
		"-f", "flv",
		rtmpURL,
	)

	// Create pipes for video input
	videoPipe, err := cmd.StdinPipe()
	if err != nil {
		log.Printf("Failed to create video pipe: %v", err)
		return
	}

	// Start FFmpeg
	if err := cmd.Start(); err != nil {
		log.Printf("Failed to start FFmpeg: %v", err)
		return
	}

	broadcaster.FFmpegCmd = cmd

	log.Printf("FFmpeg started for broadcaster %s", broadcaster.ID)

	// Read RTP packets and write to FFmpeg
	buf := make([]byte, 1500)
	for {
		n, _, err := track.Read(buf)
		if err != nil {
			if err == io.EOF {
				break
			}
			log.Printf("Error reading video track: %v", err)
			break
		}

		if _, err := videoPipe.Write(buf[:n]); err != nil {
			log.Printf("Error writing to FFmpeg: %v", err)
			break
		}
	}

	videoPipe.Close()
	cmd.Wait()
	log.Printf("Video track ended for broadcaster %s", broadcaster.ID)
}

// Handle audio track
func handleAudioTrack(broadcaster *Broadcaster, track *webrtc.TrackRemote) {
	// Similar to video, but for audio
	// For simplicity, we'll handle audio in the same FFmpeg process
	// In production, you'd merge audio and video streams
	log.Printf("Audio track received for broadcaster %s", broadcaster.ID)
}

// Stop broadcaster and cleanup
func stopBroadcaster(broadcasterID string) {
	broadcasterMutex.Lock()
	defer broadcasterMutex.Unlock()

	broadcaster, exists := activeBroadcasters[broadcasterID]
	if !exists {
		return
	}

	// Stop FFmpeg
	if broadcaster.FFmpegCmd != nil && broadcaster.FFmpegCmd.Process != nil {
		broadcaster.FFmpegCmd.Process.Kill()
	}

	// Close peer connection
	if broadcaster.PeerConnection != nil {
		broadcaster.PeerConnection.Close()
	}

	delete(activeBroadcasters, broadcasterID)
	log.Printf("Broadcaster stopped: %s", broadcasterID)
}

// Handle stop broadcast request
func handleStopBroadcast(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var req struct {
		BroadcasterID string `json:"broadcaster_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request")
		return
	}

	stopBroadcaster(req.BroadcasterID)

	respondJSON(w, http.StatusOK, map[string]string{
		"message": "Broadcast stopped",
	})
}
