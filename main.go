package main

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/go-sql-driver/mysql"
)

const LOBBY_ID_LENGTH = 6
const MAX_MSG_LEN = 512
const MAX_USERNAME_LEN = 32

type message struct {
	Id            int    `json:"messageId"`
	LobbyId       string `json:"lobbyId"`
	SenderName    string `json:"senderName"`
	MessageString string `json:"messageContent"`
	Timestamp     int64  `json:"timestamp"`
}

type sender struct {
	Username string `json:"name"`
	LobbyId  string `json:"lobbyId"`
	IsTyping bool   `json:"isTyping"`
}

type lobbyData struct {
	Messages []message `json:"messages"`
	Senders  []sender  `json:"senders"`
	Id       string    `json:"id"`
}

var db *sql.DB

func main() {
	gin.SetMode(gin.ReleaseMode);

	cfg := mysql.Config{
		User:   os.Getenv("DBUSER"),
		Passwd: os.Getenv("DBPASS"),
		Net:    "tcp",
		Addr:   os.Getenv("DBADDR"),
		DBName: "chat",
		AllowNativePasswords: true,
	}

	var dberr error
	db, dberr = sql.Open("mysql", cfg.FormatDSN())
	if dberr != nil {
		log.Fatal(dberr)
	}

	pingErr := db.Ping()
	if pingErr != nil {
		log.Fatal(pingErr)
	}
	fmt.Println("Connected to database!")

	router := gin.Default()

	router.Use(cors.Default())

	router.GET("/lobby/:id", fetchLobbyData)
	router.POST("/postMessage", postMessage)
	router.GET("/lobbyExists/:id", lobbyExists)
	router.POST("/createLobby", createLobby)
	router.POST("/enterLobby", enterLobby)
	router.POST("/updateTyping", updateTyping)

	var err error

	if os.Getenv("USETLS") == "true" {
		err = router.RunTLS(":8443", "/etc/letsencrypt/live/daily-planners.com/fullchain.pem", "/etc/letsencrypt/live/daily-planners.com/privkey.pem")
	} else {
		err = router.Run(":8080")
	}

	if err != nil {
		log.Fatal("unable to start server :", err)
	}
}

var msgMutex sync.Mutex

var lobbyMutex sync.Mutex
var senderMutex sync.Mutex

func doesLobbyExist(id string) bool {
	var val int

	row := db.QueryRow("SELECT COUNT(*) FROM lobbies WHERE id = ?", id)

	if err := row.Scan(&val); err != nil {
		return false
	}

	if val == 0 {
		return false
	}

	return true
}

func getMessagesFor(lobbyId string) ([]message, error) {
	messages := []message{}

	rows, err := db.Query("SELECT * FROM message WHERE lobbyId = ?", lobbyId)
	if err != nil {
		return nil, err
	}

	defer rows.Close()

	// Loop through rows, using Scan to assign column data to struct fields.
	for rows.Next() {
		var msg message
		if err := rows.Scan(&msg.Id, &msg.LobbyId, &msg.SenderName, &msg.MessageString, &msg.Timestamp); err != nil {
			return nil, fmt.Errorf("get messages for %q: %v", lobbyId, err)
		}
		messages = append(messages, msg)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("get messages for %q: %v", lobbyId, err)
	}

	return messages, nil
}

func getSendersFor(lobbyId string) ([]sender, error) {
	senders := []sender{}

	rows, err := db.Query("SELECT * FROM sender WHERE lobbyId = ?", lobbyId)
	if err != nil {
		return nil, err
	}

	defer rows.Close()

	// Loop through rows, using Scan to assign column data to struct fields.
	for rows.Next() {
		var sndr sender
		if err := rows.Scan(&sndr.Username, &sndr.LobbyId, &sndr.IsTyping); err != nil {
			return nil, fmt.Errorf("get senders for %q: %v", lobbyId, err)
		}
		senders = append(senders, sndr)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("get senders for %q: %v", lobbyId, err)
	}

	return senders, nil
}

func constructLobbyData(id string) (lobbyData, error) {
	if !doesLobbyExist(id) {
		return lobbyData{}, errors.New("lobby not found")
	}

	includedMsgs, msgerr := getMessagesFor(id)
	includedSenders, sendererr := getSendersFor(id)

	if msgerr != nil {
		return lobbyData{}, msgerr
	}

	if sendererr != nil {
		return lobbyData{}, sendererr
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

func appendMessage(msg message) error {
	msg.Timestamp = time.Now().Unix()

	_, err := db.Exec("INSERT INTO message (lobbyId, senderName, messageString, timestamp) VALUES (?, ?, ?, ?)", msg.LobbyId, msg.SenderName, msg.MessageString, msg.Timestamp)
	if err != nil {
		return fmt.Errorf("addAlbum: %v", err)
	}
	return nil
}

func postMessage(c *gin.Context) {
	var msg message

	if err := c.BindJSON(&msg); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "Message was invalid!"})
		return
	}

	if len(msg.MessageString) > MAX_MSG_LEN {
		c.JSON(http.StatusBadRequest, gin.H{"message": "Message is too long!"})
		return
	}

	if !doesLobbyExist(msg.LobbyId) {
		c.JSON(http.StatusBadRequest, gin.H{"message": "Message did not belong to a lobby!"})
		return
	}

	msgMutex.Lock()

	insertErr := appendMessage(msg)
	if insertErr != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": insertErr.Error()})
	}

	defer msgMutex.Unlock()

	lobbyData, err := constructLobbyData(msg.LobbyId)

	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	c.IndentedJSON(http.StatusCreated, lobbyData)
}

func insertLobby(id string) error {
	_, err := db.Exec("INSERT INTO lobbies (id) VALUES (?)", id)
	if err != nil {
		return fmt.Errorf("insert lobby: %v", err)
	}
	return nil
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

	defer lobbyMutex.Unlock()

	err := insertLobby(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
	}

	c.JSON(http.StatusCreated, id)
}

func senderExists(enterReq sender) bool {
	var val int

	row := db.QueryRow("SELECT COUNT(*) FROM sender WHERE lobbyId = ? AND name = ?", enterReq.LobbyId, enterReq.Username)
	if err := row.Scan(&val); err != nil {
		return false
	}

	if val == 0 {
		return false
	}

	return true
}

func addSender(enterReq sender) error {
	if senderExists(enterReq) {
		return nil
	}

	enterReq.IsTyping = false

	_, err := db.Exec("INSERT INTO sender (name, lobbyId, isTyping) VALUES (?, ?, ?)", enterReq.Username, enterReq.LobbyId, enterReq.IsTyping)
	if err != nil {
		return fmt.Errorf("insert lobby: %v", err)
	}

	return nil
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

	if len(enterReq.Username) > MAX_USERNAME_LEN {
		c.JSON(http.StatusBadRequest, gin.H{"message": "Username is too long!"})
		return
	}

	senderMutex.Lock()
	addErr := addSender(enterReq)
	if addErr != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": addErr.Error()})
		return
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

func setTyping(request sender) error {
	fmt.Printf("updating sender: %v", request)
	_, err := db.Exec("UPDATE sender SET isTyping = ? WHERE lobbyId = ? AND name = ?", request.IsTyping, request.LobbyId, request.Username)
	return err
}

func updateTyping(c *gin.Context) {
	var request sender

	if err := c.BindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "Failed to parse request body!"})
		return
	}

	senderMutex.Lock()

	err := setTyping(request)

	defer senderMutex.Unlock()

	if err == nil {
		c.JSON(http.StatusOK, struct{}{})
	} else {
		c.JSON(http.StatusNotFound, gin.H{"message": err.Error()})
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
