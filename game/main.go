package main

import (
	"bufio"
	"database/sql"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"

	_ "github.com/lib/pq"
)

var (
	leaderboardMutex sync.Mutex
	leaderboard      map[string]Player
	db               *sql.DB
	lm               LeaderboardManager
)

type Player struct {
	UserName string
	Age      int
	Score    int
}

type GameRequest struct {
	Players   []Player
	numRounds int
}

// LeaderboardManager struct to manage the leaderboard
type LeaderboardManager struct {
	scores map[string]Player
	mutex  sync.Mutex
}

func clearBuffer() {
	var discard string
	fmt.Scanln(&discard)
}

// takeShot simulates a shooting action in the game, randomly returning 0 (miss) or 3 (hit).
func takeShot() int {
	if rand.Intn(2) == 0 {
		return 0 // Missed shot
	}
	return 3 // Successful shot
}

func initializePlayers(numPlayers int) []Player {
	players := make([]Player, numPlayers)
	reader := bufio.NewReader(os.Stdin)

	for i := 0; i < numPlayers; i++ {
		var name string
		var age int

		for name == "" {
			fmt.Printf("Enter the name of Player %d:\n", i+1)
			nameInput, _ := reader.ReadString('\n')
			name = strings.TrimSpace(nameInput)
			if name == "" {
				fmt.Println("Name cannot be empty. Please enter a valid name.")
			}
		}

		for age <= 0 {
			fmt.Printf("Enter the Age of Player %d:\n", i+1)
			fmt.Scanln(&age)
			if age <= 0 || age >= 120 {
				fmt.Print("Error Invalid age: please enter valid age between 0 - 120\n")
				clearBuffer()
			}
		}
		players[i] = Player{UserName: name, Age: age}
	}
	return players // Return the slice of initialized players
}

func simulateGame(gameReq GameRequest) []Player {
	for round := 1; round <= gameReq.numRounds; round++ {
		for miniRound := 1; miniRound <= 3; miniRound++ {
			for shot := 1; shot <= 3; shot++ {
				for i := range gameReq.Players {
					points := takeShot()
					gameReq.Players[i].Score += points
				}
			}
		}
	}
	return gameReq.Players
}

func playGame(gameReq GameRequest, doneChan chan<- []Player) {
	players := simulateGame(gameReq)
	calculateScores(players)
	doneChan <- players
}

func calculateScores(players []Player) {

	sort.Slice(players, func(i, j int) bool {
		return players[i].Score > players[j].Score
	})
}

func (lm *LeaderboardManager) FetchLeaderboardFromDB(db *sql.DB) ([]Player, error) {
	rows, err := db.Query("SELECT username, COALESCE(age, 0), score FROM leaderboard ORDER BY score DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var leaderboard []Player
	for rows.Next() {
		var p Player
		if err := rows.Scan(&p.UserName, &p.Age, &p.Score); err != nil {
			return nil, err
		}
		leaderboard = append(leaderboard, p)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return leaderboard, nil
}

// UpdateLeaderboard updates the leaderboard with a slice of players
func (lm *LeaderboardManager) UpdateLeaderboard(db *sql.DB, players []Player) error {
	lm.mutex.Lock()
	defer lm.mutex.Unlock()

	for _, p := range players {
		// Update in-memory leaderboard
		lm.scores[p.UserName] = p

		// Update database leaderboard
		_, err := db.Exec(`
    INSERT INTO leaderboard (username, age, score)
    VALUES ($1, $2, $3)
    ON CONFLICT (username)
    DO UPDATE SET score = EXCLUDED.score, age = EXCLUDED.age`,
			p.UserName, p.Age, p.Score)
		if err != nil {
			return err
		}
	}
	return nil
}

func leaderboardHandler(lm *LeaderboardManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {

		// Fetch the sorted leaderboard from the database
		sortedLeaderboard, err := lm.FetchLeaderboardFromDB(db)
		if err != nil {
			http.Error(w, "Server error", http.StatusInternalServerError)
			log.Println("Fetching leaderboard from DB:", err)
			return
		}

		html := "<!DOCTYPE html><html><head><title>Leaderboard</title></head><body>"
		html += "<h1>Leaderboard</h1>"
		html += "<table border='1'><tr><th>UserName</th><th>Age</th><th>Score</th></tr>"
		for _, player := range sortedLeaderboard {
			html += fmt.Sprintf("<tr><td>%s</td><td>%d</td><td>%d</td></tr>", player.UserName, player.Age, player.Score)
		}
		html += "</table></body></html>"

		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, html)

	}
}

// Function to connect to the database
func ConnectToDatabase() (*sql.DB, error) {
	const (
		host     = "dishdb-1.czm0a6c2szh6.us-east-2.rds.amazonaws.com"
		port     = 5432
		user     = "postgres"
		password = "Dragon123"
		dbname   = "dishdb"
	)

	psqlInfo := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s",
		host, port, user, password, dbname)

	db, err := sql.Open("postgres", psqlInfo)
	if err != nil {
		panic(err)
	}

	err = db.Ping()
	if err != nil {
		panic(err)
	}

	fmt.Println("Successfully connected!")
	return db, nil
}

func main() {

	var err error
	db, err = ConnectToDatabase()
	if err != nil {
		log.Fatalf("Could not connect to the database: %v", err)
	}
	defer db.Close()

	var numPlayers, numRounds int
	fmt.Println("Enter the number of Players:")
	fmt.Scanln(&numPlayers)
	fmt.Println("Enter the number of Rounds:")
	fmt.Scanln(&numRounds)

	players := initializePlayers(numPlayers)

	gameReg := GameRequest{Players: players, numRounds: numRounds}

	doneChan := make(chan []Player)
	go playGame(gameReg, doneChan)

	playersResults := <-doneChan
	// Initialize the LeaderboardManager with an empty map
	lm := LeaderboardManager{scores: make(map[string]Player)}

	// Update the leaderboard in the database
	if err := lm.UpdateLeaderboard(db, playersResults); err != nil {
		log.Fatalf("Could not update leaderboard: %v", err)
	}

	http.HandleFunc("/leaderboard", leaderboardHandler(&lm))

	fmt.Println("Starting server on http://localhost:8080/")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
