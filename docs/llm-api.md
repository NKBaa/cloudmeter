# CloudMeter 系统开放 API 规范

## 1. 适用范围

该 API 供受信任的大模型、脚本、运维工具和第三方集成读取系统信息，并执行经过白名单限制的管理操作。它不是数据库直连接口，不提供任意 SQL、密码或 Secret 访问能力。

基础路径：`https://你的控制台域名/api/automation/v1`

旧版 `/api/llm/v1` 路径与 `cm_llm_` 密钥继续兼容，新接入应使用 `/api/automation/v1` 和 `cm_api_` 密钥。

## 2. 鉴权

超级管理员在“系统设置 → 系统开放 API 与访问密钥”中生成或轮换密钥。密钥明文只显示一次，服务端仅保存 SHA-256 摘要。

每个请求必须包含：

```http
Authorization: Bearer cm_api_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
Content-Type: application/json
```

密钥轮换后旧密钥立即失效。不要把密钥写入提示词、代码仓库、日志或浏览器端代码；应使用模型运行环境的 Secret/环境变量注入。

## 3. 响应与错误

所有响应使用 JSON。错误格式：

```json
{
  "error": {
    "code": "validation_failed",
    "message": "具体错误说明"
  }
}
```

常见状态码：`200` 成功，`400` 参数错误，`401` 密钥无效或已撤销，`500` 服务端错误。

## 4. 系统分析

`GET /analysis` 返回系统展示设置与汇总指标。指标包括用户数、应用数、运行中应用数、待完成部署任务数，以及最近 24 小时失败类审计事件数。返回内容不包含密码、会话令牌、支付密钥、应用 Secret 或个人账单明细。

```bash
curl -sS "$CLOUDMETER_URL/api/automation/v1/analysis" \
  -H "Authorization: Bearer $CLOUDMETER_API_KEY"
```

## 5. 读取系统设置

`GET /settings/system` 返回当前系统名称、品牌内容、首页内容及法务声明。

```bash
curl -sS "$CLOUDMETER_URL/api/automation/v1/settings/system" \
  -H "Authorization: Bearer $CLOUDMETER_API_KEY"
```

## 6. 修改系统设置

`PATCH /settings/system` 接受部分更新，只修改请求中出现的字段。允许字段：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `systemName` | string | 系统名称，1 到 64 个字符 |
| `logoUrl` | string | 品牌徽标 URL |
| `footerText` | string | 全局页脚 |
| `aboutContent` | string | 关于内容或完整 URL |
| `homepageContent` | string | 首页 HTML 富文本 |
| `termsOfService` | string | 用户协议 |
| `privacyPolicy` | string | 隐私政策 |

网站入口、域名、TLS、DNS 凭据等高风险配置不在该接口的写入白名单中。

```bash
curl -sS -X PATCH "$CLOUDMETER_URL/api/automation/v1/settings/system" \
  -H "Authorization: Bearer $CLOUDMETER_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"footerText":"由 CloudMeter 提供服务"}'
```

成功后返回完整的系统设置。每次写入都会产生 `llm.system.settings.update` 审计记录。

## 7. 模型调用约束

1. 修改前先调用 `GET /settings/system` 获取当前值。
2. 只发送需要更改的字段，不回传无关字段。
3. 不把分析结果中的计数推断为具体用户身份或业务结论。
4. 收到 `400` 时根据错误信息修正参数，不重复提交相同无效请求。
5. 收到 `401` 时停止调用并通知管理员轮换或重新配置密钥。
6. 对首页 HTML、协议和隐私政策的变更应保留原意，避免注入脚本和不受信任的外部内容。

## 8. 用户、日志与数据库运维

受信任的工具还可以使用以下管理接口。它们接受 `cm_api_` 密钥，并且每次写操作都会写入审计日志。

- `GET /users`：读取脱敏用户清单（不含密码、角色细节、钱包和会话）。
- `PATCH /users/{userID}`：修改 `displayName` 或 `status`（仅允许 `active`、`suspended`）。暂停用户会撤销其现有会话。
- `GET /logs/audit`：读取最近 100 条审计事件。
- `POST /logs/runtime/clear`：清理应用运行日志。
- `GET /database/summary`：查看 PostgreSQL 类型和公开表名，不返回行数据。
- `POST /database/maintenance`：执行白名单维护操作，目前仅支持 `operation=analyze`。

接口不会提供任意 SQL、建表/删表、原始日志批量篡改、用户密码重置、超级管理员角色变更、支付或应用 Secret 读写能力。
