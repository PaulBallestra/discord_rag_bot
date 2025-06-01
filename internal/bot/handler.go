// internal/bot/handler.go
package bot

import (
	"discord-rag-bot/internal/database"
	"discord-rag-bot/internal/models"
	"discord-rag-bot/internal/rag"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

type BotHandler struct {
	db           *database.DB
	rag          *rag.RAGRetriever
	session      *discordgo.Session
	botID        string
	voiceManager *VoiceManager
}

func NewBotHandler(db *database.DB, rag *rag.RAGRetriever) *BotHandler {
	handler := &BotHandler{
		db:  db,
		rag: rag,
	}
	handler.voiceManager = NewVoiceManager(handler)
	return handler
}

func (h *BotHandler) SetSession(s *discordgo.Session) {
	h.session = s
	user, err := s.User("@me")
	if err != nil {
		log.Printf("Error getting bot user: %v", err)
		return
	}
	h.botID = user.ID

	// Add voice state update handler
	s.AddHandler(h.voiceManager.HandleVoiceStateUpdate)
}

func (h *BotHandler) OnMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	// Ignore bot messages
	if m.Author.ID == h.botID {
		return
	}

	// Store message for RAG
	go h.storeMessage(m)

	// Check for voice commands
	if strings.HasPrefix(m.Content, "/join") || strings.Contains(m.Content, "join voice") {
		h.handleJoinVoiceCommand(s, m)
		return
	}

	if strings.HasPrefix(m.Content, "/leave") || strings.Contains(m.Content, "leave voice") {
		h.handleLeaveVoiceCommand(s, m)
		return
	}

	// Check if bot is mentioned or DM for text chat
	botMentioned := strings.Contains(m.Content, "<@"+h.botID+">") ||
		strings.HasPrefix(m.Content, "/ai ") ||
		m.GuildID == "" // DM

	if botMentioned {
		go h.handleAIQuery(s, m)
	}
}

func (h *BotHandler) handleJoinVoiceCommand(s *discordgo.Session, m *discordgo.MessageCreate) {
	// Find the user's voice channel
	guild, err := s.State.Guild(m.GuildID)
	if err != nil {
		s.ChannelMessageSend(m.ChannelID, "Error finding your voice channel.")
		return
	}

	var voiceChannelID string
	for _, vs := range guild.VoiceStates {
		if vs.UserID == m.Author.ID {
			voiceChannelID = vs.ChannelID
			break
		}
	}

	if voiceChannelID == "" {
		s.ChannelMessageSend(m.ChannelID, "You need to be in a voice channel for me to join!")
		return
	}

	err = h.voiceManager.JoinVoiceChannel(s, m.GuildID, voiceChannelID, m.Author.ID)
	if err != nil {
		s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Error joining voice channel: %v", err))
		return
	}

	s.ChannelMessageSend(m.ChannelID, "🎤 Joined voice channel! You can now talk to me. I'm listening...")
}

func (h *BotHandler) handleLeaveVoiceCommand(s *discordgo.Session, m *discordgo.MessageCreate) {
	err := h.voiceManager.LeaveVoiceChannel(m.GuildID)
	if err != nil {
		s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Error leaving voice channel: %v", err))
		return
	}

	s.ChannelMessageSend(m.ChannelID, "👋 Left voice channel!")
}

func (h *BotHandler) logVoiceInteraction(guildID, channelID, userID, username, query, response string) {
	interaction := &models.BotInteraction{
		UserID:    userID,
		Username:  username,
		Query:     query,
		Response:  response,
		ChannelID: channelID,
		GuildID:   guildID,
		IsVoice:   true,
		Timestamp: time.Now(),
	}

	if err := h.db.Create(interaction).Error; err != nil {
		log.Printf("Error logging voice interaction: %v", err)
	}
}

func (h *BotHandler) storeMessage(m *discordgo.MessageCreate) {
	if m.Content == "" || len(m.Content) < 10 {
		return // Skip empty or very short messages
	}

	// Get channel and guild info
	channel, err := h.session.Channel(m.ChannelID)
	if err != nil {
		log.Printf("Error getting channel info: %v", err)
		return
	}

	guild, err := h.session.Guild(m.GuildID)
	if err != nil {
		log.Printf("Error getting guild info: %v", err)
		return
	}

	message := &models.DiscordMessage{
		MessageID:   m.ID,
		Content:     m.Content,
		Author:      m.Author.ID,
		Username:    m.Author.Username,
		ChannelID:   m.ChannelID,
		ChannelName: channel.Name,
		GuildID:     m.GuildID,
		GuildName:   guild.Name,
		Timestamp:   m.Timestamp,
	}

	// Use the new method that handles embeddings
	err = h.rag.StoreMessageWithEmbedding(message)
	if err != nil {
		log.Printf("Error storing message with embedding: %v", err)
	}
}

func (h *BotHandler) handleAIQuery(s *discordgo.Session, m *discordgo.MessageCreate) {
	// Clean the query
	query := strings.ReplaceAll(m.Content, "<@"+h.botID+">", "")
	query = strings.ReplaceAll(query, "/ai ", "")
	query = strings.TrimSpace(query)

	if query == "" {
		s.ChannelMessageSend(m.ChannelID, "Hi! How can I help you?")
		return
	}

	// Show typing indicator
	s.ChannelTyping(m.ChannelID)

	// Get guild info
	guild, err := s.Guild(m.GuildID)
	if err != nil {
		log.Printf("Error getting guild: %v", err)
		return
	}

	// Get relevant context using RAG
	context, err := h.rag.SearchRelevantContext(query, m.GuildID, 5)
	if err != nil {
		log.Printf("Error getting context: %v", err)
		s.ChannelMessageSend(m.ChannelID, "Sorry, I encountered an error while searching for context.")
		return
	}

	// Generate AI response
	response, err := h.rag.GenerateResponse(query, context, m.Author.Username, guild.Name)
	if err != nil {
		log.Printf("Error generating response: %v", err)
		s.ChannelMessageSend(m.ChannelID, "Sorry, I encountered an error while generating a response.")
		return
	}

	// Send response
	s.ChannelMessageSend(m.ChannelID, response)

	// Log interaction
	interaction := &models.BotInteraction{
		UserID:    m.Author.ID,
		Username:  m.Author.Username,
		Query:     query,
		Response:  response,
		ChannelID: m.ChannelID,
		GuildID:   m.GuildID,
		Timestamp: time.Now(),
	}

	if err := h.db.Create(interaction).Error; err != nil {
		log.Printf("Error logging interaction: %v", err)
	}
}
