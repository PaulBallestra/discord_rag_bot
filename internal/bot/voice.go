// internal/bot/voice.go
package bot

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/jonas747/dca"
	"layeh.com/gopus"
)

type VoiceManager struct {
	handler     *BotHandler
	connections map[string]*VoiceConnection
	mu          sync.RWMutex
}

type VoiceConnection struct {
	GuildID      string
	ChannelID    string
	Connection   *discordgo.VoiceConnection
	IsRecording  bool
	AudioBuffer  *bytes.Buffer
	LastActivity time.Time
	UserId       string
	decoder      *gopus.Decoder
	mu           sync.RWMutex
}

func NewVoiceManager(handler *BotHandler) *VoiceManager {
	return &VoiceManager{
		handler:     handler,
		connections: make(map[string]*VoiceConnection),
	}
}

func (vm *VoiceManager) HandleVoiceStateUpdate(s *discordgo.Session, vsu *discordgo.VoiceStateUpdate) {
	// Auto-join when user joins a voice channel and mentions the bot
	if vsu.ChannelID != "" && vsu.UserID != vm.handler.botID {
		// Store user's voice channel for potential bot joining
	}
}

func (vm *VoiceManager) JoinVoiceChannel(s *discordgo.Session, guildID, channelID, userID string) error {
	vm.mu.Lock()
	defer vm.mu.Unlock()

	// Check if already connected to this guild
	if existing, exists := vm.connections[guildID]; exists {
		if existing.ChannelID == channelID {
			return nil // Already in the right channel
		}
		// Disconnect from current channel
		existing.Connection.Disconnect()
		delete(vm.connections, guildID)
	}

	// Join the voice channel
	vc, err := s.ChannelVoiceJoin(guildID, channelID, false, true)
	if err != nil {
		return fmt.Errorf("failed to join voice channel: %v", err)
	}

	// Create decoder for incoming audio
	decoder, err := gopus.NewDecoder(48000, 1) // 48kHz, mono
	if err != nil {
		vc.Disconnect()
		return fmt.Errorf("failed to create opus decoder: %v", err)
	}

	voiceConn := &VoiceConnection{
		GuildID:      guildID,
		ChannelID:    channelID,
		Connection:   vc,
		IsRecording:  false,
		AudioBuffer:  &bytes.Buffer{},
		LastActivity: time.Now(),
		UserId:       userID,
		decoder:      decoder,
	}

	vm.connections[guildID] = voiceConn

	// Start listening for voice data
	go vm.listenForVoice(voiceConn)

	log.Printf("Joined voice channel %s in guild %s", channelID, guildID)
	return nil
}

func (vm *VoiceManager) listenForVoice(vc *VoiceConnection) {
	for {
		select {
		case packet := <-vc.Connection.OpusRecv:
			if packet != nil {
				vm.processVoicePacket(vc, packet)
			}
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
	// We want to process all incoming voice packets, not just from the bot
	// Removed the SSRC check that was filtering out user voice

	vc.mu.Lock()
	defer vc.mu.Unlock()

	// Decode Opus to PCM
	pcm, err := vc.decoder.Decode(packet.Opus, 960, false)
	if err != nil {
		log.Printf("Error decoding opus: %v", err)
		return
	}

	// Convert to bytes and write to buffer
	buf := new(bytes.Buffer)
	err = binary.Write(buf, binary.LittleEndian, pcm)
	if err != nil {
		log.Printf("Error writing PCM data: %v", err)
		return
	}

	vc.AudioBuffer.Write(buf.Bytes())
	vc.LastActivity = time.Now()

	// If we're not currently recording, start recording
	if !vc.IsRecording {
		vc.IsRecording = true
		go vm.handleVoiceRecording(vc)
	}
}

func (vm *VoiceManager) handleVoiceRecording(vc *VoiceConnection) {
	// Wait for silence (no new audio for 2 seconds)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	lastBufferSize := 0
	silenceCount := 0

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

			// If we have 2 seconds of silence and some audio data
			if silenceCount >= 20 && currentSize > 0 {
				vm.processRecordedAudio(vc)
				return
			}

			// Maximum recording time of 30 seconds
			if silenceCount >= 300 {
				vm.processRecordedAudio(vc)
				return
			}
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

	if len(audioData) < 1000 { // Too short to be meaningful
		return
	}

	log.Printf("Processing recorded audio (%d bytes) from guild %s", len(audioData), vc.GuildID)

	// Convert PCM to WAV and send to AI
	go vm.processVoiceToAI(vc, audioData)
}

func (vm *VoiceManager) processVoiceToAI(vc *VoiceConnection, pcmData []byte) {
	// Convert PCM to WAV format
	wavData, err := vm.pcmToWav(pcmData)
	if err != nil {
		log.Printf("Error converting PCM to WAV: %v", err)
		return
	}

	// Send to speech-to-text
	text, err := vm.handler.rag.AI.SpeechToText(bytes.NewReader(wavData))
	if err != nil {
		log.Printf("Error in speech-to-text: %v", err)
		return
	}

	if text == "" {
		log.Println("No text detected from audio")
		return
	}

	log.Printf("Transcribed text: %s", text)

	// Get guild info for context
	guild, err := vm.handler.session.Guild(vc.GuildID)
	if err != nil {
		log.Printf("Error getting guild: %v", err)
		return
	}

	// Get user info
	user, err := vm.handler.session.User(vc.UserId)
	if err != nil {
		log.Printf("Error getting user: %v", err)
		return
	}

	// Generate AI response using RAG
	context, err := vm.handler.rag.SearchRelevantContext(text, vc.GuildID, 5)
	if err != nil {
		log.Printf("Error getting context: %v", err)
		return
	}

	response, err := vm.handler.rag.GenerateResponse(text, context, user.Username, guild.Name)
	if err != nil {
		log.Printf("Error generating AI response: %v", err)
		return
	}

	log.Printf("AI Response: %s", response)

	// Convert response to speech and play
	err = vm.speakResponse(vc, response)
	if err != nil {
		log.Printf("Error speaking response: %v", err)
		return
	}

	// Log the voice interaction
	vm.handler.logVoiceInteraction(vc.GuildID, vc.ChannelID, vc.UserId, user.Username, text, response)
}

func (vm *VoiceManager) speakResponse(vc *VoiceConnection, text string) error {
	// Generate speech from text
	audioData, err := vm.handler.rag.AI.TextToSpeech(text)
	if err != nil {
		return fmt.Errorf("error generating speech: %v", err)
	}

	// Save audio to temporary file
	tempFile := fmt.Sprintf("/tmp/tts_%d.mp3", time.Now().UnixNano())
	err = os.WriteFile(tempFile, audioData, 0644)
	if err != nil {
		return fmt.Errorf("error saving audio file: %v", err)
	}
	defer os.Remove(tempFile)

	// Convert MP3 to DCA format for Discord
	dcaFile := fmt.Sprintf("/tmp/tts_%d.dca", time.Now().UnixNano())
	defer os.Remove(dcaFile)

	err = vm.convertToDCA(tempFile, dcaFile)
	if err != nil {
		return fmt.Errorf("error converting to DCA: %v", err)
	}

	// Play the audio
	return vm.playAudioFile(vc.Connection, dcaFile)
}

func (vm *VoiceManager) convertToDCA(inputFile, outputFile string) error {
	// Use FFmpeg to convert to DCA format
	cmd := exec.Command("ffmpeg",
		"-i", inputFile,
		"-f", "s16le",
		"-ar", "48000",
		"-ac", "2",
		"-c:a", "pcm_s16le",
		"pipe:1")

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	// Create DCA encoder that writes to file
	output, err := os.Create(outputFile)
	if err != nil {
		return fmt.Errorf("error creating output file: %v", err)
	}
	defer output.Close()

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("error getting stdout pipe: %v", err)
	}

	// Start FFmpeg
	err = cmd.Start()
	if err != nil {
		return fmt.Errorf("ffmpeg error: %v, stderr: %s", err, stderr.String())
	}

	// Create DCA encoder
	opts := dca.StdEncodeOptions
	opts.RawOutput = true
	opts.Bitrate = 128
	opts.Application = "audio"

	encoder, err := dca.EncodeMem(stdout, opts)
	if err != nil {
		cmd.Process.Kill()
		return fmt.Errorf("error creating DCA encoder: %v", err)
	}
	defer encoder.Cleanup()

	// Copy encoder output to file
	_, err = io.Copy(output, encoder)
	if err != nil {
		return fmt.Errorf("error writing DCA data: %v", err)
	}

	// Wait for FFmpeg to finish
	err = cmd.Wait()
	if err != nil {
		return fmt.Errorf("ffmpeg wait error: %v, stderr: %s", err, stderr.String())
	}

	return nil
}

func (vm *VoiceManager) playAudioFile(vc *discordgo.VoiceConnection, filename string) error {
	// Open the DCA file
	file, err := os.Open(filename)
	if err != nil {
		return fmt.Errorf("error opening DCA file: %v", err)
	}
	defer file.Close()

	// Create a new decoder
	decoder := dca.NewDecoder(file)

	// Start speaking
	vc.Speaking(true)
	defer vc.Speaking(false)

	// Create a buffer for audio data
	frameBuffer := make([][]byte, 0)

	// Read all frames
	for {
		frame, err := decoder.OpusFrame()
		if err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("error decoding opus frame: %v", err)
		}
		frameBuffer = append(frameBuffer, frame)
	}

	// Play frames with correct timing
	for _, frame := range frameBuffer {
		vc.OpusSend <- frame
		// Sleep for the frame duration (20ms)
		time.Sleep(20 * time.Millisecond)
	}

	return nil
}

func (vm *VoiceManager) pcmToWav(pcmData []byte) ([]byte, error) {
	// WAV header for 48kHz, 1 channel, 16-bit PCM
	const (
		sampleRate    = 48000
		channels      = 1
		bitsPerSample = 16
	)

	dataSize := len(pcmData)
	fileSize := 36 + dataSize

	buf := new(bytes.Buffer)

	// RIFF header
	buf.WriteString("RIFF")
	binary.Write(buf, binary.LittleEndian, uint32(fileSize))
	buf.WriteString("WAVE")

	// fmt chunk
	buf.WriteString("fmt ")
	binary.Write(buf, binary.LittleEndian, uint32(16)) // chunk size
	binary.Write(buf, binary.LittleEndian, uint16(1))  // PCM format
	binary.Write(buf, binary.LittleEndian, uint16(channels))
	binary.Write(buf, binary.LittleEndian, uint32(sampleRate))
	binary.Write(buf, binary.LittleEndian, uint32(sampleRate*channels*bitsPerSample/8)) // byte rate
	binary.Write(buf, binary.LittleEndian, uint16(channels*bitsPerSample/8))            // block align
	binary.Write(buf, binary.LittleEndian, uint16(bitsPerSample))

	// data chunk
	buf.WriteString("data")
	binary.Write(buf, binary.LittleEndian, uint32(dataSize))
	buf.Write(pcmData)

	return buf.Bytes(), nil
}

func (vm *VoiceManager) LeaveVoiceChannel(guildID string) error {
	vm.mu.Lock()
	defer vm.mu.Unlock()

	if vc, exists := vm.connections[guildID]; exists {
		vc.Connection.Disconnect()
		delete(vm.connections, guildID)
		log.Printf("Left voice channel in guild %s", guildID)
	}

	return nil
}
