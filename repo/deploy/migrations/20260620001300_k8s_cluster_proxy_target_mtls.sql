-- ANI Platform · Migration 013
-- Description: Sprint 13 S02 production-shaped vCluster mTLS proxy target credentials
-- Depends on: 20260523000800_k8s_cluster_proxy_targets.sql


ALTER TABLE k8s_cluster_proxy_targets
    ADD COLUMN IF NOT EXISTS ca_data TEXT,
    ADD COLUMN IF NOT EXISTS client_certificate_data TEXT,
    ADD COLUMN IF NOT EXISTS client_key_data TEXT;

COMMENT ON COLUMN k8s_cluster_proxy_targets.ca_data IS
    'Base64 kubeconfig certificate-authority-data for vCluster mTLS proxy forwarding.';
COMMENT ON COLUMN k8s_cluster_proxy_targets.client_certificate_data IS
    'Base64 kubeconfig client-certificate-data for vCluster mTLS proxy forwarding.';
COMMENT ON COLUMN k8s_cluster_proxy_targets.client_key_data IS
    'Base64 kubeconfig client-key-data for vCluster mTLS proxy forwarding; production deployments should move this secret material behind KMS or a Kubernetes Secret provider.';

