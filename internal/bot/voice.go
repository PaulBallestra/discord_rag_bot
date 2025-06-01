// internal/bot/voice.go - Fixed for Mac M1 and discordgo compatibility
package bot

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"layeh.com/gopus"
)

type VoiceConnection struct {
	Connection   *discordgo.VoiceConnection
	GuildID      string
	ChannelID    string
	UserId       string
	AudioBuffer  *bytes.Buffer
	LastActivity time.Time
	IsRecording  bool
	decoder      *gopus.Decoder
	encoder      *gopus.Encoder
	mu           sync.RWMutex
	ctx          context.Context
	cancel       context.CancelFunc
}

type VoiceManager struct {
	connections map[string]*VoiceConnection
	mu          sync.RWMutex
	handler     *BotHandler
}

func NewVoiceManager(handler *BotHandler) *VoiceManager {
	return &VoiceManager{
		connections: make(map[string]*VoiceConnection),
		handler:     handler,
	}
}

// Replace the JoinVoiceChannel function in voice.go
func (vm *VoiceManager) JoinVoiceChannel(s *discordgo.Session, guildID, channelID, userID string) error {
	vm.mu.Lock()
	defer vm.mu.Unlock()

	// Leave existing connection if any
	if existingConn, exists := vm.connections[guildID]; exists {
		if existingConn.cancel != nil {
			existingConn.cancel()
		}
		if existingConn.Connection != nil {
			existingConn.Connection.Disconnect()
		}
		delete(vm.connections, guildID)
		time.Sleep(1 * time.Second) // Wait for cleanup
	}

	// Join voice channel
	voiceConn, err := s.ChannelVoiceJoin(guildID, channelID, false, false)
	if err != nil {
		return fmt.Errorf("failed to join voice channel: %v", err)
	}

	// Wait for the connection to be ready with timeout
	timeout := time.After(10 * time.Second)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			voiceConn.Disconnect()
			return fmt.Errorf("voice connection timeout")
		case <-ticker.C:
			if voiceConn.Ready {
				log.Printf("Voice connection ready for guild %s", guildID)
				goto ready
			}
		}
	}

ready:
	// Create opus decoder and encoder with error handling
	decoder, err := gopus.NewDecoder(48000, 2)
	if err != nil {
		voiceConn.Disconnect()
		return fmt.Errorf("failed to create decoder: %v", err)
	}

	encoder, err := gopus.NewEncoder(48000, 2, gopus.Audio)
	if err != nil {
		voiceConn.Disconnect()
		return fmt.Errorf("failed to create encoder: %v", err)
	}

	// Set encoder bitrate for better quality
	encoder.SetBitrate(96000)
	log.Printf("Encoder bitrate set to 96000")

	// Create context for this connection
	ctx, cancel := context.WithCancel(context.Background())

	// Create voice connection wrapper
	vc := &VoiceConnection{
		Connection:   voiceConn,
		GuildID:      guildID,
		ChannelID:    channelID,
		UserId:       userID,
		AudioBuffer:  new(bytes.Buffer),
		LastActivity: time.Now(),
		IsRecording:  false,
		decoder:      decoder,
		encoder:      encoder,
		ctx:          ctx,
		cancel:       cancel,
	}

	vm.connections[guildID] = vc

	// Start listening for voice data with context
	go vm.listenForVoice(vc)

	log.Printf("Joined voice channel %s in guild %s", channelID, guildID)
	return nil
}

func (vm *VoiceManager) LeaveVoiceChannel(guildID string) error {
	vm.mu.Lock()
	defer vm.mu.Unlock()

	vc, exists := vm.connections[guildID]
	if !exists {
		return fmt.Errorf("not connected to voice channel in guild %s", guildID)
	}

	// Cancel context and disconnect
	if vc.cancel != nil {
		vc.cancel()
	}
	if vc.Connection != nil {
		vc.Connection.Disconnect()
	}
	delete(vm.connections, guildID)

	log.Printf("Left voice channel in guild %s", guildID)
	return nil
}

func (vm *VoiceManager) HandleVoiceStateUpdate(s *discordgo.Session, vsu *discordgo.VoiceStateUpdate) {
	log.Printf("Voice state update: User %s in guild %s", vsu.UserID, vsu.GuildID)
}

func (vm *VoiceManager) SendAudio(vc *VoiceConnection, audioData []byte) error {
	if vc.Connection == nil {
		return fmt.Errorf("no voice connection")
	}

	// Create temp files with unique names
	tempID := time.Now().UnixNano()
	tempFile := fmt.Sprintf("/tmp/tts_%d.mp3", tempID)
	pcmFile := fmt.Sprintf("/tmp/pcm_%d.pcm", tempID)

	// Cleanup
	defer func() {
		os.Remove(tempFile)
		os.Remove(pcmFile)
	}()

	// Save MP3 file
	if err := os.WriteFile(tempFile, audioData, 0644); err != nil {
		return fmt.Errorf("error saving audio file: %v", err)
	}

	// Convert MP3 to PCM using FFmpeg
	if err := vm.convertToPCM(tempFile, pcmFile); err != nil {
		return fmt.Errorf("error converting to PCM: %v", err)
	}

	// Play the PCM audio
	return vm.playPCMFile(vc, pcmFile)
}

func (vm *VoiceManager) convertToPCM(inputFile, outputFile string) error {
	log.Printf("Converting %s to PCM format", inputFile)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-i", inputFile,
		"-f", "s16le", // 16-bit signed little-endian
		"-ar", "48000", // 48kHz sample rate
		"-ac", "2", // Stereo
		"-y", // Overwrite output
		outputFile)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg conversion failed: %v, stderr: %s", err, stderr.String())
	}

	log.Printf("Successfully converted to PCM")
	return nil
}

func (vm *VoiceManager) playPCMFile(vc *VoiceConnection, filename string) error {
	log.Printf("Playing PCM audio file: %s", filename)

	file, err := os.Open(filename)
	if err != nil {
		return fmt.Errorf("error opening PCM file: %v", err)
	}
	defer file.Close()

	// Signal that we're speaking
	vc.Connection.Speaking(true)
	defer vc.Connection.Speaking(false)

	// Read and encode PCM data in chunks
	buffer := make([]byte, 3840) // 960 samples * 2 channels * 2 bytes per sample

	for {
		select {
		case <-vc.ctx.Done():
			return fmt.Errorf("playback cancelled")
		default:
		}

		n, err := file.Read(buffer)
		if err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("error reading PCM data: %v", err)
		}

		if n == 0 {
			continue
		}

		// Convert bytes to int16 samples
		samples := make([]int16, n/2)
		for i := 0; i < len(samples); i++ {
			samples[i] = int16(binary.LittleEndian.Uint16(buffer[i*2 : i*2+2]))
		}

		// Encode to Opus
		opusData, err := vc.encoder.Encode(samples, 960, 960)
		if err != nil {
			log.Printf("Error encoding to Opus: %v", err)
			continue
		}

		// Send to Discord
		select {
		case vc.Connection.OpusSend <- opusData:
		case <-time.After(100 * time.Millisecond):
			log.Printf("Timeout sending Opus frame")
		case <-vc.ctx.Done():
			return fmt.Errorf("playback cancelled")
		}
	}

	log.Printf("Finished playing audio")
	return nil
}

func (vm *VoiceManager) listenForVoice(vc *VoiceConnection) {
	log.Printf("Started listening for voice in guild %s", vc.GuildID)

	for {
		select {
		case packet := <-vc.Connection.OpusRecv:
			if packet != nil && packet.Opus != nil {
				vm.processVoicePacket(vc, packet)
			}
		case <-vc.ctx.Done():
			log.Printf("Voice listening stopped for guild %s", vc.GuildID)
			return
		case <-time.After(30 * time.Second):
			// Check for inactivity
			vc.mu.RLock()
			inactive := time.Since(vc.LastActivity) > 5*time.Minute
			vc.mu.RUnlock()

			if inactive {
				log.Printf("Voice connection inactive, disconnecting from guild %s", vc.GuildID)
				vm.LeaveVoiceChannel(vc.GuildID)
				return
			}
		}
	}
}

func (vm *VoiceManager) processVoicePacket(vc *VoiceConnection, packet *discordgo.Packet) {
	// Skip empty or invalid packets
	if packet == nil || packet.Opus == nil || len(packet.Opus) == 0 {
		return
	}

	// Skip packets from the bot itself
	if packet.SSRC == 0 {
		return
	}

	// Decode opus data to PCM
	pcmData, err := vc.decoder.Decode(packet.Opus, 960, false)
	if err != nil {
		if !strings.Contains(err.Error(), "invalid packet") {
			log.Printf("Error decoding opus: %v", err)
		}
		return
	}

	if len(pcmData) == 0 {
		return
	}

	// Convert int16 samples to bytes
	pcmBytes := make([]byte, len(pcmData)*2)
	for i, sample := range pcmData {
		binary.LittleEndian.PutUint16(pcmBytes[i*2:], uint16(sample))
	}

	// Buffer the audio data
	vc.mu.Lock()
	vc.AudioBuffer.Write(pcmBytes)
	vc.LastActivity = time.Now()

	// Start recording if not already recording
	if !vc.IsRecording {
		vc.IsRecording = true
		go vm.handleVoiceRecording(vc)
	}
	vc.mu.Unlock()
}

func (vm *VoiceManager) handleVoiceRecording(vc *VoiceConnection) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	lastBufferSize := 0
	silenceCount := 0
	maxRecordingTime := 300 // 30 seconds max

	for {
		select {
		case <-ticker.C:
			vc.mu.RLock()
			currentSize := vc.AudioBuffer.Len()
			vc.mu.RUnlock()

			if currentSize == lastBufferSize {
				silenceCount++
			} else {
				silenceCount = 0
				lastBufferSize = currentSize
			}

			// 2 seconds of silence and sufficient audio data (at least 16KB)
			if silenceCount >= 20 && currentSize > 16000 {
				vm.processRecordedAudio(vc)
				return
			}

			// Maximum recording time reached
			if silenceCount >= maxRecordingTime {
				if currentSize > 16000 {
					log.Printf("Max recording time reached, processing %d bytes", currentSize)
					vm.processRecordedAudio(vc)
				} else {
					log.Printf("Max recording time reached but insufficient audio data (%d bytes), discarding", currentSize)
					vc.mu.Lock()
					vc.AudioBuffer.Reset()
					vc.IsRecording = false
					vc.mu.Unlock()
				}
				return
			}

		case <-vc.ctx.Done():
			log.Printf("Voice recording cancelled for guild %s", vc.GuildID)
			return
		}
	}
}

func (vm *VoiceManager) processRecordedAudio(vc *VoiceConnection) {
	vc.mu.Lock()
	audioData := make([]byte, vc.AudioBuffer.Len())
	copy(audioData, vc.AudioBuffer.Bytes())
	vc.AudioBuffer.Reset()
	vc.IsRecording = false
	vc.mu.Unlock()

	log.Printf("Processing recorded audio (%d bytes) from guild %s", len(audioData), vc.GuildID)

	if len(audioData) < 16000 {
		log.Printf("Audio data too small (%d bytes), skipping", len(audioData))
		return
	}

	// Convert PCM to WAV
	wavData, err := vm.pcmToWav(audioData)
	if err != nil {
		log.Printf("Error converting PCM to WAV: %v", err)
		return
	}

	// Transcribe audio to text
	text, err := vm.handler.rag.AI.SpeechToText(bytes.NewReader(wavData))
	if err != nil {
		log.Printf("Error in speech-to-text: %v", err)
		return
	}

	if strings.TrimSpace(text) == "" {
		log.Printf("Empty transcription, skipping")
		return
	}

	log.Printf("Transcribed text from guild %s: %s", vc.GuildID, text)

	// Get guild info
	guild, err := vm.handler.session.Guild(vc.GuildID)
	if err != nil {
		log.Printf("Error getting guild info: %v", err)
		return
	}

	// Get channel info
	channel, err := vm.handler.session.Channel(vc.ChannelID)
	if err != nil {
		log.Printf("Error getting channel info: %v", err)
		return
	}

	// Get relevant context using RAG
	context, err := vm.handler.rag.SearchRelevantContext(text, vc.GuildID, 5)
	if err != nil {
		log.Printf("Error getting context: %v", err)
		return
	}

	// Generate AI response
	response, err := vm.handler.rag.GenerateResponse(text, context, "Voice User", guild.Name)
	if err != nil {
		log.Printf("Error generating response: %v", err)
		return
	}

	// Send text response to the channel
	go func() {
		_, err := vm.handler.session.ChannelMessageSend(channel.ID, "🎤 **Voice Message:** "+text+"\n\n"+response)
		if err != nil {
			log.Printf("Error sending message: %v", err)
		}
	}()

	// Generate and play TTS response
	go func() {
		ttsAudio, err := vm.handler.rag.AI.TextToSpeech(response)
		if err != nil {
			log.Printf("Error generating TTS: %v", err)
			return
		}

		if err := vm.SendAudio(vc, ttsAudio); err != nil {
			log.Printf("Error playing TTS audio: %v", err)
		}
	}()

	// Log the voice interaction
	vm.handler.logVoiceInteraction(vc.GuildID, channel.ID, vc.UserId, "Voice User", text, response)
}

func (vm *VoiceManager) pcmToWav(pcmData []byte) ([]byte, error) {
	// Create temporary files
	pcmFile, err := os.CreateTemp("", "voice-*.pcm")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp PCM file: %v", err)
	}
	defer os.Remove(pcmFile.Name())
	defer pcmFile.Close()

	wavFile, err := os.CreateTemp("", "voice-*.wav")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp WAV file: %v", err)
	}
	defer os.Remove(wavFile.Name())
	defer wavFile.Close()

	// Write PCM data
	if _, err := pcmFile.Write(pcmData); err != nil {
		return nil, fmt.Errorf("failed to write PCM data: %v", err)
	}
	pcmFile.Close()

	// Convert PCM to proper WAV using FFmpeg
	cmd := exec.Command("ffmpeg",
		"-f", "s16le", // Input format: 16-bit signed little-endian
		"-ar", "48000", // Sample rate: 48kHz
		"-ac", "2", // Channels: stereo
		"-i", pcmFile.Name(), // Input file
		"-acodec", "pcm_s16le", // Audio codec
		"-ar", "16000", // Resample to 16kHz for OpenAI
		"-ac", "1", // Convert to mono for OpenAI
		"-y",           // Overwrite output
		wavFile.Name()) // Output file

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg conversion failed: %v, stderr: %s", err, stderr.String())
	}

	// Read the WAV file
	wavFile, err = os.Open(wavFile.Name())
	if err != nil {
		return nil, fmt.Errorf("failed to open WAV file: %v", err)
	}
	defer wavFile.Close()

	wavData, err := io.ReadAll(wavFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read WAV data: %v", err)
	}

	return wavData, nil
}
