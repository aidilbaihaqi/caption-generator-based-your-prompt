package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
)

// ==========================
// Struct untuk request dari client
// ==========================

type GenerateCaptionRequest struct {
	Platform    string `json:"platform" binding:"required"`
	Language    string `json:"language" binding:"required"`
	Tone        string `json:"tone" binding:"required"`
	Description string `json:"description" binding:"required"`
	Variants    int    `json:"variants"` // optional, default 2
}

// ==========================
// Struct untuk request ke OpenRouter
// ==========================

type ORMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ORChatRequest struct {
	Model       string      `json:"model"`
	Messages    []ORMessage `json:"messages"`
	Temperature float32     `json:"temperature"`
	MaxTokens   int         `json:"max_tokens"`
}

// ==========================
// Struct untuk response dari OpenRouter
// (dibuat lebih fleksibel supaya tidak mudah error)
// ==========================

type ORChatResponse struct {
	Choices []struct {
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`

		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`

		Content string `json:"content"`
	} `json:"choices"`
}

// ==========================
// Handler: generate caption
// ==========================

func generateCaptionHandler(c *gin.Context) {
	var req GenerateCaptionRequest

	// bind json dari client
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	// default variant 2
	if req.Variants <= 0 {
		req.Variants = 2
	}

	// NOTE: tadi di sini kamu pakai `$s` -> harusnya `%s`
	prompt := fmt.Sprintf(`
Buat %d caption %s dalam bahasa %s.
Deskripsi konten: %s
Tone: %s.
Setiap caption pisahkan dengan baris baru.
Tambahkan hashtag relevan (maksimal 8 hashtag).
Jangan tambahkan penjelasan lain di luar caption.`,
		req.Variants, req.Platform, req.Language, req.Description, req.Tone,
	)

	// call OpenRouter
	captionText, err := callOpenRouter(prompt)
	if err != nil {
		log.Println("error callOpenRouter:", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   err.Error(), // sementara kirim error asli biar kelihatan saat dev
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":     true,
		"caption_raw": captionText,
	})
}

// ==========================
// Fungsi callOpenRouter
// ==========================

func callOpenRouter(prompt string) (string, error) {
	apiKey := os.Getenv("OPENROUTER_API_KEY")
	if apiKey == "" {
		return "", fmt.Errorf("OPENROUTER_API_KEY tidak di-set, cek file .env")
	}

	// Payload ke OpenRouter
	body := ORChatRequest{
		// Untuk awal, pakai auto dulu biar pasti jalan
		// nanti kalau mau spesifik bisa ganti lagi
		Model: "openrouter/auto",
		Messages: []ORMessage{
			{
				Role: "system",
				Content: "You are an AI assistant specialized in generating high-quality social media captions. " +
					"You adapt your writing style based on the user's instructions, such as platform, audience, tone, language, and content description. " +
					"Generate captions that are clear, engaging, and relevant to the context provided. " +
					"Avoid adding explanations, disclaimers, or content outside the requested captions.",
			},
			{
				Role:    "user",
				Content: prompt,
			},
		},
		Temperature: 0.9,
		MaxTokens:   400,
	}

	jsonBytes, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest(
		"POST",
		"https://openrouter.ai/api/v1/chat/completions",
		bytes.NewBuffer(jsonBytes),
	)
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	// optional
	// req.Header.Set("HTTP-Referer", "https://your-app-domain.example")
	// req.Header.Set("X-Title", "Caption Generator Service")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	log.Println("RAW RESPONSE FROM OPENROUTER:", string(bodyBytes))

	// Kalau status >= 400, langsung balikin error + raw body biar kelihatan
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("OpenRouter error status: %s | body: %s", resp.Status, string(bodyBytes))
	}

	// Cek dulu: kalau body tidak mulai dengan '{' atau '[',
	// kemungkinan besar ini bukan JSON (HTML / text jadi).
	if len(bodyBytes) == 0 {
		return "", fmt.Errorf("empty response from OpenRouter")
	}
	if bodyBytes[0] != '{' && bodyBytes[0] != '[' {
		return "", fmt.Errorf("non-JSON response from OpenRouter: %s", string(bodyBytes))
	}

	var orResp ORChatResponse
	if err := json.Unmarshal(bodyBytes, &orResp); err != nil {
		return "", fmt.Errorf("failed to parse JSON from OpenRouter: %w | body: %s", err, string(bodyBytes))
	}

	if len(orResp.Choices) == 0 {
		return "", fmt.Errorf("no choices returned from OpenRouter | body: %s", string(bodyBytes))
	}

	choice := orResp.Choices[0]

	// beberapa fallback possible field
	if choice.Message.Content != "" {
		return choice.Message.Content, nil
	}
	if choice.Delta.Content != "" {
		return choice.Delta.Content, nil
	}
	if choice.Content != "" {
		return choice.Content, nil
	}

	return "", fmt.Errorf("no content field found in OpenRouter response | body: %s", string(bodyBytes))
}

// ==========================
// entrypoint
// ==========================

func main() {
	// Load .env DULU
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, using system environment variables")
	}

	// Baru cek dan log API key (hapus di production biar aman)
	log.Println("OPENROUTER_API_KEY (prefix):", os.Getenv("OPENROUTER_API_KEY")[:5])

	router := gin.Default()

	router.POST("/generate-caption", generateCaptionHandler)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Println("Server running on port", port)
	if err := router.Run(":" + port); err != nil {
		log.Fatal("failed to run server:", err)
	}
}
