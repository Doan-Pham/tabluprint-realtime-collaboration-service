package main

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"sync"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
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

var globalSession = &Session{
	Clients: make(map[string]*Client),
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func main() {
	r := mux.NewRouter()
	r.HandleFunc("/init", initHandler).Methods("POST")
	r.HandleFunc("/updateSelection", updateSelectionHandler).Methods("POST")
	r.HandleFunc("/ws/{clientId}", wsHandler)
	http.Handle("/", r)
	log.Println("Server started on :4949")
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
	response := InitResponse{ClientID: clientID}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func updateSelectionHandler(w http.ResponseWriter, r *http.Request) {
	var req UpdateSelectionRequest
	body, _ := io.ReadAll(r.Body)
	json.Unmarshal(body, &req)

	UpdateClientPosition(globalSession, req.ClientID, req.Position)
	broadcastUpdate(globalSession)

	w.WriteHeader(http.StatusOK)
}

func broadcastUpdate(session *Session) {
	session.Mutex.Lock()
	defer session.Mutex.Unlock()
	for _, client := range session.Clients {
		err := client.Conn.WriteJSON(session.Clients)
		if err != nil {
			log.Printf("[RTCS] Broadcast update for client : - error: %v", err)
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
		log.Println("[RTCS] /ws/"+clientID+"(WebSocket upgrade error:)", err)
		return
	}

	log.Println("[RTCS] /ws/" + clientID + "(WebSocket established)")
	RegisterClient(globalSession, clientID, conn)
}
