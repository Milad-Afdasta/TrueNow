// Package versions provides centralized dependency version management for TrueNow Analytics Platform
// All services MUST import and use these versions to ensure consistency across the platform
package versions

// Core Dependencies
const (
	GoVersion       = "1.21"
	GRPCVersion     = "v1.59.0"
	ProtobufVersion = "v1.31.0"
)

// Logging & Monitoring
const (
	LogrusVersion            = "v1.9.3"
	PrometheusClientVersion  = "v1.18.0"
)

// Database & Storage
const (
	PostgresVersion = "v1.10.9"
	RedisVersion    = "v9.12.0"
	KafkaVersion    = "v0.4.48"
)

// Web Frameworks
const (
	GorillaMuxVersion = "v1.8.1"
	FastHTTPVersion   = "v1.64.0"
	TcellVersion      = "v2.6.0"
	TviewVersion      = "v0.0.0-20230826224341-9754ab44dc1c"
)

// Configuration
const (
	ViperVersion = "v1.20.0-alpha.6"
)

// Testing
const (
	TestifyVersion = "v1.9.0"
)

// Utilities
const (
	XXHashVersion = "v2.3.0"
	UUIDVersion   = "v1.3.1"
)

// Golang.org/x packages
const (
	GolangSysVersion    = "v0.25.0"
	GolangNetVersion    = "v0.29.0"
	GolangTextVersion   = "v0.18.0"
	GolangCryptoVersion = "v0.38.0"
	GolangTermVersion   = "v0.11.0"
	GolangModVersion    = "v0.17.0"
	GolangToolsVersion  = "v0.6.0"
)

// Google Cloud & gRPC ecosystem
const (
	GenprotoVersion      = "v0.0.0-20230822172742-b8732ec3820d"
	GolangProtobufVersion = "v1.5.3"
	GoogleCmpVersion      = "v0.5.9"
)

// GetAllDependencies returns a map of all dependencies with their versions
func GetAllDependencies() map[string]string {
	return map[string]string{
		"google.golang.org/grpc":                        GRPCVersion,
		"google.golang.org/protobuf":                    ProtobufVersion,
		"github.com/sirupsen/logrus":                    LogrusVersion,
		"github.com/prometheus/client_golang":           PrometheusClientVersion,
		"github.com/lib/pq":                             PostgresVersion,
		"github.com/redis/go-redis/v9":                  RedisVersion,
		"github.com/segmentio/kafka-go":                 KafkaVersion,
		"github.com/gorilla/mux":                        GorillaMuxVersion,
		"github.com/valyala/fasthttp":                   FastHTTPVersion,
		"github.com/gdamore/tcell/v2":                   TcellVersion,
		"github.com/rivo/tview":                         TviewVersion,
		"github.com/spf13/viper":                        ViperVersion,
		"github.com/stretchr/testify":                   TestifyVersion,
		"github.com/cespare/xxhash/v2":                  XXHashVersion,
		"github.com/google/uuid":                        UUIDVersion,
		"golang.org/x/sys":                              GolangSysVersion,
		"golang.org/x/net":                              GolangNetVersion,
		"golang.org/x/text":                             GolangTextVersion,
		"golang.org/x/crypto":                           GolangCryptoVersion,
		"golang.org/x/term":                             GolangTermVersion,
		"golang.org/x/mod":                              GolangModVersion,
		"golang.org/x/tools":                            GolangToolsVersion,
		"google.golang.org/genproto/googleapis/rpc":     GenprotoVersion,
		"github.com/golang/protobuf":                    GolangProtobufVersion,
		"github.com/google/go-cmp":                      GoogleCmpVersion,
	}
}