ALTER TABLE workload_instances
    ADD COLUMN IF NOT EXISTS vpc_id TEXT,
    ADD COLUMN IF NOT EXISTS subnet_id TEXT,
    ADD COLUMN IF NOT EXISTS private_ip TEXT;

COMMENT ON COLUMN workload_instances.vpc_id IS
    'ANI VPC selected at instance create time; translated to provider networking by runtime adapters.';
COMMENT ON COLUMN workload_instances.subnet_id IS
    'ANI subnet selected at instance create time; Kube-OVN adapters translate this to Subnet CR name.';
COMMENT ON COLUMN workload_instances.private_ip IS
    'Optional requested private IPv4 address inside the selected ANI subnet.';
