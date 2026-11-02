package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Task struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Done      bool      `json:"done"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type TaskRepo interface {
	Create(title string) (Task, error)
	Get(id string) (Task, bool)
	List(done bool) []Task
	SetDone(id string, done bool) (Task, error)
}

type Clock interface{ Now() time.Time }

var (
	ErrNotFound     = errors.New("task not found")
	ErrInvalidTitle = errors.New("invalid title")
)

type inMemoryTaskRepo struct {
	mu    sync.RWMutex
	clock Clock
	seq   uint64
	tasks map[string]Task
}

func NewInMemoryTaskRepo(clock Clock) TaskRepo {
	return &inMemoryTaskRepo{
		clock: clock,
		tasks: make(map[string]Task),
	}
}
func (r *inMemoryTaskRepo) Create(title string) (Task, error) {
	title = strings.TrimSpace(title)

	if title == "" {
		return Task{}, ErrInvalidTitle
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	var id string

	for {
		r.seq++
		id = strconv.FormatUint(r.seq, 10)

		if _, exists := r.tasks[id]; !exists {
			break
		}
	}

	task := Task{
		ID:        id,
		Title:     title,
		Done:      false,
		UpdatedAt: r.clock.Now(),
	}

	r.tasks[id] = task

	return task, nil
}

func (r *inMemoryTaskRepo) Get(id string) (Task, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	task, exists := r.tasks[id]

	if !exists {
		return Task{}, false
	}

	return task, true
}

func (r *inMemoryTaskRepo) List(done bool) []Task {
	r.mu.RLock()
	defer r.mu.RUnlock()

	tasks := make([]Task, 0)

	for _, task := range r.tasks {
		if task.Done == done {
			tasks = append(tasks, task)
		}
	}

	return tasks
}

func (r *inMemoryTaskRepo) SetDone(id string, done bool) (Task, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	task, exists := r.tasks[id]

	if !exists {
		return Task{}, ErrNotFound
	}

	if task.Done != done {
		task.Done = done
		task.UpdatedAt = r.clock.Now()

		r.tasks[id] = task
	}

	return task, nil
}

type httpHandler struct {
	repo TaskRepo
}

func NewHTTPHandler(repo TaskRepo) http.Handler {
	return &httpHandler{
		repo: repo,
	}
}

func (h *httpHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	if path == "/tasks" {
		switch r.Method {
		case http.MethodPost:
			h.handleCreate(w, r)

		case http.MethodGet:
			h.handleList(w, r)

		default:
			w.Header().Set("Allow", "GET, POST")
			writeJSON(w, http.StatusMethodNotAllowed,
				map[string]string{"error": "method not allowed"})
		}

		return
	}

	if strings.HasPrefix(path, "/tasks/") {
		id := strings.TrimPrefix(path, "/tasks/")

		if id == "" || strings.Contains(id, "/") {
			writeJSON(w, http.StatusNotFound,
				map[string]string{"error": "not found"})
			return
		}

		switch r.Method {
		case http.MethodGet:
			h.handleGet(w, r, id)

		case http.MethodPatch:
			h.handlePatch(w, r, id)

		default:
			w.Header().Set("Allow", "GET, PATCH")
			writeJSON(w, http.StatusMethodNotAllowed,
				map[string]string{"error": "method not allowed"})
		}

		return
	}

	writeJSON(w, http.StatusNotFound,
		map[string]string{"error": "not found"})
}

func (h *httpHandler) handleCreate(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Title *string `json:"title"`
	}

	if err := decodeStrictJSON(r.Body, &request); err != nil || request.Title == nil {
		writeJSON(w, http.StatusBadRequest,
			map[string]string{"error": "invalid request"})
		return
	}

	task, err := h.repo.Create(*request.Title)

	if err != nil {
		if errors.Is(err, ErrInvalidTitle) {
			writeJSON(w, http.StatusBadRequest,
				map[string]string{"error": "invalid title"})
		} else {
			writeJSON(w, http.StatusInternalServerError,
				map[string]string{"error": "internal server error"})
		}

		return
	}

	writeJSON(w, http.StatusCreated, task)
}

func (h *httpHandler) handleGet(w http.ResponseWriter, _ *http.Request, id string) {
	task, exists := h.repo.Get(id)

	if !exists {
		writeJSON(w, http.StatusNotFound,
			map[string]string{"error": "task not found"})
		return
	}

	writeJSON(w, http.StatusOK, task)
}

func (h *httpHandler) handleList(w http.ResponseWriter, r *http.Request) {
	done := false

	values, exists := r.URL.Query()["done"]

	if exists {
		if len(values) != 1 {
			writeJSON(w, http.StatusBadRequest,
				map[string]string{"error": "invalid done parameter"})
			return
		}

		switch values[0] {
		case "true":
			done = true

		case "false":
			done = false

		default:
			writeJSON(w, http.StatusBadRequest,
				map[string]string{"error": "invalid done parameter"})
			return
		}
	}

	source := h.repo.List(done)

	tasks := make([]Task, len(source))
	copy(tasks, source)

	sort.Slice(tasks, func(i, j int) bool {
		if tasks[i].UpdatedAt.Equal(tasks[j].UpdatedAt) {
			return tasks[i].ID < tasks[j].ID
		}

		return tasks[i].UpdatedAt.After(tasks[j].UpdatedAt)
	})

	writeJSON(w, http.StatusOK, tasks)
}

func (h *httpHandler) handlePatch(w http.ResponseWriter, r *http.Request, id string) {
	var request struct {
		Done *bool `json:"done"`
	}

	if err := decodeStrictJSON(r.Body, &request); err != nil || request.Done == nil {
		writeJSON(w, http.StatusBadRequest,
			map[string]string{"error": "invalid request"})
		return
	}

	task, err := h.repo.SetDone(id, *request.Done)

	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeJSON(w, http.StatusNotFound,
				map[string]string{"error": "task not found"})
		} else {
			writeJSON(w, http.StatusInternalServerError,
				map[string]string{"error": "internal server error"})
		}

		return
	}

	writeJSON(w, http.StatusOK, task)
}

func decodeStrictJSON(r io.Reader, v any) error {
	decoder := json.NewDecoder(r)

	decoder.DisallowUnknownFields()

	if err := decoder.Decode(v); err != nil {
		return err
	}

	var extra any

	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values")
		}

		return err
	}

	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(v)
}
