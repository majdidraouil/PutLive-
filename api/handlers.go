package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

type StreamStatus struct {
	Active       bool   `json:"active"`
	StreamName   string `json:"stream_name"`
	Quality      string `json:"quality"`
	BitrateKbps  int    `json:"bitrate_kbps"`
	FPS          int    `json:"fps"`
	Resolution   string `json:"resolution"`
	Viewers      int    `json:"viewers"`
	UptimeSeconds int   `json:"uptime_seconds"`
	BytesSent    int64  `json:"bytes_sent"`
}

type Video struct {
	ID              string    `json:"id"`
	Title           string    `json:"title"`
	Filename        string    `json:"filename"`
	Description     string    `json:"description"`
	DurationSeconds int       `json:"duration_seconds"`
	SizeBytes       int64     `json:"size_bytes"`
	Status          string    `json:"status"`
	CreatedAt       time.Time `json:"created_at"`
}

type Schedule struct {
	ID        string    `json:"id"`
	VideoID   string    `json:"video_id"`
	StartTime time.Time `json:"start_time"`
	EndTime   time.Time `json:"end_time"`
	Quality   string    `json:"quality"`
	Loop      bool      `json:"loop"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// SRS webhook handlers
func handleOnPublish(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var payload map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	streamName := payload["stream"].(string)
	
	// Log analytics
	_, err := app.DB.Exec(
		"INSERT INTO analytics (stream_name, event_type, viewer_count) VALUES (?, ?, ?)",
		streamName, "publish_start", 0,
	)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Database error")
		return
	}

	// Allow publish (return code 0)
	respondJSON(w, http.StatusOK, map[string]int{"code": 0})
}

func handleOnUnpublish(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var payload map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	streamName := payload["stream"].(string)
	
	// Log analytics
	_, err := app.DB.Exec(
		"INSERT INTO analytics (stream_name, event_type, viewer_count) VALUES (?, ?, ?)",
		streamName, "publish_stop", 0,
	)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Database error")
		return
	}

	respondJSON(w, http.StatusOK, map[string]int{"code": 0})
}

func handleOnPlay(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var payload map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	// Allow play (return code 0)
	respondJSON(w, http.StatusOK, map[string]int{"code": 0})
}

// Stream handlers
func handleStreamStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Query SRS API for stream status
	resp, err := http.Get("http://127.0.0.1:1985/api/v1/streams/")
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to query SRS")
		return
	}
	defer resp.Body.Close()

	var srsResponse map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&srsResponse); err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to parse SRS response")
		return
	}

	streams, ok := srsResponse["streams"].([]interface{})
	if !ok || len(streams) == 0 {
		respondJSON(w, http.StatusOK, StreamStatus{Active: false})
		return
	}

	stream := streams[0].(map[string]interface{})
	
	status := StreamStatus{
		Active:     true,
		StreamName: stream["name"].(string),
		Quality:    "480p",
		Viewers:    int(stream["clients"].(float64)),
	}

	respondJSON(w, http.StatusOK, status)
}

func handleStartStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var req struct {
		VideoID string `json:"video_id"`
		Quality string `json:"quality"`
		Loop    bool   `json:"loop"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request")
		return
	}

	// Get video from database
	var video Video
	err := app.DB.QueryRow(
		"SELECT id, filename FROM videos WHERE id = ?",
		req.VideoID,
	).Scan(&video.ID, &video.Filename)

	if err == sql.ErrNoRows {
		respondError(w, http.StatusNotFound, "Video not found")
		return
	}
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Database error")
		return
	}

	// Start FFmpeg stream
	videoPath := filepath.Join("/var/lib/putlive/videos/processed", video.Filename)
	
	loopFlag := ""
	if req.Loop {
		loopFlag = "-stream_loop -1"
	}

	cmd := fmt.Sprintf(
		"ffmpeg -re %s -i %s -c copy -f flv rtmp://127.0.0.1:1935/live/stream",
		loopFlag, videoPath,
	)

	go func() {
		exec.Command("bash", "-c", cmd).Run()
	}()

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"message":  "Stream started",
		"rtmp_url": "rtmp://127.0.0.1:1935/live/stream",
		"hls_url":  "http://127.0.0.1:8080/live/stream.m3u8",
	})
}

func handleStopStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Kill all FFmpeg processes
	exec.Command("pkill", "-9", "ffmpeg").Run()

	respondJSON(w, http.StatusOK, map[string]string{
		"message": "Stream stopped",
	})
}

// Video handlers
func handleVideos(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	rows, err := app.DB.Query(`
		SELECT id, title, filename, description, duration_seconds, size_bytes, status, created_at 
		FROM videos 
		ORDER BY created_at DESC
	`)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Database error")
		return
	}
	defer rows.Close()

	var videos []Video
	for rows.Next() {
		var v Video
		err := rows.Scan(&v.ID, &v.Title, &v.Filename, &v.Description, 
			&v.DurationSeconds, &v.SizeBytes, &v.Status, &v.CreatedAt)
		if err != nil {
			continue
		}
		videos = append(videos, v)
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"videos": videos,
		"total":  len(videos),
	})
}

func handleVideoUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Parse multipart form
	if err := r.ParseMultipartForm(500 << 20); err != nil { // 500 MB max
		respondError(w, http.StatusBadRequest, "File too large")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		respondError(w, http.StatusBadRequest, "No file uploaded")
		return
	}
	defer file.Close()

	title := r.FormValue("title")
	description := r.FormValue("description")

	// Generate video ID
	videoID := generateUUID()
	filename := fmt.Sprintf("%s%s", videoID, filepath.Ext(header.Filename))

	// Save file
	uploadPath := filepath.Join("/var/lib/putlive/videos/raw", filename)
	dst, err := os.Create(uploadPath)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to save file")
		return
	}
	defer dst.Close()

	size, err := io.Copy(dst, file)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to save file")
		return
	}

	// Insert into database
	_, err = app.DB.Exec(`
		INSERT INTO videos (id, title, filename, description, size_bytes, status) 
		VALUES (?, ?, ?, ?, ?, 'processing')
	`, videoID, title, filename, description, size)

	if err != nil {
		respondError(w, http.StatusInternalServerError, "Database error")
		return
	}

	// Start transcoding in background
	go transcodeVideo(videoID, uploadPath)

	respondJSON(w, http.StatusCreated, Video{
		ID:        videoID,
		Title:     title,
		Filename:  filename,
		SizeBytes: size,
		Status:    "processing",
	})
}

func transcodeVideo(videoID, inputPath string) {
	outputPath := filepath.Join("/var/lib/putlive/videos/processed", videoID+".mp4")

	cmd := exec.Command("ffmpeg",
		"-i", inputPath,
		"-c:v", "libx264",
		"-preset", "medium",
		"-crf", "23",
		"-c:a", "aac",
		"-b:a", "128k",
		"-movflags", "+faststart",
		outputPath,
	)

	if err := cmd.Run(); err != nil {
		app.DB.Exec("UPDATE videos SET status = 'failed' WHERE id = ?", videoID)
		return
	}

	// Get duration
	durationCmd := exec.Command("ffprobe",
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		outputPath,
	)
	
	output, _ := durationCmd.Output()
	var duration int
	fmt.Sscanf(string(output), "%d", &duration)

	app.DB.Exec(
		"UPDATE videos SET status = 'ready', duration_seconds = ?, filename = ? WHERE id = ?",
		duration, filepath.Base(outputPath), videoID,
	)
}

// Schedule handlers
func handleSchedule(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		getSchedules(w, r)
	case http.MethodPost:
		createSchedule(w, r)
	case http.MethodDelete:
		deleteSchedule(w, r)
	default:
		respondError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func getSchedules(w http.ResponseWriter, r *http.Request) {
	rows, err := app.DB.Query(`
		SELECT id, video_id, start_time, end_time, quality, loop, status, created_at 
		FROM schedules 
		ORDER BY start_time ASC
	`)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Database error")
		return
	}
	defer rows.Close()

	var schedules []Schedule
	for rows.Next() {
		var s Schedule
		err := rows.Scan(&s.ID, &s.VideoID, &s.StartTime, &s.EndTime, 
			&s.Quality, &s.Loop, &s.Status, &s.CreatedAt)
		if err != nil {
			continue
		}
		schedules = append(schedules, s)
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"schedules": schedules,
	})
}

func createSchedule(w http.ResponseWriter, r *http.Request) {
	var req Schedule
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request")
		return
	}

	req.ID = generateUUID()
	req.Status = "scheduled"
	req.CreatedAt = time.Now()

	_, err := app.DB.Exec(`
		INSERT INTO schedules (id, video_id, start_time, end_time, quality, loop, status) 
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, req.ID, req.VideoID, req.StartTime, req.EndTime, req.Quality, req.Loop, req.Status)

	if err != nil {
		respondError(w, http.StatusInternalServerError, "Database error")
		return
	}

	respondJSON(w, http.StatusCreated, req)
}

func deleteSchedule(w http.ResponseWriter, r *http.Request) {
	scheduleID := r.URL.Query().Get("id")
	if scheduleID == "" {
		respondError(w, http.StatusBadRequest, "Schedule ID required")
		return
	}

	_, err := app.DB.Exec("DELETE FROM schedules WHERE id = ?", scheduleID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Database error")
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{"message": "Schedule deleted"})
}

// Analytics handlers
func handleStreamAnalytics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	rows, err := app.DB.Query(`
		SELECT stream_name, event_type, viewer_count, timestamp 
		FROM analytics 
		ORDER BY timestamp DESC 
		LIMIT 100
	`)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Database error")
		return
	}
	defer rows.Close()

	var analytics []map[string]interface{}
	for rows.Next() {
		var streamName, eventType string
		var viewerCount int
		var timestamp time.Time
		
		if err := rows.Scan(&streamName, &eventType, &viewerCount, &timestamp); err != nil {
			continue
		}

		analytics = append(analytics, map[string]interface{}{
			"stream_name":  streamName,
			"event_type":   eventType,
			"viewer_count": viewerCount,
			"timestamp":    timestamp,
		})
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"analytics": analytics,
	})
}
