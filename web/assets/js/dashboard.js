// Dashboard initialization
document.addEventListener('DOMContentLoaded', function() {
    // Check authentication
    const token = localStorage.getItem('authToken');
    if (!token) {
        window.location.href = '/login.html';
        return;
    }
    
    // Set user greeting
    const user = JSON.parse(localStorage.getItem('user') || '{}');
    document.getElementById('userGreeting').textContent = `Welcome, ${user.username || 'Admin'}`;
    
    // Initialize tabs
    initTabs();
    
    // Initialize preview player
    initPreviewPlayer();
    
    // Load initial data
    loadDashboardData();
    loadVideos();
    loadSchedules();
    loadAnalytics();
    loadSystemHealth();
    
    // Auto-refresh
    setInterval(loadDashboardData, 5000);
    setInterval(loadSystemHealth, 10000);
    
    // Initialize charts
    initCharts();
    
    // Initialize calendar
    initCalendar();
    
    // Upload form handler
    document.getElementById('uploadForm').addEventListener('submit', handleVideoUpload);
});

// Tab navigation
function initTabs() {
    const navItems = document.querySelectorAll('.nav-item');
    
    navItems.forEach(item => {
        item.addEventListener('click', function(e) {
            e.preventDefault();
            
            // Remove active class from all
            navItems.forEach(nav => nav.classList.remove('active'));
            document.querySelectorAll('.tab-content').forEach(tab => tab.classList.remove('active'));
            
            // Add active class to clicked
            this.classList.add('active');
            const tabId = this.getAttribute('data-tab');
            document.getElementById(`tab-${tabId}`).classList.add('active');
        });
    });
}

// Preview player
function initPreviewPlayer() {
    const video = document.getElementById('previewPlayer');
    const previewUrl = `http://${window.location.hostname}:8080/live/stream_144p.m3u8`;
    
    if (Hls.isSupported()) {
        const hls = new Hls({
            enableWorker: true,
            lowLatencyMode: true
        });
        
        hls.loadSource(previewUrl);
        hls.attachMedia(video);
        
        hls.on(Hls.Events.ERROR, function(event, data) {
            if (data.fatal) {
                document.getElementById('previewOverlay').style.display = 'flex';
            }
        });
    }
}

// Load dashboard data
async function loadDashboardData() {
    try {
        const status = await API.get('/stream/status');
        
        if (status.active) {
            document.getElementById('currentViewers').textContent = status.viewers;
            document.getElementById('streamUptime').textContent = formatDuration(status.uptime_seconds);
            document.getElementById('previewBitrate').textContent = `${status.bitrate_kbps} Kbps`;
            document.getElementById('previewFps').textContent = `${status.fps} fps`;
            document.getElementById('previewOverlay').style.display = 'none';
            document.getElementById('streamStatusBadge').textContent = 'Online';
            document.getElementById('streamStatusBadge').className = 'status-badge online';
        } else {
            document.getElementById('previewOverlay').style.display = 'flex';
            document.getElementById('streamStatusBadge').textContent = 'Offline';
            document.getElementById('streamStatusBadge').className = 'status-badge offline';
        }
    } catch (error) {
        console.error('Error loading dashboard data:', error);
    }
}

// Load videos
async function loadVideos() {
    try {
        const data = await API.get('/videos');
        const videoGrid = document.getElementById('videoGrid');
        const videoSelect = document.getElementById('videoSelect');
        
        videoGrid.innerHTML = '';
        videoSelect.innerHTML = '<option value="">Select video...</option>';
        
        data.videos.forEach(video => {
            // Add to grid
            const videoCard = document.createElement('div');
            videoCard.className = 'video-card';
            videoCard.innerHTML = `
                <h4>${video.title}</h4>
                <p>${formatDuration(video.duration_seconds)} | ${formatBytes(video.size_bytes)}</p>
                <p>Status: ${video.status}</p>
                <button onclick="deleteVideo('${video.id}')">Delete</button>
            `;
            videoGrid.appendChild(videoCard);
            
            // Add to select
            if (video.status === 'ready') {
                const option = document.createElement('option');
                option.value = video.id;
                option.textContent = video.title;
                videoSelect.appendChild(option);
            }
        });
    } catch (error) {
        console.error('Error loading videos:', error);
    }
}

// Handle video upload
async function handleVideoUpload(e) {
    e.preventDefault();
    
    const formData = new FormData();
    formData.append('file', document.getElementById('videoFile').files[0]);
    formData.append('title', document.getElementById('videoTitle').value);
    formData.append('description', document.getElementById('videoDescription').value);
    
    const progressBar = document.getElementById('progressBar');
    const progressText = document.getElementById('progressText');
    document.getElementById('uploadProgress').style.display = 'block';
    
    try {
        const xhr = new XMLHttpRequest();
        
        xhr.upload.addEventListener('progress', (e) => {
            const percent = (e.loaded / e.total) * 100;
            progressBar.value = percent;
            progressText.textContent = `${Math.round(percent)}%`;
        });
        
        xhr.addEventListener('load', () => {
            if (xhr.status === 201) {
                alert('Video uploaded successfully!');
                document.getElementById('uploadForm').reset();
                document.getElementById('uploadProgress').style.display = 'none';
                loadVideos();
            }
        });
        
        xhr.open('POST', '/api/videos/upload');
        xhr.setRequestHeader('Authorization', `Bearer ${localStorage.getItem('authToken')}`);
        xhr.send(formData);
    } catch (error) {
        console.error('Upload error:', error);
        alert('Upload failed');
    }
}

// Delete video
async function deleteVideo(videoId) {
    if (!confirm('Are you sure you want to delete this video?')) return;
    
    try {
        await API.delete(`/videos/${videoId}`);
        loadVideos();
    } catch (error) {
        console.error('Error deleting video:', error);
    }
}

// Start stream
async function startStream() {
    const videoId = document.getElementById('videoSelect').value;
    const quality = document.getElementById('streamQuality').value;
    const loop = document.getElementById('loopStream').checked;
    
    if (!videoId) {
        alert('Please select a video');
        return;
    }
    
    try {
        await API.post('/stream/start', { video_id: videoId, quality, loop });
        alert('Stream started!');
        loadDashboardData();
    } catch (error) {
        console.error('Error starting stream:', error);
        alert('Failed to start stream');
    }
}

// Stop stream
async function stopStream() {
    try {
        await API.post('/stream/stop', {});
        alert('Stream stopped');
        loadDashboardData();
    } catch (error) {
        console.error('Error stopping stream:', error);
    }
}

// Load schedules
async function loadSchedules() {
    try {
        const data = await API.get('/schedule');
        // Calendar will be populated by FullCalendar
    } catch (error) {
        console.error('Error loading schedules:', error);
    }
}

// Initialize calendar
function initCalendar() {
    const calendarEl = document.getElementById('calendar');
    
    const calendar = new FullCalendar.Calendar(calendarEl, {
        initialView: 'dayGridMonth',
        headerToolbar: {
            left: 'prev,next today',
            center: 'title',
            right: 'dayGridMonth,timeGridWeek,timeGridDay'
        },
        editable: true,
        droppable: true,
        events: async function(info, successCallback, failureCallback) {
            try {
                const data = await API.get('/schedule');
                const events = data.schedules.map(schedule => ({
                    id: schedule.id,
                    title: schedule.video.title,
                    start: schedule.start_time,
                    end: schedule.end_time
                }));
                successCallback(events);
            } catch (error) {
                failureCallback(error);
            }
        }
    });
    
    calendar.render();
}

// Load analytics
async function loadAnalytics() {
    try {
        const data = await API.get('/analytics/stream');
        const tbody = document.getElementById('analyticsBody');
        
        tbody.innerHTML = '';
        data.analytics.forEach(event => {
            const row = document.createElement('tr');
            row.innerHTML = `
                <td>${formatDate(event.timestamp)}</td>
                <td>${event.event_type}</td>
                <td>${event.viewer_count}</td>
            `;
            tbody.appendChild(row);
        });
    } catch (error) {
        console.error('Error loading analytics:', error);
    }
}

// Load system health
async function loadSystemHealth() {
    try {
        const data = await API.get('/health/detailed');
        
        // Update service status
        document.getElementById('srsStatus').textContent = data.components.srs.status;
        document.getElementById('srsStatus').className = `status-badge ${data.components.srs.status === 'ok' ? 'online' : 'offline'}`;
        
        document.getElementById('apiStatus').textContent = 'Online';
        document.getElementById('apiStatus').className = 'status-badge online';
        
        document.getElementById('dbStatus').textContent = data.components.database.status;
        document.getElementById('dbStatus').className = `status-badge ${data.components.database.status === 'ok' ? 'online' : 'offline'}`;
        
        // Update gauges
        document.getElementById('diskGauge').textContent = `${Math.round(data.components.disk.usage_percent)}%`;
        document.getElementById('memGauge').textContent = `${Math.round(data.components.memory.alloc_mb)} MB`;
    } catch (error) {
        console.error('Error loading system health:', error);
    }
}

// Initialize charts
function initCharts() {
    // Viewer chart
    const viewerCtx = document.getElementById('viewerChart');
    if (viewerCtx) {
        new Chart(viewerCtx, {
            type: 'line',
            data: {
                labels: [],
                datasets: [{
                    label: 'Viewers',
                    data: [],
                    borderColor: 'rgb(37, 99, 235)',
                    tension: 0.1
                }]
            },
            options: {
                responsive: true,
                scales: {
                    y: {
                        beginAtZero: true
                    }
                }
            }
        });
    }
}
