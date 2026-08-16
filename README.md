# navdash

个人服务门户：卡片化的服务导航 + 原生 OIDC 登录（authorization code + PKCE）。

前端视觉语言取自 DeepSeek Harness 官网的公开设计 token（品牌蓝、暖黑/暖白双主题、
Host Grotesk + DM Sans），无构建步骤；后端为纯 Go 标准库单二进制，无数据库、
无第三方依赖，session 使用 HMAC 签名 cookie（无状态）。

服务卡片清单不在本仓库维护——由 NixOS 配置在**求值期**从全集群的 nginx vhost
定义自动收集生成 `entries.json`（含 `accessibleBy` 分级），后端按登录态下发：

- 未登录：仅 `public` 条目 + 登录入口
- 已登录：`public` + `private` 全量

## 端点

| 路径 | 说明 |
| --- | --- |
| `/` | 静态前端（embed） |
| `/auth/login` | 302 到 OIDC provider（state + PKCE S256） |
| `/auth/callback` | code 换 token → userinfo → 签名 session cookie |
| `/auth/logout` | 清除 session |
| `/api/me` | `{authenticated, username, email}` |
| `/api/entries` | `{entries: [...]}`（按登录态过滤） |
| `/healthz` | 健康检查 |

## 配置（环境变量）

| 变量 | 必填 | 说明 |
| --- | --- | --- |
| `NAVDASH_LISTEN` | 否 | 监听地址，默认 `127.0.0.1:13828`（NixOS 集成固定为 `127.0.0.1:13833`，见 nixos-config `helpers/constants/ports.nix`） |
| `NAVDASH_BASE_URL` | 否 | 公开 origin（构造 redirect_uri），默认 `http://<listen>` |
| `NAVDASH_OIDC_ISSUER` | 是 | OIDC issuer，如 `https://login.example.com` |
| `NAVDASH_OIDC_CLIENT_ID` | 是 | client id |
| `NAVDASH_OIDC_CLIENT_SECRET` | 是 | client secret |
| `NAVDASH_SESSION_KEY` | 是 | HMAC 密钥（≥32 字节 hex） |
| `NAVDASH_ALLOWED_USERS` | 否 | 逗号分隔的 `preferred_username` 白名单；空 = 不限制 |
| `NAVDASH_ENTRIES` | 否 | entries.json 路径，默认 `./entries.json` |

## entries.json 格式

```json
{
  "entries": [
    {
      "name": "git",
      "highlight": "git",
      "suffix": ".zhyi.xin",
      "proto": "https://",
      "url": "https://git.zhyi.xin",
      "host": "greencloud",
      "access": "public"
    }
  ]
}
```

## 字体

`web/assets/fonts/` 内字体来自 Google Fonts（DM Sans、Host Grotesk，SIL OFL 1.1），
许可证见同目录 `OFL.txt`。

## License

MIT（字体文件遵循 SIL OFL 1.1）。
