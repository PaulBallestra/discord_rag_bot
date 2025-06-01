package ai

import (
	"context"
	"fmt"
	"io"
	"log"
	"strings"
	"time"

	"github.com/sashabaranov/go-openai"
)

type AIService struct {
	client *openai.Client
}

func NewAIService(apiKey string) *AIService {
	return &AIService{
		client: openai.NewClient(apiKey),
	}
}

func (ai *AIService) GenerateResponse(prompt string, contextInfo string) (string, error) {
	fullPrompt := fmt.Sprintf(`Context from previous conversations:
%s

Current question: %s

Please provide a helpful response based on the context above.`, contextInfo, prompt)

	req := openai.ChatCompletionRequest{
		Model: openai.GPT3Dot5Turbo,
		Messages: []openai.ChatCompletionMessage{
			{
				Role:    openai.ChatMessageRoleUser,
				Content: fullPrompt,
			},
		},
		MaxTokens:   500,
		Temperature: 0.7,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := ai.client.CreateChatCompletion(ctx, req)
	if err != nil {
		if strings.Contains(err.Error(), "429") || strings.Contains(err.Error(), "quota") {
			log.Printf("OpenAI quota exceeded, using fallback response")
			return ai.getFallbackResponse(prompt, contextInfo), nil
		}
		return "", fmt.Errorf("failed to generate response: %v", err)
	}

	if len(resp.Choices) == 0 {
		return ai.getFallbackResponse(prompt, contextInfo), nil
	}

	return resp.Choices[0].Message.Content, nil
}

func (ai *AIService) GenerateEmbedding(text string) ([]float32, error) {
	req := openai.EmbeddingRequest{
		Input: []string{text},
		Model: openai.AdaEmbeddingV2,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := ai.client.CreateEmbeddings(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to create embedding: %v", err)
	}

	if len(resp.Data) == 0 {
		return nil, fmt.Errorf("no embedding data returned")
	}

	return resp.Data[0].Embedding, nil
}

// NEW: Text-to-Speech using OpenAI's TTS
func (ai *AIService) TextToSpeech(text string) ([]byte, error) {
	req := openai.CreateSpeechRequest{
		Model:          openai.TTSModel1,
		Input:          text,
		Voice:          openai.VoiceAlloy,
		ResponseFormat: openai.SpeechResponseFormatMp3,
		Speed:          1.0,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	response, err := ai.client.CreateSpeech(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to create speech: %v", err)
	}
	defer response.Close()

	// Read all the audio data
	audioData, err := io.ReadAll(response)
	if err != nil {
		return nil, fmt.Errorf("failed to read audio data: %v", err)
	}

	return audioData, nil
}

// NEW: Speech-to-Text using OpenAI's Whisper
func (ai *AIService) SpeechToText(audioReader io.Reader) (string, error) {
	req := openai.AudioRequest{
		Model:    openai.Whisper1,
		Reader:   audioReader,
		Prompt:   "",
		Format:   openai.AudioResponseFormatText,
		Language: "en",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	resp, err := ai.client.CreateTranscription(ctx, req)
	if err != nil {
		return "", fmt.Errorf("failed to transcribe audio: %v", err)
	}

	return resp.Text, nil
}

// Fallback response when OpenAI is unavailable
func (ai *AIService) getFallbackResponse(prompt string, contextInfo string) string {
	prompt = strings.ToLower(prompt)

	switch {
	case strings.Contains(prompt, "hello") || strings.Contains(prompt, "hi"):
		return "Hello! I'm having trouble connecting to my AI service right now, but I'm here to help!"
	case strings.Contains(prompt, "how") && strings.Contains(prompt, "you"):
		return "I'm doing well, thanks for asking! Though I'm currently running in fallback mode due to API limitations."
	case strings.Contains(prompt, "help"):
		return "I'd love to help! I can answer questions about our previous conversations, but I'm currently in limited mode."
	case strings.Contains(prompt, "what"):
		return "That's a great question! I'm currently unable to access my full AI capabilities, but I'm still here to chat."
	default:
		if contextInfo != "" {
			return "I found some relevant context from our previous conversations, but I'm currently unable to provide a detailed response due to API limitations. Please try again later!"
		}
		return "I understand you're asking about something, but I'm currently running in limited mode. Please try again later or rephrase your question!"
	}
}
