package mesh

import (
	"context"
	"time"

	"github.com/gdev6145/Spectral_cloud/pkg/auth"
	pb "github.com/gdev6145/Spectral_cloud/pkg/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Server struct {
	pb.UnimplementedMeshServiceServer
	auth *auth.Manager
}

func NewServer(authManager *auth.Manager) *Server {
	return &Server{auth: authManager}
}

func (s *Server) SendData(ctx context.Context, msg *pb.DataMessage) (*pb.Ack, error) {
	tenant, ok := s.auth.TenantFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing tenant")
	}
	if msg.GetTenantId() != "" && msg.GetTenantId() != tenant {
		return nil, status.Error(codes.PermissionDenied, "tenant mismatch")
	}
	return &pb.Ack{
		SourceId:      msg.GetDestinationId(),
		DestinationId: msg.GetSourceId(),
		Timestamp:     time.Now().UTC().Unix(),
		Message:       "ack",
		TenantId:      tenant,
	}, nil
}

func (s *Server) SendControl(ctx context.Context, msg *pb.ControlMessage) (*pb.Ack, error) {
	tenant, ok := s.auth.TenantFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing tenant")
	}
	if msg.GetTenantId() != "" && msg.GetTenantId() != tenant {
		return nil, status.Error(codes.PermissionDenied, "tenant mismatch")
	}
	return &pb.Ack{
		SourceId:      msg.GetNodeId(),
		DestinationId: msg.GetNodeId(),
		Timestamp:     time.Now().UTC().Unix(),
		Message:       "ack",
		TenantId:      tenant,
	}, nil
}
