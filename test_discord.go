// Create a file: test_discord.go
package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/joho/godotenv"
)

func main() {
	// Load .env file
	err := godotenv.Load()
	if err != nil {
		log.Println("Warning: No .env file found")
	}

	token := os.Getenv("DISCORD_TOKEN")
	if token == "" {
		log.Fatal("DISCORD_TOKEN is not set")
	}

	fmt.Printf("Testing token: %s...%s (length: %d)\n",
		token[:10], token[len(token)-10:], len(token))

	// Create session
	dg, err := discordgo.New("Bot " + token)
	if err != nil {
		log.Fatal("Error creating Discord session:", err)
	}

	// Set a timeout
	dg.Client.Timeout = 10 * time.Second

	// Try to open connection
	fmt.Println("Attempting to connect...")
	err = dg.Open()
	if err != nil {
		log.Fatal("Error opening connection:", err)
	}

	fmt.Println("✅ Connected successfully!")
	fmt.Printf("Bot user: %s#%s (ID: %s)\n",
		dg.State.User.Username, dg.State.User.Discriminator, dg.State.User.ID)

	dg.Close()
}
