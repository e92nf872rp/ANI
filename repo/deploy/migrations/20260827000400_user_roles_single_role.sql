-- 单角色约束：每个用户只能绑定一个角色
-- user_roles 当前 PK 为 (user_id, role_id)，允许一对多；加 user_id 唯一索引强制单角色
CREATE UNIQUE INDEX IF NOT EXISTS idx_user_roles_user_id_unique ON user_roles (user_id);
