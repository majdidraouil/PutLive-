let localStream = null;
let peerConnection = null;
let broadcasterID = null;
let streamStartTime = null;
let statsInterval = null;

const config = {
    iceServers: [
        { urls: 'stun:stun.l.google.com:19302' },
        { urls: 'stun:stun1.l.google.com:19302' }
    ]
};

// Initialize on page load
document.addEventListener('DOMContentLoaded', async function() {
    await loadMediaDevices();
    setupEventListeners();
});

// Load available cameras and microphones
async function loadMediaDevices() {
    try {
        const devices = await navigator.mediaDevices.enumerateDevices();
        
        const videoDevices = devices.filter(d => d.kind === 'videoinput');
        const audioDevices = devices.filter(d => d.kind === 'audioinput');
        
        const videoSelect = document.getElementById('videoSource');
        const audioSelect = document.getElementById('audioSource');
        
        videoSelect.innerHTML = '';
        audioSelect.innerHTML = '';
        
        videoDevices.forEach(device => {
            const option = document.createElement('option');
            option.value = device.deviceId;
            option.text = device.label || `Camera ${videoDevices.indexOf(device) + 1}`;
            videoSelect.appendChild(option);
        });
        
        audioDevices.forEach(device => {
            const option = document.createElement('option');
            option.value = device.deviceId;
            option.text = device.label || `Microphone ${audioDevices.indexOf(device) + 1}`;
            audioSelect.appendChild(option);
        });
        
        // Load preview
        await loadPreview();
    } catch (error) {
        console.error('Error loading media devices:', error);
        alert('Cannot access camera/microphone. Please grant permissions.');
    }
}

// Load camera preview
async function loadPreview() {
    const videoSource = document.getElementById('videoSource').value;
    const audioSource = document.getElementById('audioSource').value;
    
    const constraints = {
        video: {
            deviceId: videoSource ? { exact: videoSource } : undefined,
            width: { ideal: 1280 },
            height: { ideal: 720 },
            frameRate: { ideal: 30 }
        },
        audio: {
            deviceId: audioSource ? { exact: audioSource } : undefined,
            echoCancellation: true,
            noiseSuppression: true
        }
    };
    
    try {
        if (localStream) {
            localStream.getTracks().forEach(track => track.stop());
        }
        
        localStream = await navigator.mediaDevices.getUserMedia(constraints);
        document.getElementById('localVideo').srcObject = localStream;
        
        // Update resolution info
        const videoTrack = localStream.getVideoTracks()[0];
        const settings = videoTrack.getSettings();
        document.getElementById('resolutionInfo').textContent = 
            `${settings.width}x${settings.height}`;
    } catch (error) {
        console.error('Error accessing media devices:', error);
        alert('Cannot access camera/microphone');
    }
}

// Setup event listeners
function setupEventListeners() {
    document.getElementById('startBroadcast').addEventListener('click', startBroadcast);
    document.getElementById('stopBroadcast').addEventListener('click', stopBroadcast);
    document.getElementById('videoSource').addEventListener('change', loadPreview);
    document.getElementById('audioSource').addEventListener('change', loadPreview);
    
    document.getElementById('shareScreen').addEventListener('change', async function() {
        if (this.checked) {
            await shareScreen();
        } else {
            await loadPreview();
        }
    });
}

// Share screen instead of camera
async function shareScreen() {
    try {
        const screenStream = await navigator.mediaDevices.getDisplayMedia({
            video: { width: 1920, height: 1080 },
            audio: false
        });
        
        // Keep audio from microphone
        const audioSource = document.getElementById('audioSource').value;
        const audioStream = await navigator.mediaDevices.getUserMedia({
            audio: {
                deviceId: audioSource ? { exact: audioSource } : undefined
            }
        });
        
        // Combine screen video + microphone audio
        localStream = new MediaStream([
            ...screenStream.getVideoTracks(),
            ...audioStream.getAudioTracks()
        ]);
        
        document.getElementById('localVideo').srcObject = localStream;
        
        // Stop screen sharing when user stops it
        screenStream.getVideoTracks()[0].addEventListener('ended', () => {
            document.getElementById('shareScreen').checked = false;
            loadPreview();
        });
    } catch (error) {
        console.error('Error sharing screen:', error);
        document.getElementById('shareScreen').checked = false;
    }
}

// Start broadcast
async function startBroadcast() {
    if (!localStream) {
        alert('Please allow camera/microphone access');
        return;
    }
    
    try {
        document.getElementById('startBroadcast').disabled = true;
        updateStatus('Connecting...', 'connecting');
        
        // Create peer connection
        peerConnection = new RTCPeerConnection(config);
        
        // Add local stream tracks to peer connection
        localStream.getTracks().forEach(track => {
            peerConnection.addTrack(track, localStream);
        });
        
        // Handle ICE candidates
        peerConnection.onicecandidate = event => {
            if (event.candidate) {
                console.log('ICE candidate:', event.candidate);
            }
        };
        
        // Handle connection state changes
        peerConnection.onconnectionstatechange = () => {
            console.log('Connection state:', peerConnection.connectionState);
            document.getElementById('connectionState').textContent = 
                peerConnection.connectionState;
            
            if (peerConnection.connectionState === 'connected') {
                updateStatus('Live', 'live');
            } else if (peerConnection.connectionState === 'failed') {
                updateStatus('Connection Failed', 'offline');
                stopBroadcast();
            }
        };
        
        // Create offer
        const offer = await peerConnection.createOffer({
            offerToReceiveAudio: false,
            offerToReceiveVideo: false
        });
        
        await peerConnection.setLocalDescription(offer);
        
        // Send offer to server
        const token = localStorage.getItem('authToken');
        const response = await fetch('/api/webrtc/offer', {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
                'Authorization': `Bearer ${token}`
            },
            body: JSON.stringify({
                sdp: offer.sdp,
                type: offer.type,
                stream_key: 'stream' // Use dynamic key if needed
            })
        });
        
        if (!response.ok) {
            throw new Error('Failed to start broadcast');
        }
        
        const answer = await response.json();
        
        // Set remote description (answer from server)
        await peerConnection.setRemoteDescription({
            type: 'answer',
            sdp: answer.sdp
        });
        
        // Update UI
        streamStartTime = Date.now();
        document.getElementById('startBroadcast').style.display = 'none';
        document.getElementById('stopBroadcast').style.display = 'block';
        
        // Start stats monitoring
        startStatsMonitoring();
        
        console.log('Broadcast started successfully');
    } catch (error) {
        console.error('Error starting broadcast:', error);
        alert('Failed to start broadcast: ' + error.message);
        updateStatus('Offline', 'offline');
        document.getElementById('startBroadcast').disabled = false;
    }
}

// Stop broadcast
async function stopBroadcast() {
    try {
        // Stop stats monitoring
        if (statsInterval) {
            clearInterval(statsInterval);
        }
        
        // Close peer connection
        if (peerConnection) {
            peerConnection.close();
            peerConnection = null;
        }
        
        // Notify server
        const token = localStorage.getItem('authToken');
        await fetch('/api/webrtc/stop', {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
                'Authorization': `Bearer ${token}`
            },
            body: JSON.stringify({
                broadcaster_id: broadcasterID
            })
        });
        
        // Update UI
        updateStatus('Offline', 'offline');
        document.getElementById('startBroadcast').style.display = 'block';
        document.getElementById('stopBroadcast').style.display = 'none';
        document.getElementById('startBroadcast').disabled = false;
        document.getElementById('streamDuration').textContent = '00:00:00';
        
        console.log('Broadcast stopped');
    } catch (error) {
        console.error('Error stopping broadcast:', error);
    }
}

// Update status display
function updateStatus(text, className) {
    const statusEl = document.getElementById('broadcastStatus');
    statusEl.textContent = text;
    statusEl.className = `status ${className}`;
}

// Start monitoring stats
function startStatsMonitoring() {
    statsInterval = setInterval(async () => {
        // Update duration
        if (streamStartTime) {
            const duration = Math.floor((Date.now() - streamStartTime) / 1000);
            const hours = Math.floor(duration / 3600);
            const minutes = Math.floor((duration % 3600) / 60);
            const seconds = duration % 60;
            
            document.getElementById('streamDuration').textContent = 
                `${String(hours).padStart(2, '0')}:${String(minutes).padStart(2, '0')}:${String(seconds).padStart(2, '0')}`;
        }
        
        // Get WebRTC stats
        if (peerConnection) {
            const stats = await peerConnection.getStats();
            let bitrate = 0;
            let fps = 0;
            
            stats.forEach(report => {
                if (report.type === 'outbound-rtp' && report.kind === 'video') {
                    if (report.bytesSent) {
                        bitrate = Math.round(report.bytesSent * 8 / 1000); // Kbps
                    }
                    if (report.framesPerSecond) {
                        fps = report.framesPerSecond;
                    }
                }
            });
            
            document.getElementById('bitrate').textContent = `${bitrate} Kbps`;
            document.getElementById('fpsCount').textContent = fps;
        }
        
        // Get viewer count from API
        try {
            const response = await fetch('/api/stream/status');
            const data = await response.json();
            document.getElementById('viewerCount').textContent = data.viewers || 0;
        } catch (error) {
            console.error('Error fetching viewer count:', error);
        }
    }, 1000);
}

// Cleanup on page unload
window.addEventListener('beforeunload', () => {
    if (peerConnection) {
        stopBroadcast();
    }
    if (localStream) {
        localStream.getTracks().forEach(track => track.stop());
    }
});
