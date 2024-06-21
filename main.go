package main

import (
	"errors"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const LOBBY_ID_LENGTH = 6

type message struct {
	Id            int    `json:"id"`
	LobbyId       string `json:"lobby_id"`
	SenderName    string `json:"sender_name"`
	MessageString string `json:"message_string"`
	Timestamp     int64  `json:"timestamp"`
}

type sender struct {
	Username string `json:"username"`
	LobbyId  string `json:"lobby_id"`
	IsTyping bool   `json:"is_typing"`
}

type lobbyData struct {
	Messages []message `json:"messages"`
	Senders  []sender  `json:"senders"`
	Id       string    `json:"id"`
}

func main() {
	router := gin.Default()

	router.GET("/lobby/:id", fetchLobbyData)
	router.POST("/postMessage", postMessage)
	router.GET("/lobbyExists/:id", lobbyExists)
	router.POST("/createLobby", createLobby)
	router.POST("/enterLobby", enterLobby)
	router.POST("/updateTyping", updateTyping)

	router.Run("localhost:8080")
}

var messages = []message{}
var senders = []sender{}
var lobbies = map[string]bool{}
var msgMutex sync.Mutex
var nextMessageId = 1
var lobbyMutex sync.Mutex
var senderMutex sync.Mutex

func doesLobbyExist(id string) bool {
	_, ok := lobbies[id]
	return ok
}

func constructLobbyData(id string) (lobbyData, error) {
	if !doesLobbyExist(id) {
		return lobbyData{}, errors.New("lobby not found")
	}

	var includedMsgs = []message{}
	var includedSenders = []sender{}

	for _, m := range messages {
		if m.LobbyId == id {
			includedMsgs = append(includedMsgs, m)
		}
	}

	for _, s := range senders {
		if s.LobbyId == id {
			includedSenders = append(includedSenders, s)
		}
	}

	return lobbyData{Messages: includedMsgs, Senders: includedSenders, Id: id}, nil
}

func fetchLobbyData(c *gin.Context) {
	// we can use... the :id thing to do this
	id := c.Param("id")
	result, err := constructLobbyData(id)

	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"message": err.Error()})
		return
	}

	c.IndentedJSON(http.StatusOK, result)
}

func postMessage(c *gin.Context) {
	var msg message

	if err := c.BindJSON(&msg); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "Message was invalid!"})
		return
	}

	if !doesLobbyExist(msg.LobbyId) {
		c.JSON(http.StatusBadRequest, gin.H{"message": "Message did not belong to a lobby!"})
		return
	}

	msgMutex.Lock()
	msg.Id = nextMessageId
	msg.Timestamp = time.Now().Unix()
	nextMessageId++
	messages = append(messages, msg)

	lobbyData, err := constructLobbyData(msg.LobbyId)

	defer msgMutex.Unlock()

	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	c.IndentedJSON(http.StatusCreated, lobbyData)
}

func createLobby(c *gin.Context) {
	lobbyMutex.Lock()
	var id string
	id = randSeq(LOBBY_ID_LENGTH)
	attempts := 10
	for doesLobbyExist(id) && attempts > 0 {
		id = randSeq(LOBBY_ID_LENGTH)
		attempts -= 1
	}

	if attempts == 0 {
		defer lobbyMutex.Unlock()
		c.JSON(http.StatusInternalServerError, gin.H{"message": "Failed to generate unique id string!"})
		return
	}

	// now we have our id
	lobbies[id] = true

	defer lobbyMutex.Unlock()

	c.JSON(http.StatusCreated, id)
}

func enterLobby(c *gin.Context) {
	var enterReq sender

	if err := c.BindJSON(&enterReq); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "Could not parse request!"})
		return
	}

	if !doesLobbyExist(enterReq.LobbyId) {
		c.JSON(http.StatusBadRequest, gin.H{"message": "Lobby does not exist!"})
		return
	}

	// we really want to check if this person already exists...

	senderMutex.Lock()

	add := true

	for _, s := range senders {
		if s.LobbyId == enterReq.LobbyId && s.Username == enterReq.Username {
			add = false
		}
	}

	if add {
		enterReq.IsTyping = false
		senders = append(senders, enterReq)
	}

	defer senderMutex.Unlock()

	result, err := constructLobbyData(enterReq.LobbyId)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	c.IndentedJSON(http.StatusOK, result)
}

func lobbyExists(c *gin.Context) {
	id := c.Param("id")

	if !doesLobbyExist(id) {
		c.IndentedJSON(http.StatusOK, false)
		return
	}

	c.IndentedJSON(http.StatusOK, true)
}

func updateTyping(c *gin.Context) {
	var request sender

	if err := c.BindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "Failed to parse request body!"})
		return
	}

	senderMutex.Lock()

	found := false

	for i := 0; i < len(senders); {
		if senders[i].Username == request.Username && senders[i].LobbyId == request.LobbyId {
			senders[i].IsTyping = request.IsTyping
			found = true
			break
		}
	}

	defer senderMutex.Unlock()

	if found {
		c.JSON(http.StatusOK, struct{}{})
	} else {
		c.JSON(http.StatusNotFound, gin.H{"message": "Could not find sender to update!"})
	}
}

var letters = []rune("abcdefghijklmnopqrstuvwxyz")

func randSeq(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}
