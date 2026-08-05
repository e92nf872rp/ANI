# PRD: Console 网络负载均衡

> Revised: 2026-08-05
> 详文：`docs/console-modules/compute/network/load-balancer.md`

## 1. Overview

负载均衡创建、查询和删除；创建后独立管理 listeners、后端组、后端成员和健康检查。

## 2. Goals

- scheme internal/public
- vpc_id 必填创建
- listener 必须关联后端组
- 后端支持实例/IP、端口、权重和健康状态

## 3. User Stories

US-001 列表；US-002 创建；US-003 删除；US-004 配置监听器；US-005 管理后端组和成员；US-006 查看后端健康状态。

## 4. Non-Goals

- UDP listener、HTTPS 证书、静态 VIP 和独立 EIP 资源
- 负载均衡主资源更新

## 5. References

- 详文、SPEC
