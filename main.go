package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/joho/godotenv"

	"github.com/davecgh/go-spew/spew"

	"github.com/gorilla/mux"
)

type Todo struct {
	Index         int       `json:"index"`
	Title         string    `json:"title"`
	Notes         string    `json:"notes"`
	CreateStamp   time.Time `json:"create_stamp"`
	UpdateStamp   time.Time `json:"update_stamp,omtiempty"`
	CompleteStamp time.Time `json:"complete_stamp,omitempty"`
	DeleteStamp   time.Time `json:"deleted_stamp,omitempty"`
	Hash          string    `json:"hash"`
	OrigHash      string    `json:"orig_hash"`
	PrevHash      string    `json:"prev_hash"`
}

type TodoMessage struct {
	Title    string `json:"title"`
	Notes    string `json:"notes"`
	OrigHash string `json:"orig_hash,omitempty"`
}

var TodoChain []Todo

func calculateHash(todo Todo) string {
	hashText := string(todo.Index) + todo.Title + todo.Notes + todo.CreateStamp.String() + todo.PrevHash
	h := sha256.New()
	h.Write([]byte(hashText))
	hashed := h.Sum(nil)
	return hex.EncodeToString(hashed)
}

func generateBlock(oldTodo Todo, message TodoMessage) (Todo, error) {
	var newTodo Todo

	t := time.Now()

	newTodo.Index = oldTodo.Index + 1
	newTodo.CreateStamp = t
	newTodo.Title = message.Title
	newTodo.Notes = message.Notes
	newTodo.PrevHash = oldTodo.Hash
	newTodo.Hash = calculateHash(newTodo)

	if message.OrigHash != "" {
		newTodo.OrigHash = message.OrigHash
	}

	return newTodo, nil
}

func isTodoValid(newTodo, oldTodo Todo) bool {
	if oldTodo.Index+1 != newTodo.Index {
		return false
	}

	if oldTodo.Hash != newTodo.PrevHash {
		return false
	}

	if calculateHash(newTodo) != newTodo.Hash {
		return false
	}

	return true
}

func chainContainsOriginalTodo(todo Todo, chain []Todo) bool {
	for _, td := range chain {
		if td.Hash == todo.OrigHash {
			return true
		}
	}
	return false
}

func findTodo(hash string, chain []Todo) (*Todo, error) {
	for _, td := range chain {
		if td.Hash == hash {
			return &td, nil
		}
	}

	err := errors.New("original todo not found")
	return nil, err
}

func replaceChain(newTodos []Todo) {
	if len(newTodos) > len(TodoChain) {
		TodoChain = newTodos
	}
}

func run() error {
	mux := makeMuxRouter()
	httpAddr := os.Getenv("ADDR")
	log.Println("Listening on ", httpAddr)
	s := http.Server{
		Addr:           ":" + httpAddr,
		Handler:        mux,
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   10 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	if err := s.ListenAndServe(); err != nil {
		return err
	}

	return nil
}

func makeMuxRouter() http.Handler {
	muxRouter := mux.NewRouter()
	muxRouter.HandleFunc("/", handleGetTodoChain).Methods("GET")
	muxRouter.HandleFunc("/", handleCreateTodo).Methods("POST")
	muxRouter.HandleFunc("/{hash}", handleUpdateTodo).Methods("PUT")
	muxRouter.HandleFunc("/{hash}", handleDeleteTodo).Methods("DELETE")
	return muxRouter
}

func handleGetTodoChain(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	bytes, err := json.MarshalIndent(TodoChain, "", " ")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	io.WriteString(w, string(bytes))
}

func handleCreateTodo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	var m TodoMessage

	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&m); err != nil {
		respondWithJSON(w, r, http.StatusBadRequest, r.Body)
		return
	}
	defer r.Body.Close()

	newTodo, err := generateBlock(TodoChain[len(TodoChain)-1], m)
	if err != nil {
		respondWithJSON(w, r, http.StatusInternalServerError, m)
		return
	}

	if isTodoValid(newTodo, TodoChain[len(TodoChain)-1]) {
		newTodoChain := append(TodoChain, newTodo)
		replaceChain(newTodoChain)
		spew.Dump(TodoChain)
	}

	respondWithJSON(w, r, http.StatusCreated, newTodo)
}

func handleUpdateTodo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	vars := mux.Vars(r)
	originalHash := vars["hash"]
	var m TodoMessage
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&m); err != nil {
		respondWithJSON(w, r, http.StatusBadRequest, r.Body)
		return
	}
	defer r.Body.Close()
	m.OrigHash = originalHash

	updateTodo, err := generateBlock(TodoChain[len(TodoChain)-1], m)
	if err != nil {
		respondWithJSON(w, r, http.StatusInternalServerError, m)
		return
	}

	updateTodo.UpdateStamp = time.Now()
	if isTodoValid(updateTodo, TodoChain[len(TodoChain)-1]) {
		newTodoChain := append(TodoChain, updateTodo)
		replaceChain(newTodoChain)
		spew.Dump(TodoChain)
	}

	respondWithJSON(w, r, http.StatusOK, updateTodo)
}

func handleCompleteTodo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	vars := mux.Vars(r)
	originalHash := vars["hash"]
	originalTodo, err := findTodo(originalHash, TodoChain)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
	}

	var m TodoMessage
	m.Title = originalTodo.Title
	m.Notes = originalTodo.Notes
	m.OrigHash = originalHash

	completeTodo, err := generateBlock(TodoChain[len(TodoChain)-1], m)
	if err != nil {
		respondWithJSON(w, r, http.StatusInternalServerError, m)
		return
	}

	t := time.Now()
	completeTodo.UpdateStamp = t
	completeTodo.CompleteStamp = t
	if isTodoValid(completeTodo, TodoChain[len(TodoChain)-1]) {
		newTodoChain := append(TodoChain, completeTodo)
		replaceChain(newTodoChain)
		spew.Dump(TodoChain)
	}

	respondWithJSON(w, r, http.StatusOK, completeTodo)
}

func handleDeleteTodo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	vars := mux.Vars(r)
	originalHash := vars["hash"]
	originalTodo, err := findTodo(originalHash, TodoChain)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
	}

	var m TodoMessage
	m.Title = originalTodo.Title
	m.Notes = originalTodo.Notes
	m.OrigHash = originalHash

	completeTodo, err := generateBlock(TodoChain[len(TodoChain)-1], m)
	if err != nil {
		respondWithJSON(w, r, http.StatusInternalServerError, m)
		return
	}

	t := time.Now()
	completeTodo.UpdateStamp = t
	completeTodo.DeleteStamp = t
	if isTodoValid(completeTodo, TodoChain[len(TodoChain)-1]) {
		newTodoChain := append(TodoChain, completeTodo)
		replaceChain(newTodoChain)
		spew.Dump(TodoChain)
	}

	respondWithJSON(w, r, http.StatusOK, completeTodo)
}

func respondWithJSON(w http.ResponseWriter, r *http.Request, code int, payload interface{}) {
	response, err := json.MarshalIndent(payload, "", " ")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("HTTP 500: Internal Server` Error"))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	w.Write(response)
}

func main() {
	err := godotenv.Load()
	if err != nil {
		log.Fatal(err)
	}

	go func() {
		t := time.Now()
		genesisTodo := Todo{
			Index:       0,
			Title:       "Genesis Todo",
			Notes:       "Genesis Notes",
			CreateStamp: t,
		}
		genesisTodo.Hash = calculateHash(genesisTodo)
		spew.Dump(genesisTodo)
		TodoChain = append(TodoChain, genesisTodo)
	}()
	log.Fatal(run())
}
