package audit

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

type Auditor struct {
	db *sql.DB
}

func NewAuditor(db *sql.DB) *Auditor {
	return &Auditor{db: db}
}

type AuditEntry struct {
	Actor        string
	Action       string
	ResourceType string
	ResourceID   string
	NamespaceID  string
	BeforeValue  interface{}
	AfterValue   interface{}
	IPAddress    string
	UserAgent    string
}

func (a *Auditor) Log(entry AuditEntry) error {
	beforeJSON, _ := json.Marshal(entry.BeforeValue)
	afterJSON, _ := json.Marshal(entry.AfterValue)
	
	_, err := a.db.Exec(`
		INSERT INTO audits (
			actor, action, resource_type, resource_id, 
			namespace_id, before_value, after_value, 
			ip_address, user_agent, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		entry.Actor,
		entry.Action,
		entry.ResourceType,
		entry.ResourceID,
		sql.NullString{String: entry.NamespaceID, Valid: entry.NamespaceID != ""},
		beforeJSON,
		afterJSON,
		entry.IPAddress,
		entry.UserAgent,
		time.Now(),
	)
	
	if err != nil {
		log.Errorf("Failed to write audit log: %v", err)
		return err
	}
	
	return nil
}

func (a *Auditor) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip audit for health/metrics endpoints
		if strings.HasPrefix(r.URL.Path, "/health") || 
		   strings.HasPrefix(r.URL.Path, "/ready") || 
		   strings.HasPrefix(r.URL.Path, "/metrics") {
			next.ServeHTTP(w, r)
			return
		}
		
		// Capture request details
		actor := r.Header.Get("X-User")
		if actor == "" {
			actor = "anonymous"
		}
		
		action := fmt.Sprintf("%s %s", r.Method, r.URL.Path)
		resourceType := extractResourceType(r.URL.Path)
		
		// Get client IP (extract IP from host:port format)
		ipAddress := r.RemoteAddr
		if xForwarded := r.Header.Get("X-Forwarded-For"); xForwarded != "" {
			ipAddress = strings.Split(xForwarded, ",")[0]
		}
		// Extract IP from "host:port" format
		if host, _, err := net.SplitHostPort(ipAddress); err == nil {
			ipAddress = host
		}
		
		// Create wrapped response writer to capture status
		wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		
		// Process request
		next.ServeHTTP(wrapped, r)
		
		// Only log write operations that succeeded
		if r.Method != "GET" && wrapped.statusCode < 400 {
			entry := AuditEntry{
				Actor:        actor,
				Action:       action,
				ResourceType: resourceType,
				IPAddress:    ipAddress,
				UserAgent:    r.UserAgent(),
			}
			
			if err := a.Log(entry); err != nil {
				log.Errorf("Failed to log audit entry: %v", err)
			}
		}
	})
}

type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func extractResourceType(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) >= 2 {
		// Remove version prefix
		if parts[0] == "v1" {
			parts = parts[1:]
		}
		if len(parts) > 0 {
			return parts[0]
		}
	}
	return "unknown"
}