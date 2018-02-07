package main

import (
	"crypto/sha256"
	"encoding/gob"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/davecgh/go-spew/spew"
	"github.com/gobuffalo/plush"
	"github.com/gorilla/mux"
	"github.com/joho/godotenv"
)

var todoFile = "./todochainz.gob"

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

type TodoDetail struct {
	OriginalTodo Todo   `json:"original_todo,omitempty"`
	Updates      []Todo `json:"updates,omitempty"`
}

type TodoMessage struct {
	Title    string `json:"title"`
	Notes    string `json:"notes"`
	OrigHash string `json:"orig_hash,omitempty"`
}

var TodoChain []Todo

func calculateHash(todo Todo) (string, error) {
	hashText := string(todo.Index) + todo.Title + todo.Notes + todo.CreateStamp.String() + todo.PrevHash
	h := sha256.New()
	_, err := h.Write([]byte(hashText))
	if err != nil {
		return "", err
	}
	hashed := h.Sum(nil)
	return hex.EncodeToString(hashed), nil
}

func generateBlock(oldTodo Todo, message TodoMessage) (Todo, error) {
	var newTodo Todo

	t := time.Now()

	newTodo.Index = oldTodo.Index + 1
	newTodo.CreateStamp = t
	newTodo.Title = message.Title
	newTodo.Notes = message.Notes
	newTodo.PrevHash = oldTodo.Hash
	hash, err := calculateHash(newTodo)
	if err != nil {
		return newTodo, err
	}
	newTodo.Hash = hash

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

	hash, err := calculateHash(newTodo)
	if err != nil || hash != newTodo.Hash {
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

func loadTodoDetail(hash string, chain []Todo) (*TodoDetail, error) {
	var todoDetail TodoDetail
	for _, td := range chain {
		if td.Hash == hash {
			todoDetail.OriginalTodo = td
		}
		//TODO: order the updates list by date ascending
		if td.OrigHash == hash {
			todoDetail.Updates = append(todoDetail.Updates, td)
		}
	}

	var err error
	if todoDetail.OriginalTodo.Hash == "" {
		err = errors.New("original todo not found")
		return nil, err
	}

	return &todoDetail, nil
}

func replaceChain(newTodos []Todo) {
	if len(newTodos) > len(TodoChain) {
		TodoChain = newTodos
	}
	err := saveFile(TodoChain)
	if err != nil {
		log.Fatal(err)
	}
}

func saveFile(object interface{}) error {
	file, err := os.Create(todoFile)
	if err == nil {
		encoder := gob.NewEncoder(file)
		encoder.Encode(object)
	}
	file.Close()
	return err
}

func loadFile(object interface{}) error {
	file, err := os.Open(todoFile)
	if err == nil {
		decoder := gob.NewDecoder(file)
		err = decoder.Decode(object)
	}
	file.Close()
	return err
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
	muxRouter.HandleFunc("/", handleGetIndex).Methods("GET")
	muxRouter.HandleFunc("/api/", handleGetTodoChain).Methods("GET")
	muxRouter.HandleFunc("/api/", handleCreateTodo).Methods("POST")
	muxRouter.HandleFunc("/api/{hash}", handleGetTodo).Methods("GET")
	muxRouter.HandleFunc("/api/{hash}", handleCompleteTodo).Methods("POST")
	muxRouter.HandleFunc("/api/{hash}", handleUpdateTodo).Methods("PUT")
	muxRouter.HandleFunc("/api/{hash}", handleDeleteTodo).Methods("DELETE")
	return muxRouter
}

func handleGetIndex(w http.ResponseWriter, r *http.Request) {
	ctx := plush.NewContext()
	ctx.Set("todos", TodoChain)
	file, err := ioutil.ReadFile("./templates/" + "index.html")
	if err != nil {
		log.Fatal(err)
	}
	s, err := plush.Render(string(file), ctx)
	if err != nil {
		log.Fatal(err)
	}
	w.Write([]byte(s))
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

func handleGetTodo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	vars := mux.Vars(r)
	hash := vars["hash"]

	detail, err := loadTodoDetail(hash, TodoChain)
	if err != nil {
		respondWithJSON(w, r, http.StatusNotFound, detail)
		return
	}

	respondWithJSON(w, r, http.StatusOK, detail)
	return
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

// TODO: whenever updating an existing todo make sure that
// we are using the latest values for the todo
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

// TODO: whenever updating an existing todo make sure that
// we are using the latest values for the todo
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
	completeTodo.DeleteStamp = originalTodo.DeleteStamp
	completeTodo.UpdateStamp = t
	completeTodo.CompleteStamp = t
	if isTodoValid(completeTodo, TodoChain[len(TodoChain)-1]) {
		newTodoChain := append(TodoChain, completeTodo)
		replaceChain(newTodoChain)
		spew.Dump(TodoChain)
	}

	respondWithJSON(w, r, http.StatusOK, completeTodo)
}

// TODO: whenever updating an existing todo make sure that
// we are using the latest values for the todo
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
	completeTodo.CompleteStamp = originalTodo.CompleteStamp
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
		if _, err := os.Stat(todoFile); os.IsNotExist(err) {
			t := time.Now()
			genesisTodo := Todo{
				Index:       0,
				Title:       "Genesis Todo",
				Notes:       "Genesis Notes",
				CreateStamp: t,
			}
			genesisTodo.Hash, err = calculateHash(genesisTodo)
			if err != nil {
				log.Fatal(err)
			}

			spew.Dump(genesisTodo)
			TodoChain = append(TodoChain, genesisTodo)
			replaceChain(TodoChain)
		} else {
			var todos []Todo
			err = loadFile(&todos)
			if err != nil {
				log.Fatal(err)
			}
			replaceChain(todos)
		}
	}()
	log.Fatal(run())
}
