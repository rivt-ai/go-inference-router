// The SDK-backed Anthropic driver is a separate module on purpose: the core
// module promises zero third-party dependencies, and the official SDK brings
// eight transitive ones. Importing this package opts into them; importing
// github.com/rivt-ai/go-inference-router alone never does.
module github.com/rivt-ai/go-inference-router/provider/anthropicsdk

go 1.27.0

require (
	github.com/anthropics/anthropic-sdk-go v1.84.0
	github.com/rivt-ai/go-inference-router v0.9.1
)

require (
	cloud.google.com/go/compute/metadata v0.10.0 // indirect
	github.com/aws/aws-sdk-go-v2 v1.47.6 // indirect
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.7.26 // indirect
	github.com/aws/aws-sdk-go-v2/config v1.33.14 // indirect
	github.com/aws/aws-sdk-go-v2/credentials v1.20.14 // indirect
	github.com/bahlo/generic-list-go v0.2.0 // indirect
	github.com/buger/jsonparser v1.1.2 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/go-logr/logr v1.4.4 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/invopop/jsonschema v0.14.0 // indirect
	github.com/pb33f/ordered-map/v2 v2.3.1 // indirect
	github.com/standard-webhooks/standard-webhooks/libraries v0.0.1 // indirect
	github.com/tidwall/gjson v1.18.0 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/sjson v1.2.5 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc v0.72.0 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.72.0 // indirect
	go.opentelemetry.io/otel v1.47.0 // indirect
	go.opentelemetry.io/otel/log v1.47.0 // indirect
	go.opentelemetry.io/otel/metric v1.47.0 // indirect
	go.opentelemetry.io/otel/sdk v1.47.0 // indirect
	go.opentelemetry.io/otel/trace v1.47.0 // indirect
	go.yaml.in/yaml/v4 v4.0.0 // indirect
	golang.org/x/oauth2 v0.37.0 // indirect
	golang.org/x/sync v0.24.0 // indirect
	google.golang.org/api v0.305.0 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
)
