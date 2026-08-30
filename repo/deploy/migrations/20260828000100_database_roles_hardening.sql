-- ANI Platform · Database Role Hardening
-- Version: 20260828000100
-- Description: 分离普通应用账号与 BYPASSRLS 专用账号
-- Depends on: 20260731000100_metering_usage.sql
--
-- ani 继续作为测试数据库管理员；业务服务不得使用 ani。
-- 密码不写入 migration，由部署 Secret/初始化流程设置。

-- ===========================================================================
-- 1) 确保角色存在并固定安全属性
-- ===========================================================================
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'ani_app') THEN
        CREATE ROLE ani_app NOLOGIN;
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'ani_app_user') THEN
        CREATE ROLE ani_app_user LOGIN;
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'ani_migrator') THEN
        CREATE ROLE ani_migrator NOLOGIN;
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'ani_outbox_publisher') THEN
        CREATE ROLE ani_outbox_publisher BYPASSRLS NOLOGIN;
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'ani_metering_writer') THEN
        CREATE ROLE ani_metering_writer BYPASSRLS NOLOGIN;
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'ani_metering_user') THEN
        CREATE ROLE ani_metering_user LOGIN;
    END IF;
END
$$;

ALTER ROLE ani_app NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS;
ALTER ROLE ani_app_user LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS INHERIT;
ALTER ROLE ani_migrator NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS;
ALTER ROLE ani_outbox_publisher NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE BYPASSRLS;
ALTER ROLE ani_metering_writer NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE BYPASSRLS;
ALTER ROLE ani_metering_user LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS INHERIT;

-- ===========================================================================
-- 2) 角色继承关系
-- ===========================================================================
GRANT ani_app TO ani_app_user;

-- metering-service 使用独立登录账号，再在事务内 SET ROLE writer。
GRANT ani_app TO ani_metering_user;
GRANT ani_metering_writer TO ani_metering_user;

-- 普通应用账号不能切换到 BYPASSRLS 角色。
REVOKE ani_metering_writer FROM ani_app_user;

-- ===========================================================================
-- 3) 数据库连接权限
-- ===========================================================================
GRANT CONNECT ON DATABASE ani TO ani_app_user;
GRANT CONNECT ON DATABASE ani TO ani_metering_user;
GRANT ALL PRIVILEGES ON DATABASE ani TO ani_migrator;
