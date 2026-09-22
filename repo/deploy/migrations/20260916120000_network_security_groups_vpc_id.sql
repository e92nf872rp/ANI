-- =============================
-- 迁移说明
-- =============================
-- 目的：为 network_security_groups 增加可空的 vpc_id 列，落地安全组与 VPC 的
--       关联建模（OpenAPI v1.yaml 已在创建请求/响应/列表过滤三处声明 vpc_id，
--       此前实现未跟随契约，见测试缺陷 安全组-3/安全组-4）。
-- 变更：ALTER TABLE network_security_groups ADD COLUMN vpc_id TEXT（可空）。
-- 回滚：ALTER TABLE network_security_groups DROP COLUMN IF EXISTS vpc_id;
--       （回滚前需确认无业务依赖该列；本迁移不引入 NOT NULL 与外键约束。）
-- 影响面：现有读写 SQL 均为显式列清单，新增可空列不改变既有 INSERT/SELECT
--       语义；存量行 vpc_id 为 NULL，匹配契约"历史资源可为空"的 nullable 语义。

ALTER TABLE network_security_groups ADD COLUMN IF NOT EXISTS vpc_id TEXT;
