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

	// Add interaction handler for slash commands
	s.AddHandler(h.handleInteraction)
}

// RegisterCommands registers slash commands for the bot
func (h *BotHandler) RegisterCommands() error {
	commands := []*discordgo.ApplicationCommand{
		{
			Name:        "join",
			Description: "Join your current voice channel",
		},
		{
			Name:        "leave",
			Description: "Leave the current voice channel",
		},
		{
			Name:        "ai",
			Description: "Ask the AI a question",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "question",
					Description: "The question to ask the AI",
					Required:    true,
				},
			},
		},
	}

	for _, cmd := range commands {
		_, err := h.session.ApplicationCommandCreate(h.session.State.User.ID, "", cmd)
		if err != nil {
			return fmt.Errorf("error creating '%s' command: %v", cmd.Name, err)
		}
	}

	log.Println("Slash commands registered successfully")
	return nil
}

// handleInteraction handles slash command interactions
func (h *BotHandler) handleInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}

	switch i.ApplicationCommandData().Name {
	case "join":
		h.handleJoinInteraction(s, i)
	case "leave":
		h.handleLeaveInteraction(s, i)
	case "ai":
		h.handleAIInteraction(s, i)
	}
}

// Keeping the existing message handlers for backward compatibility

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
	// Clean the query - extract just the actual question from the message
	query := m.Content

	// Remove the bot mention
	query = strings.ReplaceAll(query, "<@"+h.botID+">", "")

	// Remove the /ai prefix if present
	query = strings.ReplaceAll(query, "/ai ", "")

	// Trim any extra whitespace
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

	// Send text response (only once)
	s.ChannelMessageSend(m.ChannelID, response)

	// Check if we have a voice connection for this guild
	h.voiceManager.mu.RLock()
	vc, hasVoiceConnection := h.voiceManager.connections[m.GuildID]
	h.voiceManager.mu.RUnlock()

	// Generate and send voice response if in a voice channel
	if hasVoiceConnection && vc != nil {
		// Generate TTS audio and send to voice channel
		ttsAudio, err := h.rag.AI.TextToSpeech(response)
		if err != nil {
			log.Printf("Error generating TTS audio: %v", err)
		} else {
			// Send the TTS audio to the voice channel in a goroutine
			go func() {
				if err := h.voiceManager.SendAudio(vc, ttsAudio); err != nil {
					log.Printf("Error sending audio: %v", err)
				}
			}()
		}
	}

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

// Add these missing functions to handler.go

func (h *BotHandler) handleJoinInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Acknowledge the interaction immediately
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	})
	if err != nil {
		log.Printf("Error responding to interaction: %v", err)
		return
	}

	// Find user's voice channel
	guild, err := s.State.Guild(i.GuildID)
	if err != nil {
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: &[]string{"Error: Could not find guild"}[0],
		})
		return
	}

	var voiceChannelID string
	for _, vs := range guild.VoiceStates {
		if vs.UserID == i.Member.User.ID {
			voiceChannelID = vs.ChannelID
			break
		}
	}

	if voiceChannelID == "" {
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: &[]string{"❌ You need to be in a voice channel first!"}[0],
		})
		return
	}

	// Join voice channel
	err = h.voiceManager.JoinVoiceChannel(s, i.GuildID, voiceChannelID, i.Member.User.ID)
	if err != nil {
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: &[]string{fmt.Sprintf("Error joining voice channel: %v", err)}[0],
		})
		return
	}

	s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Content: &[]string{"🎤 Joined voice channel! You can now talk to me. I'm listening..."}[0],
	})
}

func (h *BotHandler) handleLeaveInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Acknowledge the interaction immediately
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	})
	if err != nil {
		log.Printf("Error responding to interaction: %v", err)
		return
	}

	err = h.voiceManager.LeaveVoiceChannel(i.GuildID)
	if err != nil {
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: &[]string{fmt.Sprintf("Error leaving voice channel: %v", err)}[0],
		})
		return
	}

	s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Content: &[]string{"👋 Left voice channel!"}[0],
	})
}

func (h *BotHandler) handleAIInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Acknowledge the interaction immediately
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	})
	if err != nil {
		log.Printf("Error responding to interaction: %v", err)
		return
	}

	options := i.ApplicationCommandData().Options
	if len(options) == 0 {
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: &[]string{"Please provide a question!"}[0],
		})
		return
	}

	query := options[0].StringValue()
	if query == "" {
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: &[]string{"Hi! How can I help you?"}[0],
		})
		return
	}

	// Get guild info
	guild, err := s.Guild(i.GuildID)
	if err != nil {
		log.Printf("Error getting guild: %v", err)
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: &[]string{"Sorry, I encountered an error."}[0],
		})
		return
	}

	// Get relevant context using RAG
	context, err := h.rag.SearchRelevantContext(query, i.GuildID, 5)
	if err != nil {
		log.Printf("Error getting context: %v", err)
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: &[]string{"Sorry, I encountered an error while searching for context."}[0],
		})
		return
	}

	// Generate AI response
	response, err := h.rag.GenerateResponse(query, context, i.Member.User.Username, guild.Name)
	if err != nil {
		log.Printf("Error generating response: %v", err)
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: &[]string{"Sorry, I encountered an error while generating a response."}[0],
		})
		return
	}

	// Send text response
	s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Content: &response,
	})

	// Check if we have a voice connection for this guild
	h.voiceManager.mu.RLock()
	vc, hasVoiceConnection := h.voiceManager.connections[i.GuildID]
	h.voiceManager.mu.RUnlock()

	// Generate and send voice response if in a voice channel
	if hasVoiceConnection && vc != nil {
		// Generate TTS audio and send to voice channel
		ttsAudio, err := h.rag.AI.TextToSpeech(response)
		if err != nil {
			log.Printf("Error generating TTS audio: %v", err)
		} else {
			// Send the TTS audio to the voice channel in a goroutine
			go func() {
				if err := h.voiceManager.SendAudio(vc, ttsAudio); err != nil {
					log.Printf("Error sending audio: %v", err)
				}
			}()
		}
	}

	// Log interaction
	interaction := &models.BotInteraction{
		UserID:    i.Member.User.ID,
		Username:  i.Member.User.Username,
		Query:     query,
		Response:  response,
		ChannelID: i.ChannelID,
		GuildID:   i.GuildID,
		Timestamp: time.Now(),
	}

	if err := h.db.Create(interaction).Error; err != nil {
		log.Printf("Error logging interaction: %v", err)
	}
}
