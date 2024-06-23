package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"sync"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"

	"cloud.google.com/go/logging"
)

// Client represents a single connection from a client
type Client struct {
	ID       string
	Conn     *websocket.Conn
	Position string // Current cell position
}

// Session represents a global session for all clients
type Session struct {
	Clients map[string]*Client
	Mutex   sync.Mutex
}

type EditData struct {
	ClientID string `json:"clientId"`
	Position string `json:"position"`
	Value    string `json:"value"`
}

var globalSession = &Session{
	Clients: make(map[string]*Client),
}

var (
	// Logger is the global logger for the application.
	Logger *logging.Logger
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// InitLogger initializes the global logger
func InitLogger(projectID, logName string) {
	ctx := context.Background()

	// Creates a client.
	client, err := logging.NewClient(ctx, projectID)
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}
	// Sets the global Logger to be used throughout the application.
	Logger = client.Logger(logName)
}

func Log(message string) {
	Logger.Log(logging.Entry{Payload: message})
}

// Must be called to properly flush the log entries.
func Close() {
	if Logger != nil {
		Logger.Flush()
	}
}

func main() {
	// Initialize the logger
	credentialPath := "./cmd/tabluprint-242d321299b4.json"
	os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", credentialPath)

	InitLogger("tabluprint", "tabluprint")
	r := mux.NewRouter()
	r.Use(corsMiddleware)

	r.HandleFunc("/init", initHandler).Methods("POST")
	r.HandleFunc("/updateSelection", updateSelectionHandler).Methods("POST", "OPTIONS")
	r.HandleFunc("/ws/{clientId}", wsHandler)
	http.Handle("/", r)
	Log("Server started on :4949")
	log.Fatal(http.ListenAndServe(":4949", nil))
}

// RegisterClient registers a new client to the global session
func RegisterClient(session *Session, clientID string, conn *websocket.Conn) {
	session.Mutex.Lock()
	defer session.Mutex.Unlock()
	session.Clients[clientID] = &Client{ID: clientID, Conn: conn, Position: ""}
}

// UpdateClientPosition updates the position of a client in the global session
func UpdateClientPosition(session *Session, clientID, position string) {
	session.Mutex.Lock()
	defer session.Mutex.Unlock()
	if client, ok := session.Clients[clientID]; ok {
		client.Position = position
	}
}

// InitResponse represents the response from the init API
type InitResponse struct {
	ClientID string `json:"clientId"`
}

// UpdateSelectionRequest represents the request for updating selection
type UpdateSelectionRequest struct {
	ClientID string `json:"clientId"`
	Position string `json:"position"`
}

func initHandler(w http.ResponseWriter, r *http.Request) {
	clientID := uuid.New().String()
	Log("[RTCS] /init for client " + clientID)
	response := InitResponse{ClientID: clientID}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func updateSelectionHandler(w http.ResponseWriter, r *http.Request) {
	var req UpdateSelectionRequest
	body, _ := io.ReadAll(r.Body)
	err := json.Unmarshal(body, &req)
	Log("[RTCS] /updateSelection for client " + req.ClientID + "; position: %s" + req.Position)

	if err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	UpdateClientPosition(globalSession, req.ClientID, req.Position)
	broadcastUpdate(globalSession)

	w.WriteHeader(http.StatusOK)
}

func broadcastUpdate(session *Session) {
	session.Mutex.Lock()
	defer session.Mutex.Unlock()
	for _, client := range session.Clients {
		err := client.Conn.WriteJSON(session.Clients)
		Log("[RTCS] Broadcast update for client" + client.ID)

		if err != nil {
			Log("[RTCS] Broadcast update for client" + client.ID + "-err:" + err.Error())
			client.Conn.Close()
			delete(session.Clients, client.ID)
		}
	}
}

func wsHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	clientID := vars["clientId"]

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		Log("[RTCS] /ws/" + clientID + "(WebSocket upgrade error:)" + err.Error())
		return
	}

	Log("[RTCS] /ws/" + clientID + "(WebSocket established)")
	client := &Client{ID: clientID, Conn: conn}

	RegisterClient(globalSession, clientID, conn)
	handleClientMessages(client, globalSession)
}

func handleClientMessages(client *Client, session *Session) {
	defer client.Conn.Close()
	for {
		_, msg, err := client.Conn.ReadMessage()
		if err != nil {
			Log("Error reading message:" + err.Error())
			delete(session.Clients, client.ID)
			break
		}

		var edit EditData
		if err := json.Unmarshal(msg, &edit); err == nil {
			broadcastEdit(session, edit)
		} else {
			Log("Error unmarshaling edit data:" + err.Error())
		}
	}
}

func broadcastEdit(session *Session, edit EditData) {
	session.Mutex.Lock()
	defer session.Mutex.Unlock()
	for _, otherClient := range session.Clients {
		if otherClient.ID != edit.ClientID {
			err := otherClient.Conn.WriteJSON(edit)
			if err != nil {
				Log("Error sending edit to client " + otherClient.ID + "-error:" + err.Error())
				otherClient.Conn.Close()
				delete(session.Clients, otherClient.ID)
			}
		}
	}
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS, PUT, DELETE")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}
