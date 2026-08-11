// Package llmv1 defines the additive JSON representation spoken between a host
// application, the Router, and Provider Processes. The host may be any agent,
// CLI, or service; nothing in this protocol is specific to one of them.
package llmv1

import (
	"encoding/json"

	llm "github.com/rivt-ai/go-inference-router"
)

// Protocol is the current compatible wire-protocol version.
const Protocol = "llm.v1"

// JSON-RPC method names defined by llm.v1.
const (
	MethodInitialize         = Protocol + ".initialize"
	MethodProfilesList       = Protocol + ".profiles.list"
	MethodModelsDiscover     = Protocol + ".models.discover"
	MethodProvidersStatus    = Protocol + ".providers.status"
	MethodCapabilitiesGet    = Protocol + ".capabilities.get"
	MethodChat               = Protocol + ".chat"
	MethodEmbed              = Protocol + ".embed"
	MethodInstallPlan        = Protocol + ".install.plan"
	MethodInstallApprove     = Protocol + ".install.approve"
	MethodInstallAvailable   = Protocol + ".install.available"
	MethodInstallRemove      = Protocol + ".install.remove"
	MethodConfigReload       = Protocol + ".config.reload"
	MethodProviderStop       = Protocol + ".providers.stop"
	MethodObservation        = Protocol + ".observation"
	MethodShutdown           = Protocol + ".shutdown"
	MethodStreamEvent        = Protocol + ".stream.event"
	MethodProviderInitialize = Protocol + ".provider.initialize"
	MethodProviderModels     = Protocol + ".provider.models"
	MethodProviderMetadata   = Protocol + ".provider.metadata"
	MethodProviderChat       = Protocol + ".provider.chat"
	MethodProviderEmbed      = Protocol + ".provider.embed"
)

// InitializeRequest negotiates a host-to-router session.
type InitializeRequest struct {
	ClientName    string   `json:"client_name"`
	ClientVersion string   `json:"client_version,omitempty"`
	Protocols     []string `json:"protocols"`
	Workspace     string   `json:"workspace,omitempty"`
	Observations  bool     `json:"observations,omitempty"`
}

// InitializeResponse describes the negotiated router endpoint.
type InitializeResponse struct {
	Protocol     string `json:"protocol"`
	Name         string `json:"name"`
	Version      string `json:"version,omitempty"`
	Observations bool   `json:"observations,omitempty"`
}

// Profile is a configured friendly model alias.
type Profile struct {
	ID           string           `json:"id"`
	Provider     string           `json:"provider"`
	Model        string           `json:"model"`
	Available    bool             `json:"available"`
	Capabilities llm.Capabilities `json:"capabilities,omitempty"`
}

// ProfilesResponse lists configured profiles.
type ProfilesResponse struct {
	Profiles []Profile `json:"profiles"`
}

// ModelsDiscoverRequest optionally limits discovery to one provider.
type ModelsDiscoverRequest struct {
	Provider string `json:"provider,omitempty"`
}

// DiscoveredModel associates provider-neutral metadata with its provider.
type DiscoveredModel struct {
	Provider string        `json:"provider"`
	Model    llm.ModelInfo `json:"model"`
}

// ModelsDiscoverResponse contains informational discovered models.
type ModelsDiscoverResponse struct {
	Models []DiscoveredModel `json:"models"`
}

// ProviderState describes provider-process availability.
type ProviderState string

// Provider lifecycle states exposed to the host.
const (
	ProviderStopped         ProviderState = "stopped"
	ProviderStarting        ProviderState = "starting"
	ProviderAvailable       ProviderState = "available"
	ProviderUnavailable     ProviderState = "unavailable"
	ProviderInstallRequired ProviderState = "install_required"
)

// ProviderStatus reports one configured provider's runtime state.
type ProviderStatus struct {
	ID      string        `json:"id"`
	Type    string        `json:"type"`
	State   ProviderState `json:"state"`
	Version string        `json:"version,omitempty"`
	Error   *ErrorData    `json:"error,omitempty"`
}

// ProvidersStatusResponse reports every configured provider.
type ProvidersStatusResponse struct {
	Providers []ProviderStatus `json:"providers"`
}

// CapabilitiesRequest selects a profile for metadata inspection.
type CapabilitiesRequest struct {
	ProfileID string `json:"profile_id"`
}

// CapabilitiesResponse reports negotiated model features and limits.
type CapabilitiesResponse struct {
	Capabilities llm.Capabilities `json:"capabilities"`
	Metadata     llm.Metadata     `json:"metadata,omitempty"`
}

// ChatRequest routes one generation request through a configured profile.
type ChatRequest struct {
	ProfileID string      `json:"profile_id"`
	Stream    bool        `json:"stream,omitempty"`
	Request   llm.Request `json:"request"`
}

// ChatResponse wraps a completed provider-neutral response.
type ChatResponse struct {
	Response llm.Response `json:"response"`
}

// StreamEvent associates an ordered event with a streaming request.
type StreamEvent struct {
	RequestID string    `json:"request_id"`
	Sequence  uint64    `json:"sequence"`
	Event     llm.Event `json:"event"`
}

// EmbedRequest routes one embedding request through a configured profile.
type EmbedRequest struct {
	ProfileID string               `json:"profile_id"`
	Request   llm.EmbeddingRequest `json:"request"`
}

// EmbedResponse contains provider-neutral embedding vectors.
type EmbedResponse struct {
	Vectors [][]float32 `json:"vectors"`
}

// InstallPlanRequest requests a provider artifact without installing it.
type InstallPlanRequest struct {
	Provider string `json:"provider"`
	Version  string `json:"version,omitempty"`
}

// InstallPlan is a short-lived, reviewable provider installation proposal.
type InstallPlan struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	Version  string `json:"version"`
	Source   string `json:"source"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
	Expires  string `json:"expires"`
}

// InstallApproveRequest authorizes one previously returned plan.
type InstallApproveRequest struct {
	PlanID string `json:"plan_id"`
}

// InstallResponse identifies the verified installed provider binary.
type InstallResponse struct {
	Path string `json:"path"`
}

// InstallAvailableRequest selects one Provider Process artifact type.
type InstallAvailableRequest struct {
	Provider string `json:"provider"`
}

// InstallAvailableResponse compares cached and registry Provider Process versions.
type InstallAvailableResponse struct {
	Provider          string   `json:"provider"`
	InstalledVersions []string `json:"installed_versions"`
	InstalledVersion  string   `json:"installed_version,omitempty"`
	AvailableVersion  string   `json:"available_version"`
	UpdateAvailable   bool     `json:"update_available"`
}

// InstallRemoveRequest removes one exact cached Provider Process version.
type InstallRemoveRequest struct {
	Provider string `json:"provider"`
	Version  string `json:"version"`
}

// InstallRemoveResponse reports whether a cached version was removed.
type InstallRemoveResponse struct {
	Removed bool `json:"removed"`
}

// ConfigReloadResponse confirms that configuration was reloaded.
type ConfigReloadResponse struct{}

// ProviderStopRequest selects one Provider Definition to stop.
type ProviderStopRequest struct {
	ProviderID string `json:"provider_id"`
}

// ProviderStopResponse confirms that an opened provider was stopped.
type ProviderStopResponse struct{}

// ObservationNotification is the portable form of a runtime observation.
type ObservationNotification struct {
	Operation      llm.ObservationOperation `json:"operation"`
	Phase          llm.ObservationPhase     `json:"phase"`
	Time           string                   `json:"time"`
	DurationMillis int64                    `json:"duration_ms,omitempty"`
	RequestID      string                   `json:"request_id,omitempty"`
	ProfileID      string                   `json:"profile_id,omitempty"`
	ProviderID     string                   `json:"provider_id,omitempty"`
	ProviderType   string                   `json:"provider_type,omitempty"`
	Model          string                   `json:"model,omitempty"`
	Version        string                   `json:"version,omitempty"`
	Streaming      bool                     `json:"streaming,omitempty"`
	Attempt        int                      `json:"attempt,omitempty"`
	Status         int                      `json:"status,omitempty"`
	StreamEvents   uint64                   `json:"stream_events,omitempty"`
	Usage          llm.Usage                `json:"usage,omitempty"`
	Error          *ErrorData               `json:"error,omitempty"`
}

// ProviderInitializeRequest configures a newly launched provider process.
type ProviderInitializeRequest struct {
	ProviderID   string                     `json:"provider_id"`
	Protocols    []string                   `json:"protocols"`
	Config       map[string]json.RawMessage `json:"config,omitempty"`
	Secrets      map[string]string          `json:"secrets,omitempty"`
	Observations bool                       `json:"observations,omitempty"`
}

// ProviderInitializeResponse completes protocol and capability negotiation.
type ProviderInitializeResponse struct {
	Protocol     string           `json:"protocol"`
	Name         string           `json:"name"`
	Version      string           `json:"version"`
	Capabilities llm.Capabilities `json:"capabilities"`
	Observations bool             `json:"observations,omitempty"`
}

// ProviderModelsRequest carries host correlation for model discovery.
type ProviderModelsRequest struct {
	CorrelationID string `json:"correlation_id,omitempty"`
}

// ProviderModelsResponse lists models reported by one provider process.
type ProviderModelsResponse struct {
	Models []llm.ModelInfo `json:"models"`
}

// ProviderMetadataRequest selects a provider model for inspection.
type ProviderMetadataRequest struct {
	Model         string `json:"model"`
	CorrelationID string `json:"correlation_id,omitempty"`
}

// ProviderMetadataResponse wraps provider-neutral model metadata.
type ProviderMetadataResponse struct {
	Metadata llm.Metadata `json:"metadata"`
}

// ProviderChatRequest carries a direct router-to-provider generation request.
type ProviderChatRequest struct {
	StreamID      string      `json:"stream_id,omitempty"`
	CorrelationID string      `json:"correlation_id,omitempty"`
	Request       llm.Request `json:"request"`
}

// ProviderEmbedRequest carries a direct router-to-provider embedding request.
type ProviderEmbedRequest struct {
	CorrelationID string               `json:"correlation_id,omitempty"`
	Request       llm.EmbeddingRequest `json:"request"`
}

// ErrorData is the portable structured payload attached to JSON-RPC errors.
type ErrorData struct {
	Kind     llm.Kind `json:"kind"`
	Provider string   `json:"provider,omitempty"`
	Status   int      `json:"status,omitempty"`
	Message  string   `json:"message,omitempty"`
}
