package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	controlplane "github.com/Milad-Afdasta/TrueNow/proto/controlplane"
	"github.com/Milad-Afdasta/TrueNow/services/control-plane/internal/audit"
	grpcapi "github.com/Milad-Afdasta/TrueNow/services/control-plane/internal/grpcapi"
	"github.com/Milad-Afdasta/TrueNow/services/control-plane/internal/registry"
	"github.com/gorilla/mux"
	pq "github.com/lib/pq"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
)

type Config struct {
	Server struct {
		HTTPPort string `mapstructure:"http_port"`
		GRPCPort string `mapstructure:"grpc_port"`
	} `mapstructure:"server"`
	Database struct {
		Host     string `mapstructure:"host"`
		Port     int    `mapstructure:"port"`
		Name     string `mapstructure:"name"`
		User     string `mapstructure:"user"`
		Password string `mapstructure:"password"`
	} `mapstructure:"database"`
	Discovery struct {
		ServiceTTLSeconds int `mapstructure:"service_ttl_seconds"`
	} `mapstructure:"discovery"`
}

type Server struct {
	db           *sql.DB
	config       *Config
	router       *mux.Router
	auditor      *audit.Auditor
	registry     *registry.Registry
	queryTimeout time.Duration
}

func (s *Server) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if s.queryTimeout <= 0 {
		s.queryTimeout = 2 * time.Second
	}
	return context.WithTimeout(ctx, s.queryTimeout)
}

func main() {
	log.SetFormatter(&log.JSONFormatter{})
	log.SetLevel(log.InfoLevel)

	config := loadConfig()

	db, err := connectDB(config)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	auditor := audit.NewAuditor(db)

	serviceTTL := time.Duration(config.Discovery.ServiceTTLSeconds) * time.Second
	if serviceTTL <= 0 {
		serviceTTL = 30 * time.Second
	}
	reg := registry.NewRegistry(serviceTTL)
	defer reg.Close()

	server := &Server{
		db:           db,
		config:       config,
		router:       mux.NewRouter(),
		auditor:      auditor,
		registry:     reg,
		queryTimeout: 2 * time.Second,
	}

	server.setupRoutes()

	grpcSrv := grpc.NewServer(
		grpc.KeepaliveParams(keepalive.ServerParameters{
			MaxConnectionIdle:     5 * time.Minute,
			MaxConnectionAge:      30 * time.Minute,
			MaxConnectionAgeGrace: 1 * time.Minute,
			Time:                  2 * time.Minute,
			Timeout:               20 * time.Second,
		}),
	)
	controlplane.RegisterControlPlaneServiceServer(grpcSrv, grpcapi.NewServer(db, auditor, reg))

	grpcListener, err := net.Listen("tcp", ":"+config.Server.GRPCPort)
	if err != nil {
		log.Fatalf("failed to listen on gRPC port %s: %v", config.Server.GRPCPort, err)
	}

	httpServer := &http.Server{
		Addr:    ":" + config.Server.HTTPPort,
		Handler: server.router,
	}

	serverErrs := make(chan error, 2)

	go func() {
		log.Infof("Control Plane HTTP listening on :%s", config.Server.HTTPPort)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serverErrs <- fmt.Errorf("http server error: %w", err)
		}
	}()

	go func() {
		log.Infof("Control Plane gRPC listening on :%s", config.Server.GRPCPort)
		if err := grpcSrv.Serve(grpcListener); err != nil {
			serverErrs <- fmt.Errorf("grpc server error: %w", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-quit:
		log.WithField("signal", sig).Info("Control Plane shutdown requested")
	case err := <-serverErrs:
		log.WithError(err).Error("Control Plane server failure")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		log.WithError(err).Error("HTTP server forced shutdown")
	}
	grpcSrv.GracefulStop()
	_ = grpcListener.Close()

	log.Info("Control Plane exited cleanly")
}

func loadConfig() *Config {
	viper.SetDefault("server.http_port", "8001")
	viper.SetDefault("server.grpc_port", "9001")
	viper.SetDefault("database.host", "localhost")
	viper.SetDefault("database.port", 5432)
	viper.SetDefault("database.name", "analytics")
	viper.SetDefault("database.user", os.Getenv("USER"))
	viper.SetDefault("database.password", "")
	viper.SetDefault("discovery.service_ttl_seconds", 30)

	viper.SetEnvPrefix("CONTROL")
	viper.AutomaticEnv()

	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath("./config")
	viper.AddConfigPath(".")

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			log.Fatalf("Error reading config file: %v", err)
		}
		log.Info("No config file found, using defaults")
	}

	var config Config
	if err := viper.Unmarshal(&config); err != nil {
		log.Fatalf("Failed to unmarshal config: %v", err)
	}

	return &config
}

func connectDB(config *Config) (*sql.DB, error) {
	dsn := fmt.Sprintf("host=%s port=%d user=%s dbname=%s sslmode=disable",
		config.Database.Host,
		config.Database.Port,
		config.Database.User,
		config.Database.Name,
	)

	if config.Database.Password != "" {
		dsn += fmt.Sprintf(" password=%s", config.Database.Password)
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	if err := db.Ping(); err != nil {
		return nil, err
	}

	log.Info("Connected to PostgreSQL database")
	return db, nil
}

func (s *Server) setupRoutes() {
	// Health endpoints
	s.router.HandleFunc("/health", s.healthHandler).Methods("GET")
	s.router.HandleFunc("/ready", s.readyHandler).Methods("GET")

	// Metrics
	s.router.Handle("/metrics", promhttp.Handler())

	// API v1 routes
	v1 := s.router.PathPrefix("/v1").Subrouter()

	// Namespace endpoints
	v1.HandleFunc("/namespaces", s.createNamespace).Methods("POST")
	v1.HandleFunc("/namespaces", s.listNamespaces).Methods("GET")
	v1.HandleFunc("/namespaces/{id}", s.getNamespace).Methods("GET")

	// Table endpoints
	v1.HandleFunc("/namespaces/{ns}/tables", s.createTable).Methods("POST")
	v1.HandleFunc("/namespaces/{ns}/tables", s.listTables).Methods("GET")

	// Registry endpoints
	v1.HandleFunc("/registry/shards", s.getShardRegistry).Methods("GET")

	// Epoch endpoints
	v1.HandleFunc("/epochs/current", s.getCurrentEpoch).Methods("GET")

	// Middleware
	s.router.Use(loggingMiddleware)
	s.router.Use(s.auditor.Middleware)
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.WithFields(log.Fields{
			"method":   r.Method,
			"path":     r.URL.Path,
			"duration": time.Since(start),
		}).Info("Request handled")
	})
}

func (s *Server) healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
}

func (s *Server) readyHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.withTimeout(r.Context())
	defer cancel()
	if err := s.db.PingContext(ctx); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"status": "not ready", "error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ready"})
}

func (s *Server) createNamespace(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string                 `json:"name"`
		DisplayName string                 `json:"display_name"`
		Owner       string                 `json:"owner"`
		Quotas      map[string]interface{} `json:"quotas"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	quotasJSON, err := json.Marshal(req.Quotas)
	if err != nil {
		http.Error(w, "invalid quotas payload", http.StatusBadRequest)
		return
	}

	var id, name string
	ctx, cancel := s.withTimeout(r.Context())
	defer cancel()
	err = s.db.QueryRowContext(ctx, `
		INSERT INTO namespaces (name, display_name, owner, quotas) 
		VALUES ($1, $2, $3, $4)
		RETURNING id, name`,
		req.Name, req.DisplayName, req.Owner, quotasJSON,
	).Scan(&id, &name)

	if err != nil {
		if pqErr, ok := err.(*pq.Error); ok && pqErr.Code == "23505" {
			http.Error(w, "namespace already exists", http.StatusConflict)
			return
		}
		log.WithError(err).Error("control-plane: create namespace failed")
		http.Error(w, "failed to create namespace", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{
		"id":   id,
		"name": name,
	})
}

func (s *Server) listNamespaces(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.withTimeout(r.Context())
	defer cancel()
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, display_name, owner, created_at 
		FROM namespaces 
		ORDER BY created_at DESC
	`)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var namespaces []map[string]interface{}
	for rows.Next() {
		var id, name, displayName, owner string
		var createdAt time.Time

		if err := rows.Scan(&id, &name, &displayName, &owner, &createdAt); err != nil {
			log.WithError(err).Warn("control-plane: failed scanning namespace row")
			continue
		}

		namespaces = append(namespaces, map[string]interface{}{
			"id":           id,
			"name":         name,
			"display_name": displayName,
			"owner":        owner,
			"created_at":   createdAt,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(namespaces)
}

func (s *Server) getNamespace(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]

	var name, displayName, owner string
	var quotas json.RawMessage
	var createdAt time.Time

	ctx, cancel := s.withTimeout(r.Context())
	defer cancel()
	err := s.db.QueryRowContext(ctx, `
		SELECT name, display_name, owner, quotas, created_at 
		FROM namespaces 
		WHERE id = $1`,
		id,
	).Scan(&name, &displayName, &owner, &quotas, &createdAt)

	if err == sql.ErrNoRows {
		http.Error(w, "Namespace not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":           id,
		"name":         name,
		"display_name": displayName,
		"owner":        owner,
		"quotas":       quotas,
		"created_at":   createdAt,
	})
}

func (s *Server) createTable(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	namespace := vars["ns"]

	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// First get namespace ID
	ctx, cancel := s.withTimeout(r.Context())
	defer cancel()
	var namespaceID string
	err := s.db.QueryRowContext(ctx, "SELECT id FROM namespaces WHERE name = $1", namespace).Scan(&namespaceID)
	if err != nil {
		http.Error(w, "Namespace not found", http.StatusNotFound)
		return
	}

	var id, name string
	err = s.db.QueryRowContext(ctx, `
		INSERT INTO tables (namespace_id, name, description) 
		VALUES ($1, $2, $3)
		RETURNING id, name`,
		namespaceID, req.Name, req.Description,
	).Scan(&id, &name)

	if err != nil {
		if pqErr, ok := err.(*pq.Error); ok && pqErr.Code == "23505" {
			http.Error(w, "table already exists", http.StatusConflict)
			return
		}
		log.WithError(err).Error("control-plane: failed to create table")
		http.Error(w, "failed to create table", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{
		"id":   id,
		"name": name,
	})
}

func (s *Server) listTables(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	namespace := vars["ns"]

	ctx, cancel := s.withTimeout(r.Context())
	defer cancel()
	rows, err := s.db.QueryContext(ctx, `
		SELECT t.id, t.name, t.active_version, t.created_at 
		FROM tables t
		JOIN namespaces n ON t.namespace_id = n.id
		WHERE n.name = $1
		ORDER BY t.created_at DESC`,
		namespace,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var tables []map[string]interface{}
	for rows.Next() {
		var id, name string
		var activeVersion int
		var createdAt time.Time

		if err := rows.Scan(&id, &name, &activeVersion, &createdAt); err != nil {
			log.WithError(err).Warn("control-plane: failed scanning table row")
			continue
		}

		tables = append(tables, map[string]interface{}{
			"id":             id,
			"name":           name,
			"active_version": activeVersion,
			"created_at":     createdAt,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tables)
}

func (s *Server) getShardRegistry(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.withTimeout(r.Context())
	defer cancel()
	rows, err := s.db.QueryContext(ctx, `
		SELECT s.id, s.virtual_shard_id, s.leader_node_id, s.epoch_number, tv.id as table_version_id
		FROM shards s
		JOIN table_versions tv ON s.table_version_id = tv.id
		WHERE s.status = 'active'
		ORDER BY s.virtual_shard_id
	`)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var shards []map[string]interface{}
	for rows.Next() {
		var id, leaderNodeID, tableVersionID string
		var virtualShardID int
		var epochNumber int64

		if err := rows.Scan(&id, &virtualShardID, &leaderNodeID, &epochNumber, &tableVersionID); err != nil {
			log.WithError(err).Warn("control-plane: failed scanning shard row")
			continue
		}

		shards = append(shards, map[string]interface{}{
			"id":               id,
			"virtual_shard_id": virtualShardID,
			"leader_node_id":   leaderNodeID,
			"epoch_number":     epochNumber,
			"table_version_id": tableVersionID,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"shards": shards,
	})
}

func (s *Server) getCurrentEpoch(w http.ResponseWriter, r *http.Request) {
	tableVersionID := r.URL.Query().Get("table_version_id")
	if tableVersionID == "" {
		http.Error(w, "table_version_id required", http.StatusBadRequest)
		return
	}

	var epochNumber int64
	ctx, cancel := s.withTimeout(r.Context())
	defer cancel()
	err := s.db.QueryRowContext(ctx, `
		SELECT epoch_number 
		FROM epochs 
		WHERE table_version_id = $1 
		ORDER BY epoch_number DESC 
		LIMIT 1`,
		tableVersionID,
	).Scan(&epochNumber)

	if err == sql.ErrNoRows {
		epochNumber = 1 // Default epoch
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"epoch_number":     epochNumber,
		"table_version_id": tableVersionID,
	})
}
