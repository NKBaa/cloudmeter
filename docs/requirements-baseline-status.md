# 最终需求基线对照状态

更新时间：2026-08-20

本文对照 `pasted-text.txt` 的 GLOBAL/APP/NET/DEL/LOG/AUTH/BOT/QUOTA/BAL/MON/IMG/NAV/SIDE/FAQ/UI/SEC/ERR/实例模型要求，记录当前代码证据与剩余验收工作。

## 已完成（代码已有证据）

- APP：实例使用独立 `instance_id`；容器名、数据卷、路由按实例隔离；同用户可多实例；版本快照与模板生命周期解耦；已发布版本可编辑并创建新版本；下架/完全删除只处理模板并写审计。
- NET：用户级 Docker Network；普通应用创建请求不包含 `PortBindings`/`PublishAllPorts`；Gateway 是唯一宿主机业务入口；Router 按实例路由并校验会话和所有权。
- LOG/SEC：管理员 API 后端校验；高风险操作确认并写管理审计；错误包含 request/instance 关联字段；Secret 只返回元数据/掩码。
- AUTH/BOT：认证策略、邮箱域名精确匹配、Turnstile 服务端校验与持久化。
- QUOTA/BAL：新用户额度只影响未来账户；邀请奖励幂等并入账；低余额提醒支持阈值、冷却、恢复重置、邮件重试和日志。
- MON/IMG/NAV/SIDE/FAQ：监控、镜像引用检查/安全删除、主页/文档/控制台导航、侧栏显示策略、FAQ 管理和用户读取均已有路由/API。
- UI/ERR：控制台壳层保留，菜单只替换内容；深色紧凑主题、过渡动画、加载/空/错误/重试状态已有统一基础样式。

## 本次修正

系统盘容量不再是新模板运行规格的必填项。`RuntimeStorage` 对缺失字段使用平台固定内部默认值；系统盘不出现在用户可选费用项，`storage.system.gib_days` 已从定价管理过滤。历史规格仍兼容读取。

## 部分完成/需要实机验收

1. WSL 空环境需补真实产品模板、SillyTavern 拉取部署、同应用多实例、网关路径、更新版本、删除模板后实例继续运行。
2. 需要逐页浏览器验收所有管理/用户页面的 NewAPI 风格一致性、移动端溢出和失败重试。
3. 需要验证备份按应用→数据卷二级展示、按时间排序和单项删除；EPay 回调、SMTP 实际投递、工单完整对话。

## 验证命令

```powershell
go test ./...
cd apps/web; npm run build
git diff --check
```

本次执行结果：Go 全部通过，Web `vue-tsc` 与 Vite 构建通过，diff 检查仅提示 Git 换行转换警告。
