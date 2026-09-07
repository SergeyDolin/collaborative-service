package middlewares

import (
	"collaborative/internal/auth"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestRequestLogging(t *testing.T) {
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
		status  int
		level   zapcore.Level
	}{
		{"empty", func(w http.ResponseWriter, r *http.Request) {}, 200, zap.DebugLevel},
		{"implicit", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) }, 200, zap.DebugLevel},
		{"first status wins", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(201); w.WriteHeader(500) }, 201, zap.DebugLevel},
		{"client error", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) }, 401, zap.WarnLevel},
		{"server error", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }, 503, zap.ErrorLevel},
		{"panic", func(w http.ResponseWriter, r *http.Request) { panic("test panic") }, 500, zap.ErrorLevel},
	} {
		t.Run(tc.name, func(t *testing.T) {
			core, logs := observer.New(zap.DebugLevel)
			rec := httptest.NewRecorder()
			LogMiddleware(zap.New(core).Sugar())(tc.handler).ServeHTTP(rec, httptest.NewRequest("GET", "/api/stats?token=secret-query", nil))
			entries := logs.FilterMessage("HTTP request").All()
			if len(entries) != 1 {
				t.Fatalf("got %d request logs", len(entries))
			}
			e := entries[0]
			if rec.Code != tc.status || e.ContextMap()["status"] != int64(tc.status) || e.Level != tc.level {
				t.Fatalf("response=%d log=%+v fields=%v", rec.Code, e.Entry, e.ContextMap())
			}
			if strings.Contains(fmt.Sprint(logs.All()), "secret-query") {
				t.Fatal("query leaked into log")
			}
		})
	}
}

func TestAuthDoesNotLogTokens(t *testing.T) {
	service := auth.NewJWTService("test-key", 1)
	token, err := service.GenerateToken("alice")
	if err != nil {
		t.Fatal(err)
	}
	for _, level := range []zapcore.Level{zap.DebugLevel, zap.InfoLevel} {
		core, logs := observer.New(level)
		logger := zap.New(core).Sugar()
		h := LogMiddleware(logger)(AuthMiddleware(service, logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if user, ok := GetUserFromContext(r.Context()); !ok || user != "alice" {
				t.Error("missing authenticated user")
			}
			w.WriteHeader(http.StatusNoContent)
		})))
		r := httptest.NewRequest("GET", "/api/profile", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status: %d", rec.Code)
		}
		if strings.Contains(fmt.Sprint(logs.All()), token) {
			t.Fatal("token leaked into logs")
		}
		if level == zap.InfoLevel && logs.Len() != 0 {
			t.Fatal("routine request produced info logs")
		}
	}
}
