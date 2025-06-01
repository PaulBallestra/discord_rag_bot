// cmd/bot/main.go
package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"discord-rag-bot/internal/ai"
	"discord-rag-bot/internal/bot"
	"discord-rag-bot/internal/database"
	"discord-rag-bot/internal/rag"

	"github.com/bwmarrin/discordgo"
	"github.com/joho/godotenv"
)

func main() {
	// Load environment variables
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found")
	}

	// Initialize database
	db, err := database.NewDB(
		os.Getenv("DB_HOST"),
		os.Getenv("DB_USER"),
		os.Getenv("DB_PASSWORD"),
		os.Getenv("DB_NAME"),
		5432,
	)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}

	// Initialize AI service
	aiService := ai.NewAIService(os.Getenv("OPENAI_API_KEY"))

	// Initialize RAG retriever
	ragRetriever := rag.NewRAGRetriever(db, aiService)

	// Initialize bot handler (includes voice manager)
	botHandler := bot.NewBotHandler(db, ragRetriever)

	// Create Discord session
	discord, err := discordgo.New("Bot " + os.Getenv("DISCORD_TOKEN"))
	if err != nil {
		log.Fatalf("Error creating Discord session: %v", err)
	}

	// Set up bot handler
	botHandler.SetSession(discord)

	// Add event handlers
	discord.AddHandler(botHandler.OnMessageCreate)

	// Set intents (add voice state intent)
	discord.Identify.Intents = discordgo.IntentsGuildMessages |
		discordgo.IntentsDirectMessages |
		discordgo.IntentsGuildVoiceStates

	// Open connection
	if err := discord.Open(); err != nil {
		log.Fatalf("Error opening Discord connection: %v", err)
	}
	defer discord.Close()

	log.Println("🎤 Discord Voice RAG Bot is running!")
	log.Println("Commands:")
	log.Println("  /join - Join your voice channel")
	log.Println("  /leave - Leave voice channel")
	log.Println("  @bot <message> - Text chat with AI")
	log.Println("  Just talk when bot is in voice channel!")

	// Wait for interrupt signal
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	log.Println("Shutting down Discord Voice RAG Bot...")
}
