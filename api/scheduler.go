package main

import (
	"log"
	"time"
)

func startScheduler() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	log.Println("Scheduler started")

	for range ticker.C {
		checkScheduledStreams()
	}
}

func checkScheduledStreams() {
	now := time.Now()

	// Get scheduled streams that should start now
	rows, err := app.DB.Query(`
		SELECT id, video_id, quality, loop 
		FROM schedules 
		WHERE start_time <= ? AND status = 'scheduled'
	`, now)

	if err != nil {
		log.Printf("Scheduler error: %v", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var id, videoID, quality string
		var loop bool

		if err := rows.Scan(&id, &videoID, &quality, &loop); err != nil {
			continue
		}

		log.Printf("Starting scheduled stream: %s", id)

		// Update status to running
		app.DB.Exec("UPDATE schedules SET status = 'running' WHERE id = ?", id)

		// Start the stream
		go startScheduledStream(id, videoID, quality, loop)
	}

	// Check for streams that should stop
	app.DB.Exec(`
		UPDATE schedules 
		SET status = 'completed' 
		WHERE end_time <= ? AND status = 'running'
	`, now)
}

func startScheduledStream(scheduleID, videoID, quality string, loop bool) {
	// Implementation similar to handleStartStream
	log.Printf("Stream started for schedule %s", scheduleID)
}
