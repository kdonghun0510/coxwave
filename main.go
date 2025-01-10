package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5"
	"github.com/joho/godotenv"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

var dbConn *pgx.Conn

func main() {
	err := godotenv.Load(".env")
	if err != nil {
		log.Fatalf("Error loading .env file: %v", err)
	}

	serverPort := os.Getenv("SERVER_PORT")
	host := os.Getenv("DB_HOST")
	dbPort := os.Getenv("DB_PORT")
	user := os.Getenv("DB_USER")
	password := os.Getenv("DB_PASSWORD")
	dbname := "coxwave"

	dbConnStr := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable", user, password, host, dbPort, dbname)
	dbConn, err = pgx.Connect(context.Background(), dbConnStr)
	if err != nil {
		log.Fatalf("Unable to connect to database: %v\n", err)
	}
	defer dbConn.Close(context.Background())

	// Route
	mux := mux.NewRouter()

	// Session
	mux.Use(SessionMiddleware)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./test.html") // chat.html 경로
	})
	mux.HandleFunc("/ping", pingHandler).Methods("GET")
	mux.HandleFunc("/history", chatHistoryHandler).Methods("GET")
	mux.HandleFunc("/chat", chatHandler).Methods("GET")

	// Server Runner
	log.Printf("Starting server on port %s", serverPort)
	err = http.ListenAndServe(fmt.Sprintf(":%s", serverPort), mux)
	if err != nil {
		log.Fatalf("Failed To Run Server: %s", err)
	}
}

func pingHandler(res http.ResponseWriter, req *http.Request) {
	fmt.Fprintln(res, "PING")
}

// middleware for session process
func SessionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("session_id")
		var sessionID string

		if err != nil || cookie.Value == "" {
			sessionID, err = generateSessionID()
			if err != nil {
				log.Printf("Error generating session ID: %s", err)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}

			// set session_id in cookie
			http.SetCookie(w, &http.Cookie{
				Name:     "session_id",
				Value:    sessionID,
				Path:     "/",
				HttpOnly: true, 
				Secure:   true,
				MaxAge:   3600,
			})
			log.Printf("Generated new session ID: %s", sessionID)
		} else {
			sessionID = cookie.Value
			log.Printf("Existing session ID: %s", sessionID)
		}

		ctx := context.WithValue(r.Context(), "session_id", sessionID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// generate a session_id
func generateSessionID() (string, error) {
	bytes := make([]byte, 16)
	_, err := rand.Read(bytes)
	if err != nil {
		return "", fmt.Errorf("failed to generate session ID: %w", err)
	}
	return hex.EncodeToString(bytes), nil
}


func chatHistoryHandler(res http.ResponseWriter, req *http.Request) {
	sessionID, ok := req.Context().Value("session_id").(string)
	if !ok || sessionID == "" {
		http.Error(res, "Invalid session ID", http.StatusBadRequest)
		return
	}

	// get chat history data
	contextRows, err := dbConn.Query(context.Background(), `
		SELECT question, answer
		FROM (
			SELECT question, answer, created_at
			FROM context
			WHERE session_id = $1
			ORDER BY id DESC 
			LIMIT 3
		) subquery
		ORDER BY created_at ASC;
	`, sessionID)
	if err != nil {
		http.Error(res, fmt.Sprintf("Database query error: %v", err), http.StatusInternalServerError)
		return
	}
	defer contextRows.Close()

	var contextResults []map[string]string

	for contextRows.Next() {
		var question, answer string
		if err := contextRows.Scan(&question, &answer); err != nil {
			http.Error(res, fmt.Sprintf("Error scanning row: %v", err), http.StatusInternalServerError)
			return
		}
		contextResults = append(contextResults, map[string]string{"question": question, "answer": answer})
	}

	if len(contextResults) == 0 {
		contextResults = []map[string]string{}
	}

	response := map[string]interface{}{
		"previous_chats": contextResults,
	}

	res.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(res).Encode(response); err != nil {
		http.Error(res, fmt.Sprintf("Error encoding response: %v", err), http.StatusInternalServerError)
		return
	}
}

func chatHandler(res http.ResponseWriter, req *http.Request) {
	conn, err := upgrader.Upgrade(res, req, nil)
	if err != nil {
		log.Printf("WebSocket Upgrade failed: %s", err)
		return
	}
	defer conn.Close()

	log.Println("WebSocket connection established")

	session_id, ok := req.Context().Value("session_id").(string)
	if !ok || session_id == "" {
		log.Println("Failed to retrieve session ID from context")
	}

	for {
		messageType, message, err := conn.ReadMessage()
		if err != nil {
			log.Printf("Error reading message: %s", err)
			break
		}
		log.Printf("Received: %s", message)
		
		// Process RAG (Vector Search and Generation)
		response, err := handleRAG(string(message), session_id)
		if err != nil {
			log.Printf("Error in RAG process: %s", err)
			conn.WriteMessage(messageType, []byte("Error processing your query"))
			continue
		}

		// Send the response back to the client
		err = conn.WriteMessage(messageType, []byte(response))
		if err != nil {
			log.Printf("Error writing message: %s", err)
			break
		}
	}
}

func handleRAG(query string, session_id string) (string, error) {
	// Generate embedding for query
	queryEmbedding, err := generateEmbedding(query)
	if err != nil {
		return "", fmt.Errorf("error generating embedding: %w", err)
	}

	embeddingJSON, err := json.Marshal(queryEmbedding)
	if err != nil {
		return "", fmt.Errorf("error marshalling embedding: %w", err)
	}

	// vector search in the database
	relevantRows, err := dbConn.Query(context.Background(), `
        SELECT question, answer
		FROM qna
		WHERE embedding <-> $1 < 1
		ORDER BY embedding <-> $1
		LIMIT 5;`, string(embeddingJSON))
	if err != nil {
		return "", fmt.Errorf("error performing vector search: %w", err)
	}
	defer relevantRows.Close()

	var relevantResults []map[string]string
	for relevantRows.Next() {
		var question, answer string
		if err := relevantRows.Scan(&question, &answer); err != nil {
			return "", fmt.Errorf("error scanning row: %w", err)
		}
		relevantResults = append(relevantResults, map[string]string{"question": question, "answer": answer})
	}
	log.Printf("검색 결과:  %s", relevantResults)
	
	// Exception control for retrieval
	if len(relevantResults) < 5 {
		words := strings.Fields(query)

		if len(words) > 1 {
			partialQuery := strings.Join(words[:len(words)-1], " ")

			regexRows, err := dbConn.Query(context.Background(), `
                SELECT question, answer
				FROM qna
				WHERE question ~* $1
				LIMIT $2;`, fmt.Sprintf(".*%s.*", partialQuery), 5-len(relevantResults))
			if err != nil {
				return "", fmt.Errorf("error performing regex search: %w", err)
			}
			defer regexRows.Close()

			for regexRows.Next() {
				var question, answer string
				if err := regexRows.Scan(&question, &answer); err != nil {
					return "", fmt.Errorf("error scanning regex row: %w", err)
				}
				relevantResults = append(relevantResults, map[string]string{"question": question, "answer": answer})
			}
		}
	}

	// Combine results for GPT
	relevant, err := json.Marshal(relevantResults)
	if err != nil {
		return "", fmt.Errorf("error marshalling RAG results: %w", err)
	}

	contextRows, err := dbConn.Query(context.Background(), `
        SELECT question, answer
		FROM (
			SELECT question, answer
			FROM context
			WHERE session_id = $1
			ORDER BY created_at DESC 
			LIMIT 3
		) subquery
		ORDER BY created_at ASC;
	`, session_id)
	defer contextRows.Close()

	var contextResults []map[string]string
	for contextRows.Next() {
		var question, answer string
		if err := contextRows.Scan(&question, &answer); err != nil {
			return "", fmt.Errorf("error scanning row: %w", err)
		}
		contextResults = append(contextResults, map[string]string{"question": question, "answer": answer})
	}

	user_context, err := json.Marshal(contextResults)
	if err != nil {
		return "", fmt.Errorf("error marshalling RAG results: %w", err)
	}
	log.Printf("히스토리 결과:  %s", contextResults)
	// Generate GPT response using RAG results
	gptResponse, err := callGPT(query, string(relevant), string(user_context))
	if err != nil {
		return "", fmt.Errorf("error generating GPT response: %w", err)
	}

	var queryData map[string]string
	err = json.Unmarshal([]byte(query), &queryData)
	if err != nil {
		return "", fmt.Errorf("error parsing query JSON: %w", err)
	}

	actualQuery, exists := queryData["query"]
	if !exists {
		return "", fmt.Errorf("query field missing in JSON")
	}

	_, err = dbConn.Exec(context.Background(), `
    INSERT INTO context (session_id, question, answer, created_at)
    VALUES ($1, $2, $3, NOW());`, session_id, actualQuery, gptResponse)

	if err != nil {
		return "", fmt.Errorf("error inserting into context table: %w", err)
	}

	return gptResponse, nil
}

type EmbeddingRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

func generateEmbedding(query string) ([]float32, error) {
	openAIKey := os.Getenv("OPENAI_API_KEY")
	if openAIKey == "" {
		return nil, fmt.Errorf("OpenAI API key not set in environment variables")
	}

	url := "https://api.openai.com/v1/embeddings"
	requestBody := EmbeddingRequest{
		Model: "text-embedding-3-small",
		Input: query,
	}

	payload, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("error encoding JSON payload: %w", err)
	}

	req, err := http.NewRequest("POST", url, strings.NewReader(string(payload)))
	if err != nil {
		return nil, fmt.Errorf("error creating request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+openAIKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error sending request to OpenAI: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := ioutil.ReadAll(resp.Body)
		return nil, fmt.Errorf("OpenAI API error: %s", body)
	}

	var response struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	err = json.NewDecoder(resp.Body).Decode(&response)
	if err != nil {
		return nil, fmt.Errorf("error decoding response: %w", err)
	}

	if len(response.Data) == 0 {
		return nil, fmt.Errorf("no embedding data returned")
	}

	return response.Data[0].Embedding, nil
}

type GPTRequest struct {
	Model    string         `json:"model"`
	Messages []GPTMessage   `json:"messages"`
	MaxTokens int           `json:"max_tokens"`
	Temperature float32     `json:"temperature"`
}

type GPTMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func callGPT(query string, relevant_information string, user_context string) (string, error) {
	openAIKey := os.Getenv("OPENAI_API_KEY")
	if openAIKey == "" {
		return "", fmt.Errorf("OpenAI API key not set in environment variables")
	}

	url := "https://api.openai.com/v1/chat/completions"
	requestBody := GPTRequest{
		Model: "gpt-4o-mini",
		Messages: []GPTMessage{
			{
				Role: "system",
				Content: `당신은 네이버 스마트스토어와 관련된 질문에 응답하는 'FAQ 챗봇'입니다. 

				당신의 주요 역할:
				1. 사용자가 질문한 내용(` + "`user_query`" + `)을 이해하고, 
				2. 제공된 데이터(` + "`relevant_information`" + ` 및 ` + "`user_context`" + `)를 바탕으로 사용자 질문에 정확히 답변합니다.
				
				### 응답 규칙:
				1. 사용자 질문이 제공된 ` + "`relevant_information`" + ` 데이터와 관련이 있으면, 데이터를 기반으로 답변을 생성하세요.
				2. 질문에 대해 제공된 데이터에서 찾을 수 없는 경우에도, 데이터를 종합적으로 분석하고 유사성을 활용하여 가능한 최선의 답변을 제공하세요.
				3. 사용자의 질문이 네이버 스마트스토어와 관련이 없다고 확실히 판단되면, 아래와 같은 응답을 제공합니다:
				- '저는 네이버 스마트스토어 FAQ를 위한 챗봇입니다. 관련된 질문을 부탁드립니다.'
				4. 데이터(` + "`relevant_information`" + ` 또는 ` + "`user_context`" + `)에서 제공된 정보와 질문의 단어 또는 의미적 유사성이 명확하다면, 이를 우선적으로 활용하세요.
				5. recommend1과 recommend2 응답에서는 질문에 대한 응답 이후 사용자가 추가적으로 궁금해할 수 있는 부분에 대한 간략한 질문이어야 합니다.
				
				응답은 반드시 응답은 반드시 다음 JSON 형식으로 응답을 제공해주세요. {"answer": 질문에 대한 한글 응답, "recommend1": 질문에 대한 응답 이후 궁금할 내용 1, "recommend2": 질문에 대한 응답 이후 궁금할 내용 2}`,
			},
			{Role: "user", Content: fmt.Sprintf(`{user_query: %s,\nrelevant_information: %s,\nuser_context: %s} 응답은 반드시 다음 JSON 형식으로 응답을 제공해주세요. {"answer": 질문에 대한 한글 응답, "recommend1": 질문에 대한 응답 이후 궁금할 내용 1, "recommend2": 질문에 대한 응답 이후 궁금할 내용 2}`, query, relevant_information, user_context)},
		},
		MaxTokens:   2000,
		Temperature: 0.7,
	}

	payload, err := json.Marshal(requestBody)
	if err != nil {
		return "", fmt.Errorf("error encoding GPT request: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("error creating GPT request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+openAIKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("error sending GPT request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := ioutil.ReadAll(resp.Body)
		return "", fmt.Errorf("GPT API error: %s", body)
	}

	var gptResponse struct {
		Choices []struct {
			Message GPTMessage `json:"message"`
		} `json:"choices"`
	}
	err = json.NewDecoder(resp.Body).Decode(&gptResponse)
	if err != nil {
		return "", fmt.Errorf("error decoding GPT response: %w", err)
	}

	if len(gptResponse.Choices) == 0 {
		return "", fmt.Errorf("no GPT response received")
	}

	return gptResponse.Choices[0].Message.Content, nil
}
