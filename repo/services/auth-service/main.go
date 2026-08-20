package main

import (
	"github.com/kubercloud/ani/pkg/bootstrap"
	"github.com/kubercloud/ani/services/auth-service/internal/config"
	"github.com/kubercloud/ani/services/auth-service/internal/service"
)

func main() {
	cfg := config.Load()
	deps := bootstrap.MustConnect(cfg.Config)
	defer deps.Close()

	authSvc := service.NewAuthService(deps.DB, deps.Ports.Cache, service.JWTConfig{
		PublicKeyPEM:         cfg.JWTPublicKeyPEM,
		PublicKeyFile:        cfg.JWTPublicKeyFile,
		PrivateKeyPEM:        cfg.JWTPrivateKeyPEM,
		PrivateKeyFile:       cfg.JWTPrivateKeyFile,
		Issuer:               cfg.JWTIssuer,
		OIDCIssuerURL:        cfg.OIDCIssuerURL,
		OIDCClientID:         cfg.OIDCClientID,
		OIDCClientSecret:     cfg.OIDCClientSecret,
		OIDCAuthURL:          cfg.OIDCAuthURL,
		OIDCTokenURL:         cfg.OIDCTokenURL,
		OIDCJWKSURL:          cfg.OIDCJWKSURL,
		OIDCPublicKeyPEM:     cfg.OIDCPublicKeyPEM,
		OIDCPublicKeyFile:    cfg.OIDCPublicKeyFile,
		OIDCGroupRoleMapJSON: cfg.OIDCGroupRoleMapJSON,
	}).WithMintCredentials(cfg.MintCredentials)
	bootstrap.RunGRPC(cfg.GRPCPort, authSvc.Register, deps)
}
