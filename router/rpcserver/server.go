// Package rpcserver exposes a Router over the host-facing llm.v1 interface.
package rpcserver

import (
	"context"
	"io"
	"slices"
	"sync/atomic"

	llm "github.com/rivt-ai/go-inference-router"
	"github.com/rivt-ai/go-inference-router/protocol/jsonrpc"
	"github.com/rivt-ai/go-inference-router/protocol/llmv1"
	"github.com/rivt-ai/go-inference-router/router"
	"github.com/rivt-ai/go-inference-router/router/config"
)

// Installer stages and approves signed provider binary installations.
type Installer interface {
	Plan(context.Context, string, string) (llmv1.InstallPlan, error)
	Approve(context.Context, string) (string, error)
	Available(context.Context, string) (llmv1.InstallAvailableResponse, error)
	Remove(context.Context, string, string) (bool, error)
}

// ConfigLoader reloads the Router's configured files.
type ConfigLoader func(context.Context) (config.Config, error)

// Server exposes a Router and forwards observations over llm.v1.
type Server struct {
	conn         *jsonrpc.Conn
	router       *router.Router
	installer    Installer
	load         ConfigLoader
	observations atomic.Bool
}

// New creates a Server on reader and writer. The Server can be passed as an
// llm.Observer before Serve starts.
func New(reader io.Reader, writer io.Writer) *Server {
	return &Server{conn: jsonrpc.New(reader, writer)}
}

// Serve exposes modelRouter until the stream or context closes.
func (s *Server) Serve(ctx context.Context, modelRouter *router.Router, installer Installer, load ConfigLoader) error {
	s.router, s.installer, s.load = modelRouter, installer, load
	s.register()
	return s.conn.Serve(ctx)
}

func (s *Server) register() {
	s.conn.Handle(llmv1.MethodInitialize, s.initialize)
	s.conn.Handle(llmv1.MethodProfilesList, s.profiles)
	s.conn.Handle(llmv1.MethodModelsDiscover, s.discover)
	s.conn.Handle(llmv1.MethodProvidersStatus, s.status)
	s.conn.Handle(llmv1.MethodCapabilitiesGet, s.capabilities)
	s.conn.Handle(llmv1.MethodChat, s.chat)
	s.conn.Handle(llmv1.MethodEmbed, s.embed)
	s.conn.Handle(llmv1.MethodInstallPlan, s.installPlan)
	s.conn.Handle(llmv1.MethodInstallApprove, s.installApprove)
	s.conn.Handle(llmv1.MethodInstallAvailable, s.installAvailable)
	s.conn.Handle(llmv1.MethodInstallRemove, s.installRemove)
	s.conn.Handle(llmv1.MethodConfigReload, s.configReload)
	s.conn.Handle(llmv1.MethodProviderStop, s.providerStop)
	s.conn.Handle(llmv1.MethodShutdown, s.shutdown)
}

func (s *Server) initialize(_ context.Context, params []byte) (any, error) {
	var request llmv1.InitializeRequest
	if err := jsonrpc.Decode(params, &request); err != nil {
		return nil, jsonrpc.InvalidParams(err)
	}
	if !slices.Contains(request.Protocols, llmv1.Protocol) {
		return nil, llmv1.DomainError(&llm.Error{
			Kind: llm.KindProtocol, Provider: "router", Message: "no supported protocol version",
		})
	}
	s.observations.Store(request.Observations)
	return llmv1.InitializeResponse{
		Protocol: llmv1.Protocol, Name: "go-inference-router", Version: "dev",
		Observations: request.Observations,
	}, nil
}

func (s *Server) profiles(ctx context.Context, _ []byte) (any, error) {
	return llmv1.ProfilesResponse{Profiles: s.router.Profiles(ctx)}, nil
}

func (s *Server) discover(ctx context.Context, params []byte) (any, error) {
	var request llmv1.ModelsDiscoverRequest
	if len(params) != 0 {
		if err := jsonrpc.Decode(params, &request); err != nil {
			return nil, jsonrpc.InvalidParams(err)
		}
	}
	models, err := s.router.Discover(ctx, request.Provider)
	if err != nil {
		return nil, llmv1.DomainError(err)
	}
	return llmv1.ModelsDiscoverResponse{Models: models}, nil
}

func (s *Server) status(ctx context.Context, _ []byte) (any, error) {
	return llmv1.ProvidersStatusResponse{Providers: s.router.Status(ctx)}, nil
}

func (s *Server) capabilities(ctx context.Context, params []byte) (any, error) {
	var request llmv1.CapabilitiesRequest
	if err := jsonrpc.Decode(params, &request); err != nil {
		return nil, jsonrpc.InvalidParams(err)
	}
	capabilities, metadata, err := s.router.Capabilities(ctx, request.ProfileID)
	if err != nil {
		return nil, llmv1.DomainError(err)
	}
	return llmv1.CapabilitiesResponse{Capabilities: capabilities, Metadata: metadata}, nil
}

func (s *Server) chat(ctx context.Context, params []byte) (any, error) {
	var request llmv1.ChatRequest
	if err := jsonrpc.Decode(params, &request); err != nil {
		return nil, jsonrpc.InvalidParams(err)
	}
	var sequence uint64
	var emit func(llm.Event) error
	if request.Stream {
		emit = func(event llm.Event) error {
			sequence++
			return s.conn.Notify(ctx, llmv1.MethodStreamEvent, llmv1.StreamEvent{
				RequestID: jsonrpc.RequestID(ctx), Sequence: sequence, Event: event,
			})
		}
	}
	response, err := s.router.Chat(ctx, request.ProfileID, request.Request, emit)
	if err != nil {
		return nil, llmv1.DomainError(err)
	}
	return llmv1.ChatResponse{Response: *response}, nil
}

func (s *Server) embed(ctx context.Context, params []byte) (any, error) {
	var request llmv1.EmbedRequest
	if err := jsonrpc.Decode(params, &request); err != nil {
		return nil, jsonrpc.InvalidParams(err)
	}
	vectors, err := s.router.Embed(ctx, request.ProfileID, request.Request)
	if err != nil {
		return nil, llmv1.DomainError(err)
	}
	return llmv1.EmbedResponse{Vectors: vectors}, nil
}

func (s *Server) installPlan(ctx context.Context, params []byte) (any, error) {
	if s.installer == nil {
		return nil, llmv1.DomainError(&llm.Error{
			Kind: llm.KindUnavailable, Provider: "router", Message: "provider installer is unavailable",
		})
	}
	var request llmv1.InstallPlanRequest
	if err := jsonrpc.Decode(params, &request); err != nil {
		return nil, jsonrpc.InvalidParams(err)
	}
	plan, err := s.installer.Plan(ctx, request.Provider, request.Version)
	if err != nil {
		return nil, llmv1.DomainError(err)
	}
	return plan, nil
}

func (s *Server) installApprove(ctx context.Context, params []byte) (any, error) {
	if s.installer == nil {
		return nil, llmv1.DomainError(&llm.Error{
			Kind: llm.KindUnavailable, Provider: "router", Message: "provider installer is unavailable",
		})
	}
	var request llmv1.InstallApproveRequest
	if err := jsonrpc.Decode(params, &request); err != nil {
		return nil, jsonrpc.InvalidParams(err)
	}
	path, err := s.installer.Approve(ctx, request.PlanID)
	if err != nil {
		return nil, llmv1.DomainError(err)
	}
	return llmv1.InstallResponse{Path: path}, nil
}

func (s *Server) installAvailable(ctx context.Context, params []byte) (any, error) {
	if s.installer == nil {
		return nil, llmv1.DomainError(&llm.Error{
			Kind: llm.KindUnavailable, Provider: "router", Message: "provider installer is unavailable",
		})
	}
	var request llmv1.InstallAvailableRequest
	if err := jsonrpc.Decode(params, &request); err != nil {
		return nil, jsonrpc.InvalidParams(err)
	}
	available, err := s.installer.Available(ctx, request.Provider)
	if err != nil {
		return nil, llmv1.DomainError(err)
	}
	return available, nil
}

func (s *Server) installRemove(ctx context.Context, params []byte) (any, error) {
	if s.installer == nil {
		return nil, llmv1.DomainError(&llm.Error{
			Kind: llm.KindUnavailable, Provider: "router", Message: "provider installer is unavailable",
		})
	}
	var request llmv1.InstallRemoveRequest
	if err := jsonrpc.Decode(params, &request); err != nil {
		return nil, jsonrpc.InvalidParams(err)
	}
	removed, err := s.router.RemoveProviderVersion(ctx, request.Provider, request.Version, s.installer.Remove)
	if err != nil {
		return nil, llmv1.DomainError(err)
	}
	return llmv1.InstallRemoveResponse{Removed: removed}, nil
}

func (s *Server) configReload(ctx context.Context, _ []byte) (any, error) {
	if s.load == nil {
		return nil, llmv1.DomainError(&llm.Error{
			Kind: llm.KindUnavailable, Provider: "router", Message: "configuration reload is unavailable",
		})
	}
	cfg, err := s.load(ctx)
	if err == nil {
		err = s.router.Apply(ctx, cfg)
	}
	if err != nil {
		return nil, llmv1.DomainError(err)
	}
	return llmv1.ConfigReloadResponse{}, nil
}

func (s *Server) providerStop(ctx context.Context, params []byte) (any, error) {
	var request llmv1.ProviderStopRequest
	if err := jsonrpc.Decode(params, &request); err != nil {
		return nil, jsonrpc.InvalidParams(err)
	}
	if err := s.router.StopProvider(ctx, request.ProviderID); err != nil {
		return nil, llmv1.DomainError(err)
	}
	return llmv1.ProviderStopResponse{}, nil
}

func (s *Server) shutdown(context.Context, []byte) (any, error) {
	return struct{}{}, s.router.Close()
}

// Observe forwards an observation to hosts that opted in during initialize.
func (s *Server) Observe(ctx context.Context, event llm.Observation) {
	if !s.observations.Load() {
		return
	}
	if event.RequestID == "" {
		event.RequestID = jsonrpc.RequestID(ctx)
	}
	_ = s.conn.Notify(ctx, llmv1.MethodObservation, llmv1.ObservationNotificationOf(event))
}
