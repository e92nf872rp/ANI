// Package targetiam contains the Gateway-side client seam for the frozen
// Direct P2 IAM contract. The interface deliberately exposes only the two
// operations used by the DP2-05 tracer bullet.
package targetiam

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	iamv1 "github.com/kubercloud/ani/services/ani-gateway/internal/targetiam/gen"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

const DecisionTimeout = 500 * time.Millisecond

type Client interface {
	PasswordLogin(context.Context, *iamv1.PasswordLoginRequest) (*iamv1.PasswordLoginResponse, error)
	CheckPermission(context.Context, *iamv1.CheckPermissionRequest) (*iamv1.CheckPermissionResponse, error)
}

type grpcClient struct {
	authentication iamv1.AuthenticationServiceClient
	authorization  iamv1.AuthorizationServiceClient
}

type MutualTLSConfig struct {
	ServerName      string
	CAFile          string
	CertificateFile string
	PrivateKeyFile  string
}

func NewGRPCClient(connection grpc.ClientConnInterface) Client {
	return &grpcClient{
		authentication: iamv1.NewAuthenticationServiceClient(connection),
		authorization:  iamv1.NewAuthorizationServiceClient(connection),
	}
}

func Dial(address string, config MutualTLSConfig) (Client, func() error, error) {
	address = strings.TrimSpace(address)
	if address == "" {
		return nil, nil, errors.New("target IAM address is required")
	}
	config.ServerName = strings.TrimSpace(config.ServerName)
	config.CAFile = strings.TrimSpace(config.CAFile)
	config.CertificateFile = strings.TrimSpace(config.CertificateFile)
	config.PrivateKeyFile = strings.TrimSpace(config.PrivateKeyFile)
	if config.ServerName == "" {
		return nil, nil, errors.New("target IAM TLS server name is required")
	}
	if config.CAFile == "" {
		return nil, nil, errors.New("target IAM TLS CA file is required")
	}
	if config.CertificateFile == "" {
		return nil, nil, errors.New("target IAM TLS certificate file is required")
	}
	if config.PrivateKeyFile == "" {
		return nil, nil, errors.New("target IAM TLS private key file is required")
	}

	caPEM, err := os.ReadFile(config.CAFile)
	if err != nil {
		return nil, nil, fmt.Errorf("read target IAM TLS CA: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return nil, nil, errors.New("parse target IAM TLS CA: no certificates found")
	}
	certificate, err := tls.LoadX509KeyPair(config.CertificateFile, config.PrivateKeyFile)
	if err != nil {
		return nil, nil, fmt.Errorf("load target IAM TLS client key pair: %w", err)
	}
	transport := credentials.NewTLS(&tls.Config{
		MinVersion:   tls.VersionTLS13,
		ServerName:   config.ServerName,
		RootCAs:      roots,
		Certificates: []tls.Certificate{certificate},
	})
	connection, err := grpc.NewClient(address,
		grpc.WithTransportCredentials(transport),
		grpc.WithDisableRetry(),
	)
	if err != nil {
		return nil, nil, err
	}
	return NewGRPCClient(connection), connection.Close, nil
}

func (c *grpcClient) PasswordLogin(ctx context.Context, request *iamv1.PasswordLoginRequest) (*iamv1.PasswordLoginResponse, error) {
	callCtx, cancel := context.WithTimeout(ctx, DecisionTimeout)
	defer cancel()
	return c.authentication.PasswordLogin(callCtx, request)
}

func (c *grpcClient) CheckPermission(ctx context.Context, request *iamv1.CheckPermissionRequest) (*iamv1.CheckPermissionResponse, error) {
	callCtx, cancel := context.WithTimeout(ctx, DecisionTimeout)
	defer cancel()
	return c.authorization.CheckPermission(callCtx, request)
}

var _ Client = (*grpcClient)(nil)
